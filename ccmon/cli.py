"""Command line interface: argparse, --since parsing, table and JSON rendering."""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone

from . import metrics, transcript
from .transcript import SessionError

DEFAULT_PROJECTS = "~/.claude/projects"


# ---- formatting ---------------------------------------------------------------

def human(n):
    if n is None:
        return "-"
    n = float(n)
    for lim, suf in ((1e9, "G"), (1e6, "M"), (1e3, "k")):
        if abs(n) >= lim:
            return "%.1f%s" % (n / lim, suf)
    return "%d" % round(n)


def fmt_duration(ms):
    if ms is None:
        return "-"
    s = int(round(ms / 1000.0))
    if s < 60:
        return "%ds" % s
    m, s = divmod(s, 60)
    if m < 60:
        return "%dm%02ds" % (m, s)
    h, m = divmod(m, 60)
    return "%dh%02dm" % (h, m)


def fmt_dt(d, with_date=True):
    if not d:
        return "-"
    d = d.astimezone(timezone.utc)
    return d.strftime("%Y-%m-%d %H:%M" if with_date else "%H:%M:%S")


def fmt_rate(r):
    return "-" if r is None else "%d%%" % round(r * 100)


def table(headers, rows, left=()):
    """Plain aligned text; columns named in `left` are left-aligned, others right."""
    rows = [[str(c) for c in r] for r in rows]
    widths = [len(h) for h in headers]
    for r in rows:
        for i, c in enumerate(r):
            widths[i] = max(widths[i], len(c))

    def line(cells):
        out = []
        for i, c in enumerate(cells):
            out.append(c.ljust(widths[i]) if headers[i] in left else c.rjust(widths[i]))
        return "  ".join(out).rstrip()

    return "\n".join([line(headers)] + [line(r) for r in rows])


# ---- --since --------------------------------------------------------------------

def parse_since(text, now=None):
    now = now or datetime.now(timezone.utc)
    m = re.fullmatch(r"(\d+)([dhw])", text.strip())
    if m:
        n, u = int(m.group(1)), m.group(2)
        return now - timedelta(**{{"d": "days", "h": "hours", "w": "weeks"}[u]: n})
    try:
        d = datetime.fromisoformat(text.strip())
    except ValueError:
        raise SessionError("invalid --since value %r (use Nd, Nh, Nw or an ISO date)" % text)
    if d.tzinfo is None:
        d = d.replace(tzinfo=timezone.utc)
    return d


def load_all(args):
    paths = transcript.discover_sessions(args.projects_dir)
    cutoff = parse_since(args.since) if getattr(args, "since", None) else None
    sessions = []
    for p in paths:
        if cutoff is not None:
            try:
                if os.path.getmtime(p) < cutoff.timestamp():
                    continue
            except OSError:
                continue
        s = transcript.load_session(p)
        if cutoff is not None and s.last_ts is not None and s.last_ts < cutoff:
            continue
        if getattr(args, "repo", None) not in (None, s.repo) or getattr(args, "task", None) not in (None, s.task):
            continue
        metrics.analyze(s, args.agents_dir, args.repeat_threshold)
        sessions.append(s)
    sessions.sort(key=lambda s: s.first_ts or transcript.EPOCH)
    return sessions


# ---- commands -----------------------------------------------------------------

def cmd_sessions(args):
    sessions = load_all(args)
    sums = [metrics.session_summary(s) for s in sessions]
    repos = metrics.group_repos(sessions)
    malformed = sum(x["malformed"] for x in sums)
    if args.json:
        emit_json({"sessions": sums, "repos": repos, "malformed": malformed})
        return 0
    headers = ["repo / task / session", "start (UTC)", "total", "main%", "sub%", "disp", "repeat",
               "loops", "recheck", "warn", "malformed"]
    by_id = {x["id"]: x for x in sums}

    def row(label, x):
        t = x["tokens"]
        return [label, x["start"] and fmt_dt(datetime.fromisoformat(x["start"])) or "-",
                human(t["total"]), "%.0f" % t["main_pct"], "%.0f" % t["subagent_pct"],
                x["dispatch_count"], x["repeats"], x["review_loops"],
                fmt_rate(x["recheck"]["rate"]), x["warn_count"], x["malformed"]]

    rows = []
    for r in repos:
        rows.append(row(r["repo"], r))
        in_task = {i for t in r["tasks"] for i in t["sessions"]}
        rows.extend(row("  " + i[:8], by_id[i]) for i in r["sessions"] if i not in in_task)
        for t in r["tasks"]:
            rows.append(row("  " + t["task"], t))
            rows.extend(row("    " + i[:8], by_id[i]) for i in t["sessions"])
    print(table(headers, rows, left=("repo / task / session", "start (UTC)")))
    print("\n%d repositories, %d tasks, %d sessions, %d malformed lines" % (
        len(repos), sum(len(r["tasks"]) for r in repos), len(sums), malformed))
    return 0


def usage_rows(mapping):
    return [[k, human(v["input"]), human(v["output"]), human(v["cache_write"]),
             human(v["cache_read"]), human(v["total"])] for k, v in mapping.items()]


def cmd_session(args):
    path = transcript.find_session(args.projects_dir, args.id)
    s = transcript.load_session(path)
    metrics.analyze(s, args.agents_dir, args.repeat_threshold)
    x = metrics.session_summary(s, detail=True)
    if args.json:
        emit_json(x)
        return 0
    t = x["tokens"]
    print("session   %s" % x["id"])
    others = [w for w in x["worktrees"] if w != x["task"]]
    print("repo      %s (%s)" % (x["repo"], x["repo_path"] or x["project_dir"]))
    print("task      %s%s" % (x["task"] or "-", " (also entered: %s)" % ", ".join(others) if others else ""))
    print("span      %s .. %s UTC" % (fmt_dt(s.first_ts), fmt_dt(s.last_ts)))
    print("tokens    total %s, main %s (%.1f%%), subagents %s (%.1f%%)" % (
        human(t["total"]), human(t["main"]), t["main_pct"], human(t["subagents"]), t["subagent_pct"]))
    rr = x["recheck"]
    print("signals   dispatches %d, repeats %d, review loops %d, re-check rate %s (%d/%d), malformed lines %d" % (
        x["dispatch_count"], x["repeats"], x["review_loops"], fmt_rate(rr["rate"]),
        rr["rechecked"], rr["explorer_with_citations"], x["malformed"]))
    uh = ["", "input", "output", "cache-w", "cache-r", "total"]
    print("\nBy model")
    print(table(["model"] + uh[1:], usage_rows(x["by_model"]), left=("model",)))
    print("\nBy agent")
    print(table(["agent"] + uh[1:], usage_rows(x["by_agent"]), left=("agent",)))
    if x["warnings"]:
        print("\nWarnings")
        for w in x["warnings"]:
            print("  WARN %s" % w)
    print("\nDispatches")
    if not s.dispatches:
        print("  (none)")
        return 0
    rows = []
    for d in s.dispatches:
        u = d.usage
        flags = list(d.flags)
        if d.status != "completed":
            flags.append(d.status)
        desc = (d.description.strip().splitlines() or [""])[0]
        rows.append([fmt_dt(d.start, False), ("  " * (d.depth - 1) if d.depth > 1 else "") + d.agent_type,
                     ",".join(m.replace("claude-", "") for m in d.models) or "-",
                     human(u.input), human(u.output), human(u.total), fmt_duration(d.duration_ms),
                     ",".join(flags) or "-", desc[:60]])
    print(table(["time", "agent", "model", "in", "out", "total", "duration", "flags", "description"],
                rows, left=("time", "agent", "model", "flags", "description")))
    return 0


def cmd_agents(args):
    sessions = load_all(args)
    agg = metrics.aggregate_agents(sessions)
    if args.json:
        emit_json(agg)
        return 0
    rows = []
    for a in agg["agents"]:
        u = a["usage"]
        rows.append([a["agent"], a["dispatches"], ",".join(m.replace("claude-", "") for m in a["models"]) or "-",
                     human(u["input"]), human(u["output"]), human(u["cache_write"]), human(u["cache_read"]),
                     human(a["tokens_p50"]), human(a["tokens_p90"]),
                     fmt_duration(a["duration_ms_p50"]), fmt_duration(a["duration_ms_p90"]),
                     a["repeats"], a["warn_count"]])
    print(table(["agent", "disp", "models", "input", "output", "cache-w", "cache-r",
                 "tok p50", "tok p90", "dur p50", "dur p90", "repeat", "warn"], rows,
                left=("agent", "models")))
    print("\n%d sessions, %d malformed lines" % (agg["sessions"], agg["malformed"]))
    return 0


def emit_json(obj):
    json.dump(obj, sys.stdout, indent=2, default=str)
    sys.stdout.write("\n")


# ---- entry point ------------------------------------------------------------------

def _common(suppress):
    p = argparse.ArgumentParser(add_help=False)
    d = argparse.SUPPRESS if suppress else None
    p.add_argument("--projects-dir", default=d or DEFAULT_PROJECTS, help="transcripts root (default %s)" % DEFAULT_PROJECTS)
    p.add_argument("--agents-dir", default=d, help="agent definitions dir (overrides cwd/.claude/agents and ~/.claude/agents)")
    p.add_argument("--repeat-threshold", type=float, default=d or 0.5, help="Jaccard threshold for repeat flag (default 0.5)")
    p.add_argument("--json", action="store_true", default=d or False, help="emit one JSON document")
    return p


def build_parser():
    p = argparse.ArgumentParser(prog="ccmon", description="Report how a Claude Code main session delegates to subagents.",
                                parents=[_common(False)])
    sub = p.add_subparsers(dest="command", required=True)
    since = argparse.ArgumentParser(add_help=False)
    since.add_argument("--since", help="Nd, Nh, Nw or an ISO date")
    scope = argparse.ArgumentParser(add_help=False)
    scope.add_argument("--repo", help="only this repository (name)")
    scope.add_argument("--task", help="only this task (worktree name)")
    s = sub.add_parser("sessions", parents=[_common(True), since, scope], help="sessions grouped by repository and task")
    s.set_defaults(func=cmd_sessions)
    s = sub.add_parser("session", parents=[_common(True)], help="one session in detail")
    s.add_argument("id", help="session id or unique prefix")
    s.set_defaults(func=cmd_session)
    s = sub.add_parser("agents", parents=[_common(True), since, scope], help="per agent type aggregates")
    s.set_defaults(func=cmd_agents)
    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except SessionError as e:
        print("ccmon: %s" % e, file=sys.stderr)
        return 2
    except BrokenPipeError:
        return 0
