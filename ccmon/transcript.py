"""Discovery and tolerant loading of Claude Code transcripts (read-only)."""
from __future__ import annotations

import glob
import json
import os
import re
from datetime import datetime, timezone

TOKEN_CLASSES = ("input", "output", "cache_write", "cache_read")
_USAGE_KEYS = {
    "input": "input_tokens",
    "output": "output_tokens",
    "cache_write": "cache_creation_input_tokens",
    "cache_read": "cache_read_input_tokens",
}
EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


class SessionError(Exception):
    """User-facing lookup error (exit code 2)."""


class Usage:
    __slots__ = TOKEN_CLASSES

    def __init__(self, input=0, output=0, cache_write=0, cache_read=0):
        self.input = input
        self.output = output
        self.cache_write = cache_write
        self.cache_read = cache_read

    @property
    def total(self):
        return self.input + self.output + self.cache_write + self.cache_read

    def add(self, other):
        for k in TOKEN_CLASSES:
            setattr(self, k, getattr(self, k) + getattr(other, k))

    def to_dict(self):
        d = {k: getattr(self, k) for k in TOKEN_CLASSES}
        d["total"] = self.total
        return d


def _int(v):
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        return 0
    return int(v)


def usage_from(raw):
    if not isinstance(raw, dict):
        return Usage()
    return Usage(**{k: _int(raw.get(src)) for k, src in _USAGE_KEYS.items()})


def parse_ts(s):
    if not isinstance(s, str):
        return None
    try:
        d = datetime.fromisoformat(s)
    except ValueError:
        return None
    if d.tzinfo is None:
        d = d.replace(tzinfo=timezone.utc)
    return d


def add_usage(by_model, model, usage):
    by_model.setdefault(model, Usage()).add(usage)


def sum_usage(by_model):
    u = Usage()
    for v in by_model.values():
        u.add(v)
    return u


class Dispatch:
    def __init__(self, tool_use_id, ts, agent_type, description, prompt, requested_model, depth, parent_agent_id):
        self.tool_use_id = tool_use_id
        self.ts = ts
        self.agent_type = agent_type  # None until resolved
        self.description = description or ""
        self.prompt = prompt or ""
        self.requested_model = requested_model
        self.depth = depth
        self.parent_agent_id = parent_agent_id
        self.agent_id = None
        self.status = "no-run"
        self.usage_by_model = {}
        self.start = ts
        self.returned_at = None
        self.duration_ms = None
        self.results = []  # [(ts, text)]
        self.resumes = 0
        self.first_ts = None
        self.last_ts = None
        # filled by metrics
        self.flags = []
        self.repeat_of = None
        self.repeat_score = None
        self.warnings = []
        self.review_loops_dispatch = 0
        self.review_loops_resume = 0

    @property
    def usage(self):
        return sum_usage(self.usage_by_model)

    @property
    def models(self):
        return sorted(self.usage_by_model)

    @property
    def sort_key(self):
        return self.ts or EPOCH


class Session:
    def __init__(self, path):
        self.path = path
        self.id = os.path.splitext(os.path.basename(path))[0]
        self.project_dir = os.path.basename(os.path.dirname(path))
        self.cwd = None
        self.dispatches = []
        self.main_usage_by_model = {}
        self.malformed = 0
        self.reads = []  # [(ts, file_path)] main-session Read calls
        self.sends = []  # [(ts, Dispatch)] SendMessage to a known agent
        self.first_ts = None
        self.last_ts = None
        self.mtime = None
        self.repo_path = None
        self.task = None
        self.worktrees = []
        # filled by metrics
        self.warnings = []
        self.review_loops = 0
        self.review_loop_events = []
        self.recheck_candidates = 0
        self.rechecked = 0

    @property
    def project(self):
        if self.cwd:
            return os.path.basename(self.cwd.rstrip("/")) or self.cwd
        return self.project_dir

    @property
    def repo(self):
        if self.repo_path:
            return os.path.basename(self.repo_path) or self.repo_path
        return self.project_dir


_WT_MARK = "/.claude/worktrees/"
_AGENT_WT = re.compile(r"agent-[0-9a-f]+")


def _locate(path, name=None, origin=None):
    """(repository path, task) for a worktree path, or for a plain cwd when name is None."""
    if _WT_MARK in path:
        repo, rest = path.split(_WT_MARK, 1)
        task = rest.split("/")[0]
        return repo, (None if not task or _AGENT_WT.fullmatch(task) else task)
    if name:  # worktree made by a hook, outside .claude/worktrees
        path = origin if isinstance(origin, str) and origin else path
    return path.rstrip("/") or path, name


def attribute(states, cwd):
    """(repo_path, task, worktrees) from the main transcript's worktree-state records and first cwd.

    states: [(worktreePath, worktreeName, originalCwd)] in file order. The session belongs to the
    last worktree it entered; with none, to its first cwd. Nothing here touches git or the disk.
    """
    entered = []
    for path, name, origin in states:
        if _AGENT_WT.fullmatch(name) or (entered and entered[-1][0] == path):
            continue
        entered.append((path, name, origin))
    located = [_locate(*e) for e in entered]
    if not located and cwd:
        located = [_locate(cwd)]
    repo, task = located[-1] if located else (None, None)
    return repo, task, [t for _, t in located if t]


class _Scan:
    def __init__(self):
        self.usage = {}
        self.dispatches = []
        self.sends = []
        self.reads = []
        self.tool_result_agent = {}
        self.notifications = {}
        self.queued_notifications = {}
        self.results = []
        self.own_handback = False
        self.handback_ts = []
        self.handbacks = {}  # agent id -> [(ts, text)] seen as peer hand-back user lines
        self.first_ts = None
        self.last_ts = None
        self.malformed = 0
        self.cwd = None
        self.worktree_states = []


_NOTE_RE = {
    "tool_use_id": re.compile(r"<tool-use-id>\s*(.*?)\s*</tool-use-id>", re.S),
    "status": re.compile(r"<status>\s*(.*?)\s*</status>", re.S),
    "duration_ms": re.compile(r"<duration_ms>\s*(\d+)\s*</duration_ms>"),
    "result": re.compile(r"<result>(.*?)</result>", re.S),
}


def parse_notification(text):
    m = _NOTE_RE["tool_use_id"].search(text)
    if not m:
        return None
    out = {"tool_use_id": m.group(1)}
    s = _NOTE_RE["status"].search(text)
    out["status"] = s.group(1) if s else None
    d = _NOTE_RE["duration_ms"].search(text)
    out["duration_ms"] = int(d.group(1)) if d else None
    r = _NOTE_RE["result"].search(text)
    out["result"] = r.group(1).strip() if r else None
    return out


def iter_objects(path):
    """Yield parsed JSON values, or None for a line that is not valid JSON."""
    try:
        f = open(path, "r", encoding="utf-8", errors="replace")
    except OSError:
        return
    with f:
        for line in f:
            s = line.strip()
            if not s:
                continue
            try:
                yield json.loads(s)
            except (ValueError, RecursionError):
                yield None


def _scan(path, seen, depth, agent_id):
    sc = _Scan()
    pending = {}  # message id -> [ts, [texts], end_turn]
    order = []
    for obj in iter_objects(path):
        if not isinstance(obj, dict) or not isinstance(obj.get("type"), str):
            sc.malformed += 1
            continue
        typ = obj["type"]
        if typ == "worktree-state":
            ws = obj.get("worktreeSession")
            if isinstance(ws, dict) and all(isinstance(ws.get(k), str) for k in ("worktreePath", "worktreeName")):
                sc.worktree_states.append((ws["worktreePath"], ws["worktreeName"], ws.get("originalCwd")))
            continue
        if typ == "attachment":
            att = obj.get("attachment")
            if isinstance(att, dict) and att.get("type") == "queued_command":
                pr = att.get("prompt")
                if isinstance(pr, str) and pr.lstrip().startswith("<task-notification>"):
                    n = parse_notification(pr)
                    if n and n["tool_use_id"] not in sc.queued_notifications:
                        n["ts"] = parse_ts(obj.get("timestamp"))
                        sc.queued_notifications[n["tool_use_id"]] = n
            continue
        if typ not in ("assistant", "user"):
            continue
        msg = obj.get("message")
        if not isinstance(msg, dict):
            sc.malformed += 1
            continue
        ts = parse_ts(obj.get("timestamp"))
        if ts:
            if sc.first_ts is None or ts < sc.first_ts:
                sc.first_ts = ts
            if sc.last_ts is None or ts > sc.last_ts:
                sc.last_ts = ts
        if sc.cwd is None and isinstance(obj.get("cwd"), str):
            sc.cwd = obj["cwd"]
        content = msg.get("content")
        if typ == "assistant":
            model = msg.get("model")
            if not isinstance(model, str):
                model = "unknown"
            mid = msg.get("id") if isinstance(msg.get("id"), str) else None
            key = mid or next((v for v in (obj.get("requestId"), obj.get("uuid"))
                               if isinstance(v, str) and v), None)
            if model != "<synthetic>":
                if not (isinstance(key, str) and key in seen):
                    if isinstance(key, str):
                        seen.add(key)
                    add_usage(sc.usage, model, usage_from(msg.get("usage")))
            texts = []
            if isinstance(content, list):
                for b in content:
                    if not isinstance(b, dict):
                        continue
                    bt = b.get("type")
                    if bt == "text" and isinstance(b.get("text"), str):
                        texts.append(b["text"])
                    elif bt == "tool_use":
                        _tool_use(sc, b, ts, depth, agent_id)
                        if b.get("name") == "SubagentHandback" and agent_id is not None:
                            hm = b.get("input")
                            hm = hm.get("message") if isinstance(hm, dict) else None
                            if isinstance(hm, str) and hm:
                                sc.results.append((ts, hm))
                                sc.own_handback = True
                                if ts:
                                    sc.handback_ts.append(ts)
            elif isinstance(content, str):
                texts.append(content)
            if agent_id is not None:
                uid = obj.get("uuid")
                rk = mid or (uid if isinstance(uid, str) and uid else None) or id(obj)
                ent = pending.get(rk)
                if ent is None:
                    ent = pending[rk] = [ts, [], False]
                    order.append(rk)
                ent[1].extend(texts)
                if msg.get("stop_reason") == "end_turn":
                    ent[2] = True
                    ent[0] = ts or ent[0]
        else:  # user
            origin = obj.get("origin")
            if isinstance(origin, dict) and origin.get("handback") and isinstance(origin.get("from"), str):
                htext = next((v for v in (origin.get("handbackReportText"), origin.get("body"), content)
                              if isinstance(v, str) and v), "")
                sc.handbacks.setdefault(origin["from"], []).append((ts, htext))
            if isinstance(content, str):
                if content.lstrip().startswith("<task-notification>"):
                    n = parse_notification(content)
                    if n:
                        n["ts"] = ts
                        sc.notifications[n["tool_use_id"]] = n
            elif isinstance(content, list):
                tur = obj.get("toolUseResult")
                aid = tur.get("agentId") if isinstance(tur, dict) else None
                for b in content:
                    if isinstance(b, dict) and b.get("type") == "tool_result" and isinstance(aid, str):
                        tid = b.get("tool_use_id")
                        if isinstance(tid, str):
                            sc.tool_result_agent[aid] = tid
    for rk in order:
        ts, texts, end = pending[rk]
        if end and texts:
            sc.results.append((ts, "\n".join(texts)))
    return sc


def _tool_use(sc, b, ts, depth, agent_id):
    name = b.get("name")
    inp = b.get("input")
    if not isinstance(inp, dict):
        inp = {}
    if name == "Agent":
        tid = b.get("id")
        if not isinstance(tid, str):
            return
        st = inp.get("subagent_type")
        model = inp.get("model")
        sc.dispatches.append(Dispatch(
            tid, ts, st if isinstance(st, str) and st else None,
            inp.get("description") if isinstance(inp.get("description"), str) else "",
            inp.get("prompt") if isinstance(inp.get("prompt"), str) else "",
            model if isinstance(model, str) else None, depth, agent_id))
    elif name == "SendMessage":
        to = inp.get("to")
        if isinstance(to, str):
            sc.sends.append((ts, to))
    elif name == "Read" and agent_id is None:
        fp = inp.get("file_path")
        if isinstance(fp, str):
            sc.reads.append((ts, fp))


def _read_meta(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            d = json.load(f)
    except (OSError, ValueError, RecursionError):
        return {}
    return d if isinstance(d, dict) else {}


def load_session(path):
    s = Session(path)
    try:
        s.mtime = os.path.getmtime(path)
    except OSError:
        pass
    seen = set()
    main = _scan(path, seen, 1, None)
    s.cwd = main.cwd
    s.malformed = main.malformed
    s.main_usage_by_model = main.usage
    s.reads = main.reads
    scans = [main]
    agent_files = []  # (agent_id, meta, scan)
    sub_dir = os.path.splitext(path)[0] + os.sep + "subagents"
    for ap in sorted(glob.glob(os.path.join(glob.escape(sub_dir), "agent-*.jsonl"))):
        aid = os.path.basename(ap)[len("agent-"):-len(".jsonl")]
        meta = _read_meta(ap[:-len(".jsonl")] + ".meta.json")
        sd = meta.get("spawnDepth")
        sd = sd if isinstance(sd, int) and not isinstance(sd, bool) else 1
        sc = _scan(ap, seen, sd + 1, aid)
        s.malformed += sc.malformed
        scans.append(sc)
        agent_files.append((aid, meta, sc))
        if s.cwd is None:
            s.cwd = sc.cwd
    s.repo_path, s.task, s.worktrees = attribute(main.worktree_states, s.cwd)
    for sc in scans:
        for t in (sc.first_ts, sc.last_ts):
            if t:
                if s.first_ts is None or t < s.first_ts:
                    s.first_ts = t
                if s.last_ts is None or t > s.last_ts:
                    s.last_ts = t
    by_tid = {}
    for sc in scans:
        for d in sc.dispatches:
            if d.tool_use_id not in by_tid:
                by_tid[d.tool_use_id] = d
                s.dispatches.append(d)
    notifs = {}
    agent_to_tid = {}
    handbacks = {}
    for sc in scans:
        for k, v in sc.queued_notifications.items():
            notifs.setdefault(k, v)  # first seen wins; user-line notifications override below
        for k, v in sc.handbacks.items():
            handbacks.setdefault(k, []).extend(v)
        agent_to_tid.update(sc.tool_result_agent)
    for sc in scans:
        notifs.update(sc.notifications)
    by_agent = {}
    for aid, meta, sc in agent_files:
        tid = meta.get("toolUseId")
        d = by_tid.get(tid) if isinstance(tid, str) else None
        if d is None or d.agent_id is not None:
            d = by_tid.get(agent_to_tid.get(aid))
            if d is not None and d.agent_id is not None:
                d = None
        if d is None:
            at = meta.get("agentType")
            d = Dispatch(None, sc.first_ts, at if isinstance(at, str) and at else None,
                         meta.get("description") if isinstance(meta.get("description"), str) else "",
                         "", None, (meta.get("spawnDepth") if isinstance(meta.get("spawnDepth"), int) else 1),
                         meta.get("parentAgentId") if isinstance(meta.get("parentAgentId"), str) else None)
            d.status = "orphan"
            s.dispatches.append(d)
        else:
            d.status = "running"
        d.agent_id = aid
        by_agent[aid] = d
        d.usage_by_model = sc.usage
        d.first_ts, d.last_ts = sc.first_ts, sc.last_ts
        d.results = list(sc.results)
        hbs = sorted(handbacks.get(aid, []), key=lambda x: x[0] or EPOCH)
        if hbs and not sc.own_handback:
            d.results.extend(hbs)
        d.results.sort(key=lambda r: r[0] or EPOCH)
        n = notifs.get(d.tool_use_id) if d.tool_use_id else None
        if d.status != "orphan":
            hb_ts = [h[0] for h in hbs if h[0]] + sc.handback_ts
            if n:
                d.duration_ms = n["duration_ms"]
                if not d.results and n["result"]:
                    d.results = [(n["ts"], n["result"])]
            if n or hbs or sc.own_handback:
                times = list(hb_ts) + ([n["ts"]] if n and n["ts"] else [])
                d.returned_at = min(times) if times else None
                if hbs or sc.own_handback:
                    d.status = "completed"
                else:
                    d.status = n["status"] or "completed"
                if d.duration_ms is None and d.returned_at and d.ts:
                    d.duration_ms = max(0, int((d.returned_at - d.ts).total_seconds() * 1000))
        if d.duration_ms is None and sc.first_ts and sc.last_ts:
            d.duration_ms = int((sc.last_ts - sc.first_ts).total_seconds() * 1000)
        if d.returned_at is None:
            d.returned_at = sc.last_ts
    for d in s.dispatches:
        if d.agent_type is None:
            d.agent_type = "general-purpose"
        if d.status == "no-run":
            n = notifs.get(d.tool_use_id)
            if n:
                d.returned_at, d.duration_ms = n["ts"], n["duration_ms"]
    for sc in scans:
        for ts, to in sc.sends:
            d = by_agent.get(to)
            if d is not None:
                d.resumes += 1
                s.sends.append((ts, d))
    s.dispatches.sort(key=lambda d: d.sort_key)
    s.sends.sort(key=lambda x: x[0] or EPOCH)
    return s


def discover_sessions(projects_dir):
    projects_dir = os.path.expanduser(projects_dir)
    if not os.path.isdir(projects_dir):
        raise SessionError("projects dir not found: %s" % projects_dir)
    return sorted(glob.glob(os.path.join(glob.escape(projects_dir), "*", "*.jsonl")))


def find_session(projects_dir, prefix):
    matches = [p for p in discover_sessions(projects_dir)
               if os.path.basename(p).startswith(prefix)]
    if not matches:
        raise SessionError("no session matches id %r" % prefix)
    if len(matches) > 1:
        ids = ", ".join(os.path.basename(p)[:-len(".jsonl")][:12] for p in matches[:6])
        raise SessionError("ambiguous session id %r matches %d sessions (%s)" % (prefix, len(matches), ids))
    return matches[0]
