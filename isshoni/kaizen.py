"""Kaizen measures: is the factory getting better. Reads the factory's stores read-only (docs/factory.md, "Logs")."""
from __future__ import annotations

import glob
import json
import os
import re
import subprocess
from datetime import datetime, timezone

from . import metrics, transcript
from .cli import fmt_duration

ROLES = ("lead", "coder", "reviewer", "explorer", "other")
DEFECT_KINDS = ("tests", "reviewer", "lead", "ci", "human", "other")
STOP_KINDS = ("QUESTION", "BLOCKED", "OVERTIME")
STATUSES = ("open", "accepted", "rejected", "done")
TREND_MIN = 10
GH_FIELDS = "number,url,state,headRefName,createdAt,mergedAt"
TOPICS = [(name, re.compile(pat, re.I)) for name, pat in (
    ("permissions", r"permission|denied|refus|sandbox|isolation|not allowed"),
    ("brief", r"\bbrief\b|\bspec\b|did not say|unstated|ambigu"),
    ("rules", r"\brule|lead\.md|role file|skill|coding-taste|instruction|CLAUDE\.md"),
    ("tools", r"\btool|command|\bgit\b|\bgh\b|bash|script|hook|sendmessage|worktree"),
    ("review", r"review|verdict"),
    ("time", r"time limit|overtime|slow|wait|minutes"),
)]


class GhError(Exception):
    """gh could not list the pull requests; the message is one line."""


# ---- reading the stores -------------------------------------------------------------

def _records(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                try:
                    rec = json.loads(line)
                except (ValueError, RecursionError):
                    continue
                if isinstance(rec, dict):
                    yield rec
    except OSError:
        return


def read_log(factory_dir, name):
    """The unsuffixed legacy file first, then the month files in name order."""
    d = glob.escape(factory_dir)
    paths = [os.path.join(factory_dir, name + ".jsonl")]
    paths += sorted(glob.glob(os.path.join(d, name + "-[0-9][0-9][0-9][0-9]-[0-9][0-9].jsonl")))
    return [r for p in paths for r in _records(p)]


def _str(rec, key):
    v = rec.get(key)
    return v if isinstance(v, str) and v else None


def _key(rec):
    repo, slug = _str(rec, "repo"), _str(rec, "slug")
    return (repo, slug) if repo and slug else None


def _name(repo_path):
    return os.path.basename(repo_path.rstrip("/")) or repo_path if repo_path else None


def _ts(rec):
    return transcript.parse_ts(rec.get("ts"))


def _iso(dt):
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ") if dt else None


def _seconds(later, earlier):
    return int(round((later - earlier).total_seconds()))


# ---- gh -----------------------------------------------------------------------------

def gh_prs(repo_path):
    """Every pull request of the repository, as gh lists them; GhError when gh cannot."""
    if not os.path.isdir(repo_path):
        raise GhError("no such repository path")
    try:
        p = subprocess.run(["gh", "pr", "list", "--state", "all", "--limit", "1000", "--json", GH_FIELDS],
                           cwd=repo_path, capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.SubprocessError) as e:
        raise GhError("gh unavailable: %s" % str(e).splitlines()[0] if str(e) else "gh unavailable")
    if p.returncode != 0:
        raise GhError((p.stderr.strip().splitlines() or ["gh exited %d" % p.returncode])[0])
    try:
        prs = json.loads(p.stdout)
    except ValueError:
        raise GhError("gh printed no JSON")
    if not isinstance(prs, list):
        raise GhError("gh printed an unexpected document")
    return prs


def _pr_for(prs, slug):
    mine = [p for p in prs if isinstance(p, dict) and p.get("headRefName") == "worktree-" + slug]
    if not mine:
        return None
    p = max(mine, key=lambda p: p.get("createdAt") if isinstance(p.get("createdAt"), str) else "")
    return {"number": p.get("number"), "url": p.get("url"), "state": p.get("state"),
            "opened": _iso(transcript.parse_ts(p.get("createdAt"))),
            "merged": _iso(transcript.parse_ts(p.get("mergedAt")))}


# ---- cost ---------------------------------------------------------------------------

def _dispatch_role(d):
    r = metrics.role(d.agent_type)
    return "explorer" if r in metrics.EXPLORER_TYPES else r if r in ROLES else "other"


def _cost(sessions):
    by_role = dict.fromkeys(ROLES, 0.0)
    unpriced = 0
    dispatches = []
    for s in sessions:
        main = transcript.sum_usage(s.main_usage_by_model)
        by_role["lead"] += main.cost
        unpriced += main.unpriced
        for d in s.dispatches:
            u = d.usage
            by_role[_dispatch_role(d)] += u.cost
            unpriced += u.unpriced
            dispatches.append(d)
    reviews = sorted((d for d in dispatches if metrics.role(d.agent_type) == "reviewer" and d.status != "no-run"),
                     key=lambda d: d.start or transcript.EPOCH)
    rework = 0.0
    after = reviews and (reviews[0].returned_at or reviews[0].start)
    if after:
        rework = sum((d.usage.cost for d in dispatches
                      if metrics.role(d.agent_type) in ("coder", "reviewer") and d.start and d.start > after), 0.0)
    return {"total": round(sum(by_role.values()), 6), "unpriced_tokens": unpriced,
            "by_role": {k: round(v, 6) for k, v in by_role.items()}, "rework": round(rework, 6)}, len(reviews)


# ---- tasks --------------------------------------------------------------------------

def _stops(events):
    """Stops in event order: a lead's QUESTION/BLOCKED/OVERTIME and a denied permission."""
    out = []
    for i, e in enumerate(events):
        key, ts = _key(e), _ts(e)
        if key is None or ts is None:
            continue
        kind, waited, why = None, None, None
        if e.get("event") == "PostToolUse" and e.get("tool_name") == "SendMessage" and e.get("kind") in STOP_KINDS:
            kind, why = e["kind"], _str(e, "summary")
            session = _str(e, "session")
            for nxt in events[i + 1:]:
                nts = _ts(nxt)
                if (session and nts and nxt.get("event") == "UserPromptSubmit" and nxt.get("session") == session
                        and nxt.get("from") in ("session", "human")):
                    waited = max(0, _seconds(nts, ts))
                    break
        elif e.get("event") == "PermissionDenied":
            kind = "DENIED"
            why = ": ".join(x for x in (_str(e, "tool_name"), _str(e, "reason")) if x) or None
        if kind:
            out.append({"ts": _iso(ts), "repo": _name(key[0]), "slug": key[1], "kind": kind,
                        "waited_s": waited, "why": why, "_key": key})
    return sorted(out, key=lambda s: s["ts"])


def _defects(retro):
    counts = dict.fromkeys(DEFECT_KINDS, 0)
    for d in retro.get("defects") if isinstance(retro.get("defects"), list) else []:
        if isinstance(d, dict):
            c = d.get("caught_by")
            counts[c if c in DEFECT_KINDS else "other"] += 1
    return counts


def _first_ts(records):
    stamped = [(t, r) for r in records for t in [_ts(r)] if t]
    return min(stamped, key=lambda x: x[0]) if stamped else None


def _task(key, recs, sessions):
    """recs: this task's records by store."""
    repo, slug = key
    started_rec = next((r for r in recs["events"] if r.get("event") == "TaskStarted" and _ts(r)), None)
    started = _ts(started_rec) if started_rec else None
    if started is None:
        firsts = [s.first_ts for s in sessions if s.first_ts]
        started = min(firsts) if firsts else None
    earliest = _first_ts([r for r in recs["all"] if _str(r, "version")])
    version = _str(started_rec, "version") if started_rec and _str(started_rec, "version") else (
        _str(earliest[1], "version") if earliest else None)
    states = [r["state"] for r in recs["tasks"] if _str(r, "state")]
    t = {"repo": _name(repo), "repo_path": repo, "slug": slug, "version": version, "started": started,
         "state": states[-1] if states else None, "pr": None, "landed": False,
         "brief_to_pr_s": None, "pr_to_merge_s": None, "cost": None, "review_rounds": None, "first_pass": None}
    if sessions:
        t["cost"], t["review_rounds"] = _cost(sessions)
        t["first_pass"] = t["review_rounds"] == 1
    t["stops"] = len(recs["stops"])
    t["stopped_s"] = sum(s["waited_s"] for s in recs["stops"] if s["waited_s"] is not None)
    retros = sorted(recs["retros"], key=lambda r: _ts(r) or transcript.EPOCH)
    t["defects"] = _defects(retros[-1]) if retros else None
    return t


def _apply_pr(t, pr):
    t["pr"] = pr
    t["landed"] = bool(pr and pr["merged"])
    if pr:
        opened, merged = transcript.parse_ts(pr["opened"]), transcript.parse_ts(pr["merged"])
        if opened and t["started"] and opened >= t["started"]:
            t["brief_to_pr_s"] = _seconds(opened, t["started"])
        if opened and merged:
            t["pr_to_merge_s"] = _seconds(merged, opened)


# ---- summary ------------------------------------------------------------------------

def _measures(t):
    return {"cost": t["cost"]["total"] if t["cost"] else None, "brief_to_pr_s": t["brief_to_pr_s"],
            "pr_to_merge_s": t["pr_to_merge_s"], "review_rounds": t["review_rounds"]}


def _median(values):
    vals = [v for v in values if v is not None]
    m = metrics.percentile(vals, 50)
    return None if m is None else round(m, 6)


def _trend(landed):
    if len(landed) < TREND_MIN:
        return None
    k = len(landed) // 2
    latest, earlier = [_measures(t) for t in landed[-k:]], [_measures(t) for t in landed[:-k]]
    out = {"latest_n": k, "earlier_n": len(landed) - k}
    for m in ("cost", "brief_to_pr_s", "pr_to_merge_s", "review_rounds"):
        out[m] = {"latest": _median(x[m] for x in latest), "earlier": _median(x[m] for x in earlier)}
    return out


def _rewards(landed):
    landed = sorted(landed, key=lambda t: (t["pr"]["merged"], t["slug"]))
    if not landed:
        return []
    out, last = [], landed[-1]
    streak = 0
    for t in reversed(landed):
        if t["first_pass"] is not True:
            break
        streak += 1
    if streak >= 3:
        out.append({"kind": "first_pass_streak", "slug": last["slug"], "text": "%d landed first-pass in a row" % streak})
    for kind, measure, text in (
            ("record_low_cost", "cost", lambda v: "record low cost: $%.2f" % v),
            ("record_fast", "brief_to_pr_s", lambda v: "fastest brief to pull request: " + fmt_duration(v * 1000))):
        vals = [_measures(t)[measure] for t in landed]
        have = [v for v in vals if v is not None]
        if len(have) >= 5 and vals[-1] is not None and vals[-1] < min(have[:-1]):
            out.append({"kind": kind, "slug": last["slug"], "text": text(vals[-1])})
    return out


def _summary(tasks):
    landed = [t for t in tasks if t["landed"]]
    ms = [_measures(t) for t in landed]
    med = lambda m: {"median": _median(x[m] for x in ms), "n": sum(1 for x in ms if x[m] is not None)}  # noqa: E731
    costed = [t["cost"] for t in landed if t["cost"]]
    total = sum(c["total"] for c in costed)
    first_pass = [t["first_pass"] for t in landed if t["first_pass"] is not None]
    defects = [t["defects"] for t in tasks if t["defects"]]
    return {
        "tasks": len(tasks), "landed": len(landed),
        "in_flight": sum(1 for t in tasks if not t["landed"] and t["state"] not in ("acknowledged", "dismissed")),
        "cost": med("cost"), "brief_to_pr_s": med("brief_to_pr_s"), "pr_to_merge_s": med("pr_to_merge_s"),
        "review_rounds": med("review_rounds"),
        "first_pass": {"yes": sum(first_pass), "of": len(first_pass)},
        "rework_share": round(sum(c["rework"] for c in costed) / total, 6) if total else None,
        "by_role": {r: round(sum(c["by_role"][r] for c in costed), 6) for r in ROLES},
        "time": {"building_s": sum(max(0, t["brief_to_pr_s"] - t["stopped_s"]) for t in landed if t["brief_to_pr_s"] is not None),
                 "stopped_s": sum(t["stopped_s"] for t in landed),
                 "merge_wait_s": sum(t["pr_to_merge_s"] for t in landed if t["pr_to_merge_s"] is not None)},
        "defects": {k: sum(d[k] for d in defects) for k in DEFECT_KINDS},
        "trend_min": TREND_MIN, "trend": _trend(landed),
    }


# ---- outbox -------------------------------------------------------------------------

def _topic(text):
    return next((name for name, rx in TOPICS if rx.search(text)), "other")


def _outbox(feedback, cutoff):
    entries, changes = {}, {}
    for r in feedback:
        fid = _str(r, "id")
        if not fid:
            continue
        if isinstance(r.get("text"), str):
            entries.setdefault(fid, r)
        else:
            changes.setdefault(fid, []).append(r)
    out = []
    for fid, r in entries.items():
        ts = _ts(r)
        if cutoff is not None and not (ts and ts >= cutoff):
            continue
        status, pr = _str(r, "status") or "open", None
        latest = None
        for c in changes.get(fid, []):  # the latest ts wins, a tie goes to the later line
            if latest is None or (_ts(c) or transcript.EPOCH) >= (_ts(latest) or transcript.EPOCH):
                latest = c
        if latest:
            status, pr = _str(latest, "status") or status, _str(latest, "pr")
        text = r["text"]
        decision = text.startswith("DECISION:")
        out.append({"id": fid, "ts": _iso(ts), "repo": _name(_str(r, "repo")), "slug": _str(r, "slug"),
                    "role": _str(r, "role"), "kind": "decision" if decision else "issue", "status": status,
                    "topic": None if decision else _topic(text), "text": text, "pr": pr})
    out.reverse()
    out.sort(key=lambda e: e["ts"] or "", reverse=True)
    counts = {k: {s: sum(1 for e in out if e["kind"] == k[:-1] and e["status"] == s) for s in STATUSES}
              for k in ("issues", "decisions")}
    topics = {}
    for e in out:
        if e["kind"] == "issue" and e["status"] == "open":
            topics[e["topic"]] = topics.get(e["topic"], 0) + 1
    order = [n for n, _ in TOPICS] + ["other"]
    ranked = sorted(topics, key=lambda n: (n == "other", -topics[n], order.index(n)))
    return {**counts, "topics": [{"topic": n, "open": topics[n]} for n in ranked], "entries": out}


# ---- the site manager ---------------------------------------------------------------

def _site_manager(records, cutoff):
    """The site manager's record, folded and counted as `factory firings` does, over every repository."""
    firings, misses = {}, 0
    suppressed_first = 0
    for r in sorted(records, key=lambda r: str(r.get("ts"))):
        fid = r.get("id")
        f = firings.get(fid) if isinstance(fid, str) else None
        if "miss" in r or (isinstance(fid, str) and "criterion" in r):
            ts = _ts(r)
            if cutoff is not None and not (ts and ts >= cutoff):
                continue
            if "miss" in r:
                misses += 1
                suppressed_first += bool(r.get("firing"))
            else:
                firings[fid] = {"decision": None, "outcome": None}
        elif f and "decision" in r and f["decision"] is None:
            f["decision"] = r["decision"]
        elif f and "outcome" in r:
            f["outcome"] = r["outcome"]
    escalated = [f for f in firings.values() if f["decision"] == "escalated"]
    acted = sum(f["outcome"] == "acted" for f in escalated)
    ignored = sum(f["outcome"] == "ignored" for f in escalated)
    ratio = lambda n, d: round(n / d, 6) if d else None  # noqa: E731
    return {"escalated": len(escalated), "acted": acted, "ignored": ignored,
            "no_outcome": len(escalated) - acted - ignored,
            "suppressed": sum(f["decision"] == "suppressed" for f in firings.values()), "missed": misses,
            "missed_suppressed": suppressed_first, "missed_never_fired": misses - suppressed_first,
            "precision": ratio(acted, acted + ignored), "recall": ratio(acted, acted + misses)}


# ---- the document -------------------------------------------------------------------

def build(factory_dir, load_sessions, prices, cutoff=None, now=None):
    """The kaizen document. `load_sessions()` is called once, and only when the store has tasks."""
    factory_dir = os.path.expanduser(factory_dir)
    logs = {n: read_log(factory_dir, n) for n in ("events", "retros", "feedback", "tasks")}
    logs["tasks"].sort(key=lambda r: str(r.get("ts")))  # a carried record sits in a newer file than its ts
    stops = _stops(logs["events"])
    by_task = {}
    for store in ("events", "retros", "tasks"):
        for r in logs[store]:
            if _key(r):
                by_task.setdefault(_key(r), {"all": [], "events": [], "retros": [], "tasks": [], "stops": []})[store].append(r)
    versions = {}
    for store in logs.values():
        for r in store:
            if _key(r) in by_task:
                by_task[_key(r)]["all"].append(r)
            v, t = _str(r, "version"), _ts(r)
            if v and t and (v not in versions or t < versions[v]):
                versions[v] = t
    for s in stops:
        by_task[s["_key"]]["stops"].append(s)
    sessions = load_sessions() if by_task else []
    mine = {}
    for s in sessions:
        mine.setdefault((s.repo_path, s.task), []).append(s)
    tasks = [_task(k, recs, mine.get(k, [])) for k, recs in by_task.items()]
    if cutoff is not None:
        tasks = [t for t in tasks if t["started"] and t["started"] >= cutoff]
    tasks.sort(key=lambda t: (t["started"] is not None, t["started"] or transcript.EPOCH, t["slug"], t["repo_path"]))
    repos = []
    for path in sorted({t["repo_path"] for t in tasks}):
        try:
            prs, status = gh_prs(path), "ok"
        except GhError as e:
            prs, status = [], str(e)
        for t in tasks:
            if t["repo_path"] == path:
                _apply_pr(t, _pr_for(prs, t["slug"]))
        repos.append({"repo": _name(path), "repo_path": path, "prs": status})
    kept = {(t["repo_path"], t["slug"]) for t in tasks}
    slowed = [{"ts": _iso(_ts(r)), "repo": _name(r["repo"]), "slug": r["slug"], "text": x}
              for k in sorted(kept) for r in by_task[k]["retros"] for x in (r.get("slowed") if isinstance(r.get("slowed"), list) else [])
              if isinstance(x, str)]
    slowed.sort(key=lambda s: s["ts"] or "", reverse=True)
    for t in tasks:
        t["started"] = _iso(t["started"])
    return {
        "generated": _iso(now or datetime.now(timezone.utc)), "factory_dir": factory_dir,
        "store_found": os.path.isdir(factory_dir), "prices": prices.to_dict(), "repos": repos,
        "versions": [{"version": v, "first_seen": _iso(t)} for v, t in sorted(versions.items(), key=lambda x: (x[1], x[0]))],
        "tasks": tasks, "summary": _summary(tasks),
        "stops": [{k: v for k, v in s.items() if k != "_key"} for s in stops if s["_key"] in kept],
        "slowed": slowed, "rewards": _rewards([t for t in tasks if t["landed"]]),
        "outbox": _outbox(logs["feedback"], cutoff),
        "site_manager": _site_manager(_records(os.path.join(factory_dir, "firings.jsonl")), cutoff),
    }
