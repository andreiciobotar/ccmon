"""Command line interface: argparse, --since parsing, table and JSON rendering."""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone

from . import metrics, prices as price_file, transcript
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


def money(x, currency, unpriced=0):
    """`$` for USD, else the code and a space; a trailing `*` when some tokens had no price."""
    prefix = "$" if currency == "USD" else currency + " "
    if x <= 0:
        s = "0"
    elif x < 0.00005:
        prefix, s = "<" + prefix, "0.0001"
    elif x < 0.00995:
        s = "%.4f" % x
    elif round(x, 2) < 100:
        s = "%.2f" % x
    elif round(x) < 1e4:
        s = "%.0f" % x
    elif round(x / 1e3, 1) < 1e3:
        s = "%.1fk" % (x / 1e3)
    else:
        s = "%.1fM" % (x / 1e6)
    return prefix + s + ("*" if unpriced else "")


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


def footer(prices, models, tokens):
    """The prices line, plus the unpriced line when some tokens had no price."""
    print("prices: %s as of %s (%s)" % (prices.currency, prices.as_of, prices.path))
    if models:
        print("* no price for: %s (%s tokens); cost excludes them" % (", ".join(models), human(tokens)))


def load_all(args, prices):
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
        s = transcript.load_session(p, prices)
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
    prices = price_file.load(args.prices)
    sessions = load_all(args, prices)
    sums = [metrics.session_summary(s) for s in sessions]
    repos = metrics.group_repos(sessions)
    malformed = sum(x["malformed"] for x in sums)
    if args.json:
        emit_json({"sessions": sums, "repos": repos, "malformed": malformed, "prices": prices.to_dict()})
        return 0
    headers = ["repo / task / session", "start (UTC)", "cost", "main%", "sub%", "tokens", "disp", "repeat",
               "loops", "recheck", "warn", "malformed"]
    by_id = {x["id"]: x for x in sums}

    def row(label, x):
        t, c = x["tokens"], x["cost"]
        return [label, x["start"] and fmt_dt(datetime.fromisoformat(x["start"])) or "-",
                money(c["total"], prices.currency, x["cost"]["unpriced_tokens"]),
                "%.0f" % c["main_pct"], "%.0f" % c["subagent_pct"], human(t["total"]),
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
    footer(prices, sorted({m for x in sums for m in x["cost"]["unpriced_models"]}), sum(x["cost"]["unpriced_tokens"] for x in sums))
    return 0


def usage_rows(mapping, currency):
    """One row per key; an entry with only unpriced tokens shows `unpriced` as its cost."""
    return [[k, human(v["input"]), human(v["output"]), human(v["cache_write"]), human(v["cache_read"]),
             human(v["total"]),
             "unpriced" if v["unpriced"] and not v["cost"] else money(v["cost"], currency, v["unpriced"])]
            for k, v in mapping.items()]


def cmd_session(args):
    prices = price_file.load(args.prices)
    path = transcript.find_session(args.projects_dir, args.id)
    s = transcript.load_session(path, prices)
    metrics.analyze(s, args.agents_dir, args.repeat_threshold)
    x = metrics.session_summary(s, detail=True)
    x["prices"] = prices.to_dict()
    if args.json:
        emit_json(x)
        return 0
    t, c, cur = x["tokens"], x["cost"], prices.currency
    main_un = x["by_agent"]["main"]["unpriced"]
    print("session   %s" % x["id"])
    others = [w for w in x["worktrees"] if w != x["task"]]
    print("repo      %s (%s)" % (x["repo"], x["repo_path"] or x["project_dir"]))
    print("task      %s%s" % (x["task"] or "-", " (also entered: %s)" % ", ".join(others) if others else ""))
    print("span      %s .. %s UTC" % (fmt_dt(s.first_ts), fmt_dt(s.last_ts)))
    print("cost      total %s, main %s (%.1f%%), subagents %s (%.1f%%)" % (
        money(c["total"], cur, x["cost"]["unpriced_tokens"]), money(c["main"], cur, main_un), c["main_pct"],
        money(c["subagents"], cur, x["cost"]["unpriced_tokens"] - main_un), c["subagent_pct"]))
    print("tokens    total %s, main %s (%.1f%%), subagents %s (%.1f%%)" % (
        human(t["total"]), human(t["main"]), t["main_pct"], human(t["subagents"]), t["subagent_pct"]))
    rr = x["recheck"]
    print("signals   dispatches %d, repeats %d, review loops %d, re-check rate %s (%d/%d), malformed lines %d" % (
        x["dispatch_count"], x["repeats"], x["review_loops"], fmt_rate(rr["rate"]),
        rr["rechecked"], rr["explorer_with_citations"], x["malformed"]))
    uh = ["input", "output", "cache-w", "cache-r", "total", "cost"]
    print("\nBy model")
    print(table(["model"] + uh, usage_rows(x["by_model"], cur), left=("model",)))
    print("\nBy agent")
    print(table(["agent"] + uh, usage_rows(x["by_agent"], cur), left=("agent",)))
    if x["warnings"]:
        print("\nWarnings")
        for w in x["warnings"]:
            print("  WARN %s" % w)
    print("\nDispatches")
    if not s.dispatches:
        print("  (none)")
        footer(prices, x["cost"]["unpriced_models"], x["cost"]["unpriced_tokens"])
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
                     human(u.input), human(u.output), human(u.total), money(u.cost, cur, u.unpriced),
                     fmt_duration(d.duration_ms), ",".join(flags) or "-", desc[:60]])
    print(table(["time", "agent", "model", "in", "out", "total", "cost", "duration", "flags", "description"],
                rows, left=("time", "agent", "model", "flags", "description")))
    footer(prices, x["cost"]["unpriced_models"], x["cost"]["unpriced_tokens"])
    return 0


def cmd_agents(args):
    prices = price_file.load(args.prices)
    sessions = load_all(args, prices)
    agg = metrics.aggregate_agents(sessions)
    agg["prices"] = prices.to_dict()
    if args.json:
        emit_json(agg)
        return 0
    cur = prices.currency
    rows = []
    for a in agg["agents"]:
        u = a["usage"]
        rows.append([a["agent"], a["dispatches"], ",".join(m.replace("claude-", "") for m in a["models"]) or "-",
                     money(u["cost"], cur, u["unpriced"]),
                     *("-" if a[k] is None else money(a[k], cur, u["unpriced"]) for k in ("cost_p50", "cost_p90")),
                     human(u["input"]), human(u["output"]), human(u["cache_write"]), human(u["cache_read"]),
                     human(a["tokens_p50"]), human(a["tokens_p90"]),
                     fmt_duration(a["duration_ms_p50"]), fmt_duration(a["duration_ms_p90"]),
                     a["repeats"], a["warn_count"]])
    print(table(["agent", "disp", "models", "cost", "cost p50", "cost p90", "input", "output", "cache-w", "cache-r",
                 "tok p50", "tok p90", "dur p50", "dur p90", "repeat", "warn"], rows,
                left=("agent", "models")))
    print("\n%d sessions, %d malformed lines" % (agg["sessions"], agg["malformed"]))
    footer(prices, agg["unpriced_models"], sum(a["usage"]["unpriced"] for a in agg["agents"]))
    return 0


def cmd_kaizen(args):
    from . import kaizen  # kaizen imports this module's formatters

    prices = price_file.load(args.prices)
    cutoff = parse_since(args.since) if args.since else None
    doc = kaizen.build(args.factory_dir, lambda: load_all(args, prices), prices, cutoff)
    if args.json:
        emit_json(doc)
        return 0
    s, cur = doc["summary"], prices.currency
    if not doc["tasks"]:
        print("no factory tasks in %s" % doc["factory_dir"])
        return 0
    med = lambda m, f: "-" if s[m]["median"] is None else f(s[m]["median"])  # noqa: E731
    print("tasks %d, landed %d, in flight %d" % (s["tasks"], s["landed"], s["in_flight"]))
    print("median over landed: cost %s, brief to PR %s, PR to merge %s, review rounds %s; first pass %d/%d" % (
        med("cost", lambda v: money(v, cur)), med("brief_to_pr_s", lambda v: fmt_duration(v * 1000)),
        med("pr_to_merge_s", lambda v: fmt_duration(v * 1000)), med("review_rounds", lambda v: "%g" % v),
        s["first_pass"]["yes"], s["first_pass"]["of"]))
    for r in doc["repos"]:
        if r["prs"] != "ok":
            print("pull requests unavailable for %s: %s" % (r["repo"], r["prs"]))
    dur = lambda v: "-" if v is None else fmt_duration(v * 1000)  # noqa: E731
    rows = [[t["slug"], t["version"] or "-", "landed" if t["landed"] else t["state"] or "-", dur(t["brief_to_pr_s"]),
             dur(t["pr_to_merge_s"]), "-" if t["cost"] is None else money(t["cost"]["total"], cur, t["cost"]["unpriced_tokens"]),
             "-" if t["review_rounds"] is None else t["review_rounds"], t["stops"]] for t in doc["tasks"]]
    print()
    print(table(["task", "version", "state", "build", "merge", "cost", "rounds", "stops"], rows, left=("task", "version", "state")))
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
    p.add_argument("--prices", default=d, help="price file (default isshoni/prices.toml)")
    p.add_argument("--json", action="store_true", default=d or False, help="emit one JSON document")
    return p


def build_parser():
    p = argparse.ArgumentParser(prog="isshoni", description="Report how a Claude Code main session delegates to subagents.",
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
    s = sub.add_parser("kaizen", parents=[_common(True), since], help="improvement measures of the factory")
    s.add_argument("--factory-dir", default="~/.claude/factory", help="the factory's stores (default ~/.claude/factory)")
    s.set_defaults(func=cmd_kaizen)
    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except SessionError as e:
        print("isshoni: %s" % e, file=sys.stderr)
        return 2
    except BrokenPipeError:
        return 0
