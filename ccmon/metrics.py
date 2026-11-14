"""Metrics over loaded sessions: tokens, repeats, review loops, re-checks, model WARN."""
from __future__ import annotations

import os
import re

from .transcript import EPOCH, TOKEN_CLASSES, Usage, sum_usage

ALIASES = ("haiku", "sonnet", "opus", "fable")
EXPLORER_TYPES = ("explorer", "Explore")
_WORD = re.compile(r"\w+")
_CITE = re.compile(r"([\w./~-]+\.\w+):\d+")
_VERDICT_HEAD = re.compile(r"^\s*#+\s*Verdict\b.*$", re.M | re.I)


def percentile(values, p):
    vals = sorted(values)
    if not vals:
        return None
    if len(vals) == 1:
        return float(vals[0])
    k = (len(vals) - 1) * p / 100.0
    lo = int(k)
    hi = min(lo + 1, len(vals) - 1)
    return vals[lo] + (vals[hi] - vals[lo]) * (k - lo)


# ---- model WARN -----------------------------------------------------------

def expected_model(agent_type, cwd, agents_dir=None):
    dirs = []
    if agents_dir:
        dirs.append(os.path.expanduser(agents_dir))
    else:
        if cwd:
            dirs.append(os.path.join(cwd, ".claude", "agents"))
        dirs.append(os.path.expanduser("~/.claude/agents"))
    if not agent_type or "/" in agent_type or agent_type.startswith("."):
        return None
    for d in dirs:
        try:
            with open(os.path.join(d, agent_type + ".md"), "r", encoding="utf-8", errors="replace") as f:
                lines = f.read().splitlines()
        except OSError:
            continue
        if not lines or lines[0].strip() != "---":
            return None
        for ln in lines[1:]:
            if ln.strip() == "---":
                break
            m = re.match(r"\s*model\s*:\s*(.*?)\s*$", ln)
            if m:
                return m.group(1).strip("'\"") or None
        return None
    return None


def _strip_suffix(m):
    return re.sub(r"\[[^\]]*\]$", "", m).lower()


def model_matches(expected, actual):
    e = expected.strip().lower()
    if e in ("", "inherit"):
        return True
    if e in ALIASES:
        return e in actual.lower()
    return _strip_suffix(e) == _strip_suffix(actual)


# ---- per-session analysis ---------------------------------------------------

def _tokens(d):
    return set(_WORD.findall((d.description + " " + d.prompt).lower()))


def jaccard(a, b):
    if not a and not b:
        return 0.0
    return len(a & b) / len(a | b)


def _is_changes_needed(text):
    m = _VERDICT_HEAD.search(text)
    if m:
        rest = re.sub(r"^\s*#+\s*Verdict\b", "", m.group(0), flags=re.I).strip(" :-*\t")
        if rest:  # verdict given on the heading line itself
            return "CHANGES_NEEDED" in rest
        for ln in text[m.end():].splitlines():
            if ln.strip():
                return "CHANGES_NEEDED" in ln
        return False
    return "CHANGES_NEEDED" in text


def _flag(d, f):
    if f not in d.flags:
        d.flags.append(f)


def analyze(session, agents_dir=None, repeat_threshold=0.5):
    ds = session.dispatches
    for d in ds:
        d.flags = []
        d.review_loops_dispatch = d.review_loops_resume = 0
    # model WARN
    session.warnings = []
    for d in ds:
        d.warnings = []
        exp = expected_model(d.agent_type, session.cwd, agents_dir)
        if not exp:
            continue
        for m in d.models:
            if m == "unknown" or model_matches(exp, m):
                continue
            w = "agent %s expected model %s, used %s" % (d.agent_type, exp, m)
            d.warnings.append(w)
            session.warnings.append(w)
        if d.warnings:
            _flag(d, "WARN")
    # repeats
    by_type = {}
    for d in ds:
        by_type.setdefault(d.agent_type, []).append(d)
    for lst in by_type.values():
        prior = []
        for d in lst:
            toks = _tokens(d)
            best, best_d = 0.0, None
            for p, pt in prior:
                sc = jaccard(toks, pt)
                if sc > best:
                    best, best_d = sc, p
            d.repeat_of, d.repeat_score = None, None
            if best_d is not None and best >= repeat_threshold:
                d.repeat_of, d.repeat_score = best_d, round(best, 4)
                _flag(d, "repeat")
            prior.append((d, toks))
    # review loops
    events = []  # (ts, order, kind, dispatch)
    for d in ds:
        if d.agent_type == "reviewer":
            for ts, text in d.results:
                if _is_changes_needed(text):
                    events.append((ts or EPOCH, 0, "verdict", d))
        elif d.agent_type == "coder":
            events.append((d.ts or EPOCH, 1, "dispatch", d))
    for ts, d in session.sends:
        if d.agent_type == "coder":
            events.append((ts or EPOCH, 1, "resume", d))
    events.sort(key=lambda e: (e[0], e[1]))
    pending = []  # FIFO of (verdict ts, reviewer dispatch)
    session.review_loop_events = []
    for ts, _, kind, d in events:
        if kind == "verdict":
            pending.append((ts, d))
            continue
        if not pending:
            continue
        vts, rv = pending.pop(0)
        if kind == "dispatch":
            d.review_loops_dispatch = 1
        else:
            d.review_loops_resume += 1
        session.review_loop_events.append({
            "verdict_at": vts.isoformat() if vts != EPOCH else None,
            "reviewer_agent_id": rv.agent_id, "reviewer_tool_use_id": rv.tool_use_id,
            "coder_agent_id": d.agent_id, "coder_tool_use_id": d.tool_use_id,
            "via": kind, "at": ts.isoformat() if ts != EPOCH else None})
    session.review_loops = len(session.review_loop_events)
    for d in ds:
        if d.review_loops_dispatch:
            _flag(d, "review-loop")
        if d.review_loops_resume:
            _flag(d, "review-loop(resume x%d)" % d.review_loops_resume)
    # re-check
    session.recheck_candidates = session.rechecked = 0
    for d in ds:
        if d.agent_type not in EXPLORER_TYPES:
            continue
        cited = set()
        for _, text in d.results:
            for c in _CITE.findall(text):
                c = c[2:] if c.startswith("./") else c
                c = c[2:] if c.startswith("~/") else c
                if c:
                    cited.add(c)
        if not cited:
            continue
        session.recheck_candidates += 1
        after = d.returned_at
        hit = False
        for ts, fp in session.reads:
            if after is not None and (ts is None or ts <= after):
                continue
            if any(fp == c or fp.endswith("/" + c.lstrip("/")) for c in cited):
                hit = True
                break
        if hit:
            session.rechecked += 1
            _flag(d, "rechecked")


# ---- aggregation ------------------------------------------------------------

def usage_by_agent(session):
    out = {"main": sum_usage(session.main_usage_by_model)}
    for d in session.dispatches:
        out.setdefault(d.agent_type, Usage()).add(d.usage)
    return out


def usage_by_model(session):
    out = {}
    for src in [session.main_usage_by_model] + [d.usage_by_model for d in session.dispatches]:
        for m, u in src.items():
            out.setdefault(m, Usage()).add(u)
    return out


def _pct(n, d):
    return round(100.0 * n / d, 1) if d else 0.0


def dispatch_dict(d):
    return {
        "tool_use_id": d.tool_use_id,
        "agent_id": d.agent_id,
        "agent_type": d.agent_type,
        "description": d.description,
        "prompt_chars": len(d.prompt),
        "requested_model": d.requested_model,
        "depth": d.depth,
        "parent_agent_id": d.parent_agent_id,
        "status": d.status,
        "start": d.start.isoformat() if d.start else None,
        "returned_at": d.returned_at.isoformat() if d.returned_at else None,
        "duration_ms": d.duration_ms,
        "models": d.models,
        "usage": d.usage.to_dict(),
        "usage_by_model": {m: u.to_dict() for m, u in sorted(d.usage_by_model.items())},
        "resumes": d.resumes,
        "flags": list(d.flags),
        "review_loops_dispatch": d.review_loops_dispatch,
        "review_loops_resume": d.review_loops_resume,
        "repeat_of": d.repeat_of.tool_use_id or d.repeat_of.agent_id if d.repeat_of else None,
        "repeat_score": d.repeat_score,
        "warnings": list(d.warnings),
    }


def totals(sessions):
    """The numbers a session, a task and a repository share, computed once for a list of sessions."""
    by_agent, by_model = {}, {}
    for s in sessions:
        for a, u in usage_by_agent(s).items():
            by_agent.setdefault(a, Usage()).add(u)
        for m, u in usage_by_model(s).items():
            by_model.setdefault(m, Usage()).add(u)
    main_total = by_agent["main"].total
    sub_total = sum(u.total for k, u in by_agent.items() if k != "main")
    total = main_total + sub_total
    cands = sum(s.recheck_candidates for s in sessions)
    rechecked = sum(s.rechecked for s in sessions)
    starts = [s.first_ts for s in sessions if s.first_ts]
    ends = [s.last_ts for s in sessions if s.last_ts]
    return {
        "start": min(starts).isoformat() if starts else None,
        "end": max(ends).isoformat() if ends else None,
        "tokens": {"total": total, "main": main_total, "subagents": sub_total,
                   "main_pct": _pct(main_total, total), "subagent_pct": _pct(sub_total, total)},
        "by_model": {m: u.to_dict() for m, u in sorted(by_model.items())},
        "by_agent": {a: u.to_dict() for a, u in sorted(by_agent.items())},
        "dispatch_count": sum(len(s.dispatches) for s in sessions),
        "repeats": sum(1 for s in sessions for d in s.dispatches if "repeat" in d.flags),
        "review_loops": sum(s.review_loops for s in sessions),
        "recheck": {"explorer_with_citations": cands, "rechecked": rechecked,
                    "rate": rechecked / cands if cands else None},
        "warn_count": sum(len(s.warnings) for s in sessions),
        "malformed": sum(s.malformed for s in sessions),
    }


def _group(sessions):
    sessions = sorted(sessions, key=lambda s: s.first_ts or EPOCH)
    return {"sessions": [s.id for s in sessions], "session_count": len(sessions), **totals(sessions)}


def group_repos(sessions):
    """Repositories (by name, then path), each with its tasks (by name); sessions without a task count only in the repository."""
    repos = {}
    for s in sessions:
        repos.setdefault((s.repo, s.repo_path), []).append(s)
    out = []
    for (name, path), rs in sorted(repos.items(), key=lambda kv: (kv[0][0], kv[0][1] or "")):
        tasks = {}
        for s in rs:
            if s.task:
                tasks.setdefault(s.task, []).append(s)
        out.append({"repo": name, "repo_path": path, **_group(rs),
                    "tasks": [{"task": t, **_group(ts)} for t, ts in sorted(tasks.items())]})
    return out


def session_summary(session, detail=False):
    out = {
        "id": session.id,
        "project": session.project,
        "project_dir": session.project_dir,
        "cwd": session.cwd,
        "repo": session.repo,
        "repo_path": session.repo_path,
        "task": session.task,
        "worktrees": list(session.worktrees),
        "path": session.path,
        **totals([session]),
        "review_loop_events": list(session.review_loop_events),
        "warnings": list(session.warnings),
    }
    if detail:
        out["dispatches"] = [dispatch_dict(d) for d in session.dispatches]
    return out


def aggregate_agents(sessions):
    groups = {}
    models_total = {}
    for s in sessions:
        for m, u in usage_by_model(s).items():
            models_total.setdefault(m, Usage()).add(u)
        mu = sum_usage(s.main_usage_by_model)
        g = groups.setdefault("main", {"dispatches": [], "usage": Usage(), "models": {}, "main": True})
        g["usage"].add(mu)
        for m, u in s.main_usage_by_model.items():
            g["models"].setdefault(m, Usage()).add(u)
        for d in s.dispatches:
            g = groups.setdefault(d.agent_type, {"dispatches": [], "usage": Usage(), "models": {}})
            g["dispatches"].append(d)
            g["usage"].add(d.usage)
            for m, u in d.usage_by_model.items():
                g["models"].setdefault(m, Usage()).add(u)
    rows = []
    for name, g in sorted(groups.items(), key=lambda kv: (kv[0] != "main", kv[0])):
        ds = g["dispatches"]
        toks = [d.usage.total for d in ds if d.status != "no-run"]
        durs = [d.duration_ms for d in ds if d.duration_ms is not None]
        rows.append({
            "agent": name,
            "dispatches": len(ds),
            "models": sorted(g["models"]),
            "usage": g["usage"].to_dict(),
            "usage_by_model": {m: u.to_dict() for m, u in sorted(g["models"].items())},
            "tokens_p50": percentile(toks, 50), "tokens_p90": percentile(toks, 90),
            "duration_ms_p50": percentile(durs, 50), "duration_ms_p90": percentile(durs, 90),
            "repeats": sum(1 for d in ds if "repeat" in d.flags),
            "review_loops": sum(d.review_loops_dispatch + d.review_loops_resume for d in ds),
            "warn_count": sum(len(d.warnings) for d in ds),
        })
    return {"agents": rows,
            "by_model": {m: u.to_dict() for m, u in sorted(models_total.items())},
            "sessions": len(sessions),
            "malformed": sum(s.malformed for s in sessions)}
