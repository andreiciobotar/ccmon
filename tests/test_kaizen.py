import contextlib
import io
import json
import os
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from unittest import mock

from tests.support import AGENTS, PRICES_PATH, ROOT
from isshoni import cli, kaizen, metrics, prices as price_file, transcript

GROUPING = os.path.join(ROOT, "tests", "fixtures", "grouping")
PRICES_TOML = """currency = "USD"
as_of = "2026-01-01"
source = "test"
""" + "".join("""
[models.%s]
input = %s
output = 1
cache_write_5m = 1
cache_write_1h = 1
cache_read = 1
""" % m for m in (("claude-fable-5-2", 10), ("claude-opus-5-5", 5), ("claude-haiku-5-5", 1)))  # sonnet: no price
DAY = "2026-11-10"
ALPHA, BETA = "/r/alpha", "/r/beta"


def write(path, lines):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        for ln in lines:
            f.write(ln if isinstance(ln, str) else json.dumps(ln))
            f.write("\n")


def ev(ts, event, repo=ALPHA, slug="a1", **kw):
    return {"ts": ts, "repo": repo, "slug": slug, "event": event, "version": "0.2.0", **kw}


def assistant(ts, mid, model="claude-fable-5-2", tokens=0, content=None, cwd="/x"):
    m = {"id": mid, "model": model, "role": "assistant", "content": content or [{"type": "text", "text": "x"}]}
    if tokens:
        m["usage"] = {"input_tokens": tokens}
    return {"type": "assistant", "uuid": "u-" + mid, "timestamp": ts, "requestId": "r-" + mid, "message": m, "cwd": cwd}


def session(projects, repo, slug, start, lead_tokens, dispatches=()):
    """A transcript in worktree `slug`; dispatches are (agent_type, model, tokens, start, end[, no_file]) on DAY."""
    wt = "%s/.claude/worktrees/%s" % (repo, slug)
    sid = "s-" + slug
    path = os.path.join(projects, "p-" + slug, sid + ".jsonl")
    lines = [{"type": "worktree-state", "sessionId": sid, "worktreeSession": {
        "originalCwd": wt, "worktreePath": wt, "worktreeName": slug, "worktreeBranch": "worktree-" + slug}},
        assistant(start, "lead-" + slug, tokens=lead_tokens, cwd=wt)]
    for i, (agent, model, tokens, t0, t1, *no_file) in enumerate(dispatches):
        tid, aid = "toolu-%s-%d" % (slug, i), "a%d" % i
        lines.append(assistant("%sT%s:00.000Z" % (DAY, t0), "d-%s-%d" % (slug, i), cwd=wt, content=[
            {"type": "tool_use", "id": tid, "name": "Agent",
             "input": {"description": "d%d" % i, "prompt": "p%d" % i, "subagent_type": agent}}]))
        if not no_file:
            base = os.path.join(projects, "p-" + slug, sid, "subagents", "agent-" + aid)
            write(base + ".jsonl", [assistant("%sT%s:01.000Z" % (DAY, t0), "s-%s-%d" % (slug, i), model, tokens, cwd=wt),
                                    assistant("%sT%s:00.000Z" % (DAY, t1), "e-%s-%d" % (slug, i), model, 0, cwd=wt)])
            write(base + ".meta.json", [{"agentType": agent, "toolUseId": tid, "spawnDepth": 1}])
    write(path, lines)


def pr(number, branch, opened, merged=None, state="MERGED"):
    return {"number": number, "url": "https://gh/pull/%d" % number, "state": state, "headRefName": branch,
            "createdAt": opened, "mergedAt": merged}


class Store(unittest.TestCase):
    """A factory store and transcripts in a temp dir: `self.factory`, `self.projects`, `self.prices`."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.factory = os.path.join(tmp.name, "factory")
        self.projects = os.path.join(tmp.name, "projects")
        os.makedirs(self.factory)
        os.makedirs(self.projects)
        path = os.path.join(tmp.name, "prices.toml")
        write(path, [PRICES_TOML])
        self.prices = price_file.load(path)

    def log(self, name, lines):
        write(os.path.join(self.factory, name), lines)

    def build(self, gh, cutoff=None, sessions=None):
        loaded = []

        def load():
            for p in transcript.discover_sessions(self.projects):
                s = transcript.load_session(p, self.prices)
                metrics.analyze(s, AGENTS, 0.5)
                loaded.append(s)
            return loaded

        with mock.patch.object(kaizen, "gh_prs", side_effect=gh):
            return kaizen.build(self.factory, sessions or load, self.prices, cutoff,
                                now=datetime(2026, 11, 14, 1, 2, 3, tzinfo=timezone.utc))


def gh_alpha_only(path):
    if path == ALPHA:
        return [pr(3, "worktree-a1", "2026-11-10T09:00:00Z", None, "CLOSED"),  # an older attempt: the newest wins
                pr(5, "worktree-a1", "%sT10:50:00Z" % DAY, "%sT11:00:00Z" % DAY),
                pr(6, "worktree-other", "%sT10:00:00Z" % DAY, "%sT10:10:00Z" % DAY), "junk"]
    raise kaizen.GhError("not a GitHub repository")


class FixtureStore(Store):
    """Four tasks: a1 (everything), a0 (legacy files, no TaskStarted), b1 (declared state only), c1 (session only)."""

    def setUp(self):
        super().setUp()
        self.log("events.jsonl", [
            "not json", "[1, 2]", "{}", '{"ts": "2026-11-09T08:00:00Z", "repo": "/r/alpha"}',
            {"ts": "2026-11-09T08:00:00Z", "slug": "zz", "event": "SessionStart"},
            {"ts": "2026-11-09T08:00:00Z", "repo": ALPHA, "slug": "a0", "event": "SessionStart", "version": "0.1.0"}])
        self.log("events-2026-11.jsonl", [
            ev("%sT10:00:00Z" % DAY, "TaskStarted"),
            ev("%sT10:12:00Z" % DAY, "PostToolUse", tool_name="SendMessage", kind="QUESTION", session="s1"),
            ev("%sT10:13:00Z" % DAY, "UserPromptSubmit", session="s1", **{"from": "subagent"}),
            ev("%sT10:14:00Z" % DAY, "UserPromptSubmit", session="s1", **{"from": "human"}),
            ev("%sT10:15:00Z" % DAY, "Notification", notification_type="idle_prompt", session="s1"),
            ev("%sT10:16:00Z" % DAY, "PostToolUse", tool_name="SendMessage", kind="BLOCKED", session="s2"),
            ev("%sT10:17:00Z" % DAY, "PostToolUse", tool_name="SendMessage", kind="DONE", session="s2"),
            ev("%sT10:18:00Z" % DAY, "PermissionDenied", tool_name="Bash", reason="sandbox"),
            ev("%sT10:19:00Z" % DAY, "PermissionDenied", tool_name="Edit"),
            {"ts": "%sT10:20:00Z" % DAY, "repo": ALPHA, "event": "PermissionDenied", "tool_name": "Bash"},
            ev("%sT12:00:00Z" % DAY, "SessionStart", slug="c1")])
        self.log("retros.jsonl", [{"ts": "2026-11-09T09:00:00Z", "repo": ALPHA, "slug": "a0", "version": "0.2.0",
                                   "defects": [{"caught_by": "lead"}], "slowed": ["a0 slow"]}])
        self.log("retros-2026-11.jsonl", [
            ev("%sT12:00:00Z" % DAY, "", defects=[{"caught_by": "human"}], slowed=["old"]),
            ev("%sT13:00:00Z" % DAY, "", defects=[{"caught_by": "reviewer"}, {"caught_by": "reviewer"}, {"caught_by": "tests"},
                                                   {"caught_by": "weird"}, "str", {}], slowed=["slow two", 7, "slow three"])])
        self.log("tasks.jsonl", [
            {"ts": "%sT10:55:00Z" % DAY, "repo": ALPHA, "slug": "a1", "state": "handed-over", "deliverable": "x"}])
        self.log("tasks-2026-11.jsonl", [
            {"ts": "%sT10:01:00Z" % DAY, "repo": ALPHA, "slug": "a1", "state": "working"},
            {"ts": "%sT10:56:00Z" % DAY, "repo": ALPHA, "slug": "a1"},
            {"ts": "%sT09:00:00Z" % DAY, "repo": BETA, "slug": "b1", "state": "acknowledged"}])
        fb = lambda i, ts, text, **kw: {"ts": "2026-11-11T%s:00Z" % ts, "repo": ALPHA, "slug": "a1", "role": "reviewer",  # noqa: E731
                                        "id": i, "text": text, "status": "open", **kw}
        self.log("feedback-2026-11.jsonl", [
            fb("e1", "10:00", "The brief did not say which file"), fb("e1", "10:05", "same id again: permission"),
            fb("e2", "10:10", "Permission denied on Bash in the sandbox"),
            fb("e3", "10:20", "DECISION: used a tool"), fb("e4", "10:30", "Nothing special"),
            fb("e5", "10:40", "Used a bash script", version="0.3.0"),
            {"ts": "2026-11-11T10:50:00Z", "id": "e1", "status": "accepted"},
            {"ts": "2026-11-11T11:00:00Z", "id": "e1", "status": "done", "pr": "https://x/pull/1"},
            {"ts": "2026-11-11T10:55:00Z", "id": "e1", "status": "rejected"},
            {"ts": "2026-11-11T11:05:00Z", "id": "e3", "status": "accepted"},
            {"ts": "2026-11-11T11:05:00Z", "id": "zz", "status": "done"}, {"text": "no id"}, {"id": "e9", "text": 5}])
        session(self.projects, ALPHA, "a1", "%sT10:00:05.000Z" % DAY, 1000000, [
            ("general-purpose", "claude-haiku-5-5", 1000000, "10:01", "10:02"),
            ("explorer", "claude-haiku-5-5", 1000000, "10:02", "10:03"),
            ("Explore", "claude-haiku-5-5", 1000000, "10:03", "10:04"),
            ("factory:coder", "claude-haiku-5-5", 1000000, "10:05", "10:10"),
            ("factory:reviewer", "claude-opus-5-5", 1000000, "10:20", "10:25"),
            ("coder", "claude-haiku-5-5", 2000000, "10:30", "10:35"),
            ("coder", "claude-sonnet-5-5", 1000000, "10:40", "10:41"),
            ("reviewer", "claude-opus-5-5", 0, "10:45", "10:45", "no file")])
        session(self.projects, ALPHA, "c1", "%sT12:30:00.000Z" % DAY, 500000)
        self.doc = self.build(gh_alpha_only)
        self.by_slug = {t["slug"]: t for t in self.doc["tasks"]}


class TaskTests(FixtureStore):
    def test_tasks_are_listed_by_start_with_nulls_first(self):
        self.assertEqual([t["slug"] for t in self.doc["tasks"]], ["a0", "b1", "a1", "c1"])

    def test_identity_version_started_state(self):
        a1, a0, b1, c1 = (self.by_slug[s] for s in ("a1", "a0", "b1", "c1"))
        self.assertEqual((a1["repo"], a1["repo_path"], a1["version"], a1["started"], a1["state"]),
                         ("alpha", ALPHA, "0.2.0", "%sT10:00:00Z" % DAY, "handed-over"))
        self.assertEqual((a0["version"], a0["started"], a0["state"]), ("0.1.0", None, None))  # earliest record's version
        self.assertEqual((b1["repo"], b1["version"], b1["started"], b1["state"]), ("beta", None, None, "acknowledged"))
        self.assertEqual((c1["version"], c1["started"]), ("0.2.0", "%sT12:30:00Z" % DAY))  # from its session

    def test_pull_request_and_times(self):
        a1 = self.by_slug["a1"]
        self.assertEqual(a1["pr"], {"number": 5, "url": "https://gh/pull/5", "state": "MERGED",
                                    "opened": "%sT10:50:00Z" % DAY, "merged": "%sT11:00:00Z" % DAY})
        self.assertEqual((a1["landed"], a1["brief_to_pr_s"], a1["pr_to_merge_s"]), (True, 3000, 600))
        for s in ("a0", "b1", "c1"):
            t = self.by_slug[s]
            self.assertEqual((t["pr"], t["landed"], t["brief_to_pr_s"], t["pr_to_merge_s"]), (None, False, None, None))

    def test_cost_by_role_rework_unpriced(self):
        self.assertEqual(self.by_slug["a1"]["cost"], {
            "total": 21.0, "unpriced_tokens": 1000000,
            "by_role": {"lead": 10.0, "coder": 3.0, "reviewer": 5.0, "explorer": 2.0, "other": 1.0},
            "rework": 2.0})
        self.assertEqual(self.by_slug["c1"]["cost"]["total"], 5.0)
        self.assertEqual(self.by_slug["c1"]["cost"]["rework"], 0.0)
        self.assertIsNone(self.by_slug["a0"]["cost"])

    def test_review_rounds_and_first_pass(self):
        a1, c1, a0 = (self.by_slug[s] for s in ("a1", "c1", "a0"))
        self.assertEqual((a1["review_rounds"], a1["first_pass"]), (1, True))  # the no-run reviewer is not a round
        self.assertEqual((c1["review_rounds"], c1["first_pass"]), (0, False))  # skipping the review is not first-pass
        self.assertEqual((a0["review_rounds"], a0["first_pass"]), (None, None))

    def test_stops(self):
        self.assertEqual([(t["slug"], t["stops"], t["stopped_s"]) for t in self.doc["tasks"]],
                         [("a0", 0, 0), ("b1", 0, 0), ("a1", 4, 120), ("c1", 0, 0)])
        self.assertEqual(self.doc["stops"], [
            {"ts": "%sT10:12:00Z" % DAY, "repo": "alpha", "slug": "a1", "kind": "QUESTION", "waited_s": 120, "why": None},
            {"ts": "%sT10:16:00Z" % DAY, "repo": "alpha", "slug": "a1", "kind": "BLOCKED", "waited_s": None, "why": None},
            {"ts": "%sT10:18:00Z" % DAY, "repo": "alpha", "slug": "a1", "kind": "DENIED", "waited_s": None, "why": "Bash: sandbox"},
            {"ts": "%sT10:19:00Z" % DAY, "repo": "alpha", "slug": "a1", "kind": "DENIED", "waited_s": None, "why": "Edit"}])

    def test_defects_come_from_the_newest_retro(self):
        zero = dict.fromkeys(("tests", "reviewer", "lead", "ci", "human", "other"), 0)
        self.assertEqual(self.by_slug["a1"]["defects"], {**zero, "tests": 1, "reviewer": 2, "other": 2})
        self.assertEqual(self.by_slug["a0"]["defects"], {**zero, "lead": 1})
        self.assertIsNone(self.by_slug["c1"]["defects"])

    def test_slowed_newest_retro_first(self):
        self.assertEqual([(s["ts"], s["slug"], s["text"]) for s in self.doc["slowed"]], [
            ("%sT13:00:00Z" % DAY, "a1", "slow two"), ("%sT13:00:00Z" % DAY, "a1", "slow three"),
            ("%sT12:00:00Z" % DAY, "a1", "old"), ("2026-11-09T09:00:00Z", "a0", "a0 slow")])

    def test_versions_by_earliest_record_in_any_store(self):
        self.assertEqual(self.doc["versions"], [
            {"version": "0.1.0", "first_seen": "2026-11-09T08:00:00Z"},
            {"version": "0.2.0", "first_seen": "2026-11-09T09:00:00Z"},
            {"version": "0.3.0", "first_seen": "2026-11-11T10:40:00Z"}])


class DocumentTests(FixtureStore):
    def test_header_repos_and_gh_failure(self):
        d = self.doc
        self.assertEqual((d["generated"], d["factory_dir"], d["store_found"]), ("2026-11-14T01:02:03Z", self.factory, True))
        self.assertEqual(d["prices"], self.prices.to_dict())
        self.assertEqual(d["repos"], [{"repo": "alpha", "repo_path": ALPHA, "prs": "ok"},
                                      {"repo": "beta", "repo_path": BETA, "prs": "not a GitHub repository"}])

    def test_summary(self):
        self.assertEqual(self.doc["summary"], {
            "tasks": 4, "landed": 1, "in_flight": 2,  # b1 is acknowledged
            "cost": {"median": 21.0, "n": 1}, "brief_to_pr_s": {"median": 3000.0, "n": 1},
            "pr_to_merge_s": {"median": 600.0, "n": 1}, "review_rounds": {"median": 1.0, "n": 1},
            "first_pass": {"yes": 1, "of": 1}, "rework_share": 0.095238,
            "by_role": {"lead": 10.0, "coder": 3.0, "reviewer": 5.0, "explorer": 2.0, "other": 1.0},
            "time": {"building_s": 2880, "stopped_s": 120, "merge_wait_s": 600},
            "defects": {"tests": 1, "reviewer": 2, "lead": 1, "ci": 0, "human": 0, "other": 2},
            "trend_min": 10, "trend": None})
        self.assertEqual(self.doc["rewards"], [])

    def test_outbox_folds_entries_and_topics(self):
        o = self.doc["outbox"]
        self.assertEqual(o["issues"], {"open": 3, "accepted": 0, "rejected": 0, "done": 1})
        self.assertEqual(o["decisions"], {"open": 0, "accepted": 1, "rejected": 0, "done": 0})
        self.assertEqual(o["topics"], [{"topic": "permissions", "open": 1}, {"topic": "tools", "open": 1}, {"topic": "other", "open": 1}])
        self.assertEqual([(e["id"], e["kind"], e["status"], e["topic"], e["pr"]) for e in o["entries"]], [
            ("e5", "issue", "open", "tools", None), ("e4", "issue", "open", "other", None),
            ("e3", "decision", "accepted", None, None), ("e2", "issue", "open", "permissions", None),
            ("e1", "issue", "done", "brief", "https://x/pull/1")])  # the latest ts wins, not the last line
        self.assertEqual(o["entries"][-1], {"id": "e1", "ts": "2026-11-11T10:00:00Z", "repo": "alpha", "slug": "a1",
                                           "role": "reviewer", "kind": "issue", "status": "done", "topic": "brief",
                                           "text": "The brief did not say which file", "pr": "https://x/pull/1"})

    def test_topics_first_match_in_table_order(self):
        for text, topic in (("sandbox", "permissions"), ("an ambiguous Spec", "brief"), ("the lead.md rule", "rules"),
                            ("run the git command", "tools"), ("verdict", "review"), ("wasted minutes", "time"),
                            ("brief and permission", "permissions"), ("nothing", "other")):
            self.assertEqual(kaizen._topic(text), topic, text)

    def test_since_keeps_tasks_started_at_or_after(self):
        d = self.build(gh_alpha_only, cutoff=datetime(2026, 11, 10, 12, 30, 0, tzinfo=timezone.utc))
        self.assertEqual([t["slug"] for t in d["tasks"]], ["c1"])  # at the cutoff is kept; null starts are not
        self.assertEqual((d["stops"], d["slowed"], d["repos"]), ([], [], [{"repo": "alpha", "repo_path": ALPHA, "prs": "ok"}]))
        self.assertEqual(d["summary"]["tasks"], 1)
        d = self.build(gh_alpha_only, cutoff=datetime(2026, 11, 10, 12, 30, 1, tzinfo=timezone.utc))
        self.assertEqual((d["tasks"], d["repos"]), ([], []))
        self.assertEqual(len(d["outbox"]["entries"]), 5)
        d = self.build(gh_alpha_only, cutoff=datetime(2026, 11, 11, 10, 30, 0, tzinfo=timezone.utc))
        self.assertEqual([e["id"] for e in d["outbox"]["entries"]], ["e5", "e4"])  # entries by their own time

    def test_gh_failing_everywhere(self):
        def fail(path):
            raise kaizen.GhError("gh unavailable")
        d = self.build(fail)
        self.assertEqual([r["prs"] for r in d["repos"]], ["gh unavailable"] * 2)
        self.assertEqual([(t["pr"], t["landed"]) for t in d["tasks"]], [(None, False)] * 4)
        self.assertEqual((d["summary"]["landed"], d["summary"]["in_flight"], d["summary"]["cost"]), (0, 3, {"median": None, "n": 0}))


def shapes(d):
    """The key sets of every kind of object in a kaizen document, by name."""
    out = {}

    def add(name, objs):
        for o in objs:
            if isinstance(o, dict):
                out.setdefault(name, set()).add(frozenset(o))

    s, o, tasks = d["summary"], d["outbox"], d["tasks"]
    add("doc", [d]), add("repo", d["repos"]), add("version", d["versions"]), add("task", tasks)
    add("pr", [t["pr"] for t in tasks]), add("cost", [t["cost"] for t in tasks])
    add("by_role", [t["cost"]["by_role"] for t in tasks if t["cost"]] + [s["by_role"]])
    add("defects", [t["defects"] for t in tasks] + [s["defects"]])
    add("summary", [s]), add("time", [s["time"]]), add("first_pass", [s["first_pass"]]), add("trend", [s["trend"]])
    add("median", [s[m] for m in ("cost", "brief_to_pr_s", "pr_to_merge_s", "review_rounds")])
    add("stop", d["stops"]), add("slowed", d["slowed"]), add("reward", d["rewards"])
    add("outbox", [o]), add("counts", [o["issues"], o["decisions"]]), add("topic", o["topics"]), add("entry", o["entries"])
    add("site_manager", [d["site_manager"]])
    return out


class DashboardFixtureTests(FixtureStore):
    """The dashboard's made-up fixture has the shape the command emits."""

    def test_fixture_matches_the_command(self):
        with open(os.path.join(ROOT, "internal", "tui", "testdata", "kaizen-fixture.json")) as f:
            fixture = shapes(json.load(f))
        ours = shapes(self.doc)
        self.assertEqual({n for n, v in fixture.items() if len(v) != 1}, set())
        self.assertLessEqual({"doc", "repo", "version", "task", "pr", "cost", "by_role", "defects", "summary", "time",
                              "first_pass", "median", "stop", "slowed", "outbox", "counts", "topic", "entry",
                              "site_manager"}, set(ours))
        for name in ours:
            self.assertEqual(fixture[name], ours[name], name)
        self.assertEqual(fixture["trend"], {frozenset(kaizen._trend([task("t", "2026-11-01T00:00:00Z")] * 10))})
        self.assertEqual(fixture["reward"], {frozenset(kaizen._rewards(days(9, 8, 7, 6, 5))[0])})


class StopAndTimeTests(Store):
    def test_overtime_answered_in_its_own_session_offsets_and_a_pr_older_than_the_start(self):
        self.log("events-2026-11.jsonl", [
            ev("2026-11-10T11:00:00+01:00", "TaskStarted"),
            ev("%sT10:10:00Z" % DAY, "PostToolUse", tool_name="SendMessage", kind="OVERTIME", session="s1"),
            ev("%sT10:11:00Z" % DAY, "UserPromptSubmit", session="other", **{"from": "session"}),
            ev("%sT10:13:00Z" % DAY, "UserPromptSubmit", session="s1", **{"from": "session"})])
        d = self.build(lambda p: [pr(1, "worktree-a1", "%sT09:00:00Z" % DAY, "%sT09:30:00Z" % DAY)])
        t = d["tasks"][0]
        self.assertEqual(t["started"], "%sT10:00:00Z" % DAY)
        self.assertEqual(d["stops"], [{"ts": "%sT10:10:00Z" % DAY, "repo": "alpha", "slug": "a1", "kind": "OVERTIME",
                                       "waited_s": 180, "why": None}])
        self.assertEqual((t["landed"], t["brief_to_pr_s"], t["pr_to_merge_s"]), (True, None, 1800))

    def test_why_is_the_summary_the_hook_stored(self):
        stop = lambda minute, kind, **kw: ev("%sT10:%02d:00Z" % (DAY, minute), "PostToolUse", tool_name="SendMessage",  # noqa: E731
                                             kind=kind, session="s1", **kw)
        self.log("events-2026-11.jsonl", [
            stop(1, "QUESTION", summary="which of the two schemas?"), stop(2, "BLOCKED", summary="push denied"),
            stop(3, "OVERTIME", summary="review is on its third round"),
            stop(4, "QUESTION"), stop(5, "QUESTION", summary=""), stop(6, "QUESTION", summary=7),  # older or odd records
            stop(7, "DONE", summary="not a stop")])
        self.assertEqual([(s["kind"], s["why"]) for s in self.build(lambda p: [])["stops"]], [
            ("QUESTION", "which of the two schemas?"), ("BLOCKED", "push denied"), ("OVERTIME", "review is on its third round"),
            ("QUESTION", None), ("QUESTION", None), ("QUESTION", None)])


def fired(fid, ts, repo=ALPHA):
    return {"ts": ts, "repo": repo, "id": fid, "criterion": "quiet", "escalate": "judge", "slug": "a1", "subject": "a1", "detail": "d"}


def then(fid, hhmm, repo=ALPHA, **kw):
    """A later record of a firing on DAY: its decision, outcome or clearing."""
    return {"ts": "%sT%s:00Z" % (DAY, hhmm), "repo": repo, "id": fid, **kw}


class SiteManagerTests(Store):
    EMPTY = {"escalated": 0, "acted": 0, "ignored": 0, "no_outcome": 0, "suppressed": 0, "missed": 0,
             "missed_suppressed": 0, "missed_never_fired": 0, "precision": None, "recall": None}

    def record(self, cutoff=None):
        return self.build(lambda p: [], cutoff=cutoff)["site_manager"]

    def test_no_log_and_an_empty_log(self):
        self.assertEqual(self.record(), self.EMPTY)
        self.log("firings.jsonl", [])
        self.assertEqual(self.record(), self.EMPTY)

    def test_fired_without_a_decision_or_an_outcome_gives_no_ratio(self):
        self.log("firings-2026-11.jsonl", [fired("f1", "2026-11-10T10:00:00Z"), fired("f2", "2026-11-10T10:01:00Z"),
                                           then("f2", "10:02", decision="escalated", by="watch")])
        self.assertEqual(self.record(), {**self.EMPTY, "escalated": 1, "no_outcome": 1})

    def test_counts_and_ratios_over_every_repository_and_month_file(self):
        self.log("firings.jsonl", [  # the legacy file and the month files are one log
            "not json", {"ts": "2026-11-10T09:00:00Z", "repo": ALPHA, "id": 7, "criterion": "quiet"},
            then("zz", "09:01", outcome="acted"),
            *(fired("f%d" % i, "2026-11-10T10:%02d:00Z" % i) for i in range(1, 5)),
            fired("f1", "2026-11-10T10:08:00Z", BETA),  # the same id in another repository is another firing
            then("f1", "11:01", decision="escalated"), then("f1", "11:02", decision="suppressed"),  # the first decision stands
            then("f2", "11:03", decision="escalated"), then("f3", "11:04", decision="escalated")])
        self.log("firings-2026-11.jsonl", [
            then("f1", "12:09", outcome="acted"),
            then("f4", "11:05", decision="escalated"), then("f5", "11:06", decision="escalated"),
            then("f6", "11:07", decision="suppressed"), then("f7", "11:08", decision="suppressed"),
            then("f1", "11:09", BETA, decision="escalated"), then("f1", "12:00", BETA, outcome="ignored"),
            then("f2", "12:01", outcome="ignored"), then("f2", "12:02", outcome="acted"),  # the last outcome stands
            then("f3", "12:03", outcome="acted"),
            then("f6", "12:05", outcome="acted"),  # an outcome on a suppressed firing counts nowhere
            then("f1", "12:10", cleared=True),
            {"ts": "2026-11-10T12:11:00Z", "id": "f4", "outcome": "acted"},  # no repo: the factory counts it nowhere
            {"ts": "2026-11-10T12:12:00Z", "miss": "no repo"},
            {"ts": "2026-11-10T13:00:00Z", "repo": ALPHA, "miss": "a lead was stuck", "slug": "a1", "firing": "f6"},
            {"ts": "2026-11-10T13:01:00Z", "repo": BETA, "miss": "nothing fired", "slug": None, "firing": None},
            # carried into the new month after its first record: older than the lines above, so the fold is by ts
            *(fired("f%d" % i, "2026-11-10T10:%02d:00Z" % i) for i in range(5, 8))])
        self.assertEqual(self.record(), {
            "escalated": 6, "acted": 3, "ignored": 1, "no_outcome": 2, "suppressed": 2, "missed": 2,
            "missed_suppressed": 1, "missed_never_fired": 1, "precision": 0.75, "recall": 0.6})

    def test_since_keeps_firings_and_misses_by_their_own_time(self):
        self.log("firings-2026-11.jsonl", [
            fired("old", "2026-11-10T10:00:00Z"), fired("new", "2026-11-10T12:00:00Z"),
            then("old", "12:01", decision="escalated"), then("old", "12:02", outcome="ignored"),
            then("new", "12:03", decision="escalated"), then("new", "12:04", outcome="acted"),
            {"ts": "2026-11-10T11:59:59Z", "repo": ALPHA, "miss": "before"},
            {"ts": "2026-11-10T12:00:00Z", "repo": ALPHA, "miss": "at the cutoff"}, {"repo": ALPHA, "miss": "no time"}])
        self.assertEqual(self.record(datetime(2026, 11, 10, 12, 0, 0, tzinfo=timezone.utc)), {
            **self.EMPTY, "escalated": 1, "acted": 1, "missed": 1, "missed_never_fired": 1, "precision": 1.0, "recall": 0.5})
        self.assertEqual(self.record(), {**self.EMPTY, "escalated": 2, "acted": 1, "ignored": 1, "missed": 3,
                                         "missed_never_fired": 3, "precision": 0.5, "recall": 0.25})

    def test_agrees_with_the_factory_on_the_same_log(self):
        """The one place the two folds are compared: `factory firings --json` run on a store holding one repository."""
        home = os.path.dirname(self.factory)
        repo = os.path.realpath(os.path.join(home, "alpha"))
        subprocess.run(["git", "init", "-q", repo], check=True)
        self.factory = os.path.join(home, ".claude", "factory")
        self.log("firings.jsonl", [
            fired("f1", "2026-11-10T10:00:00Z", repo), fired("f2", "2026-11-10T10:01:00Z", repo),
            fired("f3", "2026-11-10T10:02:00Z", repo), fired("f4", "2026-11-10T10:03:00Z", repo),
            {"ts": "2026-11-10T11:00:00Z", "repo": repo, "id": "f1", "decision": "escalated"},
            {"ts": "2026-11-10T11:01:00Z", "repo": repo, "id": "f2", "decision": "suppressed", "reason": "r"},
            {"ts": "2026-11-10T11:02:00Z", "repo": repo, "id": "f3", "decision": "escalated"},
            {"ts": "2026-11-10T11:03:00Z", "repo": repo, "id": "f4", "decision": "escalated"},
            {"ts": "2026-11-10T12:00:00Z", "repo": repo, "id": "f1", "outcome": "acted"},
            {"ts": "2026-11-10T12:01:00Z", "repo": repo, "id": "f3", "outcome": "ignored"},
            {"ts": "2026-11-10T13:00:00Z", "repo": repo, "miss": "m", "slug": None, "firing": "f2"},
            {"ts": "2026-11-10T13:01:00Z", "repo": repo, "miss": "n", "slug": None, "firing": None}])
        p = subprocess.run([os.path.join(ROOT, "factory", "bin", "factory"), "firings", "--json"], cwd=repo,
                           env={**os.environ, "HOME": home}, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        theirs = json.loads(p.stdout)["summary"]
        self.assertEqual((theirs["escalated"], theirs["missed"]), (3, 2))
        theirs.update({k: round(theirs[k], 6) for k in ("precision", "recall")})
        self.assertEqual(self.record(), theirs)


class SmallTests(Store):
    def test_missing_directory_and_empty_directory(self):
        calls = []
        for path, found in ((os.path.join(self.factory, "nope"), False), (self.factory, True)):
            self.factory = path
            d = self.build(lambda p: calls.append(p), sessions=lambda: calls.append("loaded"))
            self.assertEqual((d["store_found"], d["tasks"], d["repos"], d["versions"], d["stops"], d["slowed"], d["rewards"]),
                             (found, [], [], [], [], [], []))
            self.assertEqual(d["outbox"]["entries"], [])
            self.assertEqual((d["summary"]["tasks"], d["summary"]["trend"]), (0, None))
        self.assertEqual(calls, [])  # no tasks: no transcripts loaded, no gh

    def test_legacy_file_first_then_months_in_name_order(self):
        for name, n in (("events-2026-11.jsonl", 3), ("events.jsonl", 1), ("events-2026-10.jsonl", 2), ("events-2026-1.jsonl", 9),
                        ("events-old.jsonl", 9)):
            self.log(name, ['{"n": %d}' % n, "garbage", '{"n": %d}' % (n * 10)])
        self.assertEqual([r["n"] for r in kaizen.read_log(self.factory, "events")], [1, 10, 2, 20, 3, 30])

    def test_field_poor_records_still_make_a_task(self):
        self.log("events.jsonl", [{"repo": ALPHA, "slug": "bare"}, {"repo": ALPHA, "slug": "bare", "ts": 5, "version": 3}])
        d = self.build(lambda p: [])
        self.assertEqual(len(d["tasks"]), 1)
        t = d["tasks"][0]
        self.assertEqual((t["slug"], t["version"], t["started"], t["state"], t["cost"], t["defects"], t["stops"]),
                         ("bare", None, None, None, None, None, 0))
        self.assertEqual(d["versions"], [])

    def _landed(self, n):
        """n landed tasks without transcripts: task i has brief_to_pr 60*(i+1) s and pr_to_merge 10*(i+1) s."""
        prs = []
        for i in range(n):
            opened = datetime(2026, 11, 10, i, i + 1, 0, tzinfo=timezone.utc)
            merged = opened + timedelta(seconds=10 * (i + 1))
            prs.append(pr(i, "worktree-t%02d" % i, opened.strftime("%Y-%m-%dT%H:%M:%SZ"), merged.strftime("%Y-%m-%dT%H:%M:%SZ")))
        self.log("events.jsonl", [{"ts": "2026-11-10T%02d:00:00Z" % i, "repo": ALPHA, "slug": "t%02d" % i, "event": "TaskStarted"}
                                  for i in range(n)])
        return self.build(lambda p: prs)

    def test_trend_needs_ten_landed_tasks(self):
        d = self._landed(9)
        self.assertEqual((d["summary"]["landed"], d["summary"]["trend_min"], d["summary"]["trend"]), (9, 10, None))
        d = self._landed(10)
        self.assertEqual(d["summary"]["trend"], {
            "latest_n": 5, "earlier_n": 5,
            "cost": {"latest": None, "earlier": None}, "review_rounds": {"latest": None, "earlier": None},
            "brief_to_pr_s": {"latest": 480.0, "earlier": 180.0},  # tasks 5..9 vs 0..4: 360..600 and 60..300
            "pr_to_merge_s": {"latest": 80.0, "earlier": 30.0}})
        self.assertEqual(self._landed(11)["summary"]["trend"]["latest_n"], 5)
        self.assertEqual(self._landed(11)["summary"]["trend"]["earlier_n"], 6)


def task(slug, merged, cost=5.0, brief=600, first_pass=True):
    return {"slug": slug, "pr": {"merged": merged}, "first_pass": first_pass, "brief_to_pr_s": brief,
            "cost": None if cost is None else {"total": cost}, "review_rounds": 1, "pr_to_merge_s": 60}


def days(*values, **kw):
    return [task("t%d" % i, "2026-11-%02dT00:00:00Z" % (i + 1), v, **kw) for i, v in enumerate(values)]


class RewardTests(unittest.TestCase):
    def kinds(self, landed):
        return [(r["kind"], r["slug"], r["text"]) for r in kaizen._rewards(landed)]

    def test_first_pass_streak(self):
        three = [task("t%d" % i, "2026-11-%02dT00:00:00Z" % (i + 1)) for i in range(5)]
        self.assertEqual(self.kinds(three), [("first_pass_streak", "t4", "5 landed first-pass in a row")])
        three[1]["first_pass"] = False  # a streak of 3 at the end still counts
        self.assertEqual(self.kinds(three), [("first_pass_streak", "t4", "3 landed first-pass in a row")])
        three[4]["first_pass"] = False  # broken by the latest task
        self.assertEqual(self.kinds(three), [])
        three[4]["first_pass"] = None  # no transcripts: not first-pass
        self.assertEqual(self.kinds(three), [])
        self.assertEqual(self.kinds(three[:2]), [])  # fewer than 3

    def test_streak_follows_merge_order_not_list_order(self):
        landed = [task("old", "2026-11-01T00:00:00Z", first_pass=False)] + \
                 [task("n%d" % i, "2026-11-0%dT00:00:00Z" % (i + 2)) for i in range(3)]
        self.assertEqual(self.kinds(list(reversed(landed))), [("first_pass_streak", "n2", "3 landed first-pass in a row")])

    def test_record_low_cost(self):
        base = days(9, 8, 7, 6, 5, first_pass=False)
        self.assertEqual(self.kinds(base), [("record_low_cost", "t4", "record low cost: $5.00")])
        self.assertEqual(self.kinds(days(9, 8, 7, 6, 6, first_pass=False)), [])  # a tie is not a record
        self.assertEqual(self.kinds(days(9, 8, 7, 5, 6, first_pass=False)), [])  # not the latest
        self.assertEqual(self.kinds(days(9, 8, 7, 6, first_pass=False)), [])  # fewer than 5 with a cost
        self.assertEqual(self.kinds(days(9, 8, 7, 6, None, first_pass=False)), [])  # the latest has no cost
        self.assertEqual(self.kinds(days(9, None, 8, 7, 6, 5, first_pass=False))[0][2], "record low cost: $5.00")

    def test_record_fast(self):
        landed = days(*[5] * 5, first_pass=False)
        for t, brief in zip(landed, (900, 800, 700, 600, 125)):
            t["brief_to_pr_s"] = brief
        self.assertEqual(self.kinds(landed), [("record_fast", "t4", "fastest brief to pull request: 2m05s")])
        landed[4]["brief_to_pr_s"] = 600  # a tie
        self.assertEqual(self.kinds(landed), [])
        landed[4]["brief_to_pr_s"] = 1000
        self.assertEqual(self.kinds(landed), [])

    def test_all_three_and_none(self):
        landed = days(9, 8, 7, 6, 5)
        for t, brief in zip(landed, (900, 800, 700, 600, 500)):
            t["brief_to_pr_s"] = brief
        self.assertEqual([r["kind"] for r in kaizen._rewards(landed)], ["first_pass_streak", "record_low_cost", "record_fast"])
        self.assertEqual(kaizen._rewards([]), [])


class GhTests(unittest.TestCase):
    def run_gh(self, **kw):
        with mock.patch.object(kaizen.subprocess, "run", **kw) as run:
            return kaizen.gh_prs(ROOT), run

    def test_lists_pull_requests_in_the_repository(self):
        prs, run = self.run_gh(return_value=subprocess.CompletedProcess([], 0, '[{"number": 1}]', ""))
        self.assertEqual(prs, [{"number": 1}])
        argv, kw = run.call_args
        self.assertEqual(argv[0], ["gh", "pr", "list", "--state", "all", "--limit", "1000", "--json", kaizen.GH_FIELDS])
        self.assertEqual((kw["cwd"], kw["timeout"]), (ROOT, 20))

    def test_a_missing_repository_path_is_not_blamed_on_gh(self):
        with mock.patch.object(kaizen.subprocess, "run") as run, self.assertRaises(kaizen.GhError) as cm:
            kaizen.gh_prs(os.path.join(ROOT, "no-such-dir"))
        self.assertEqual((str(cm.exception), run.called), ("no such repository path", False))

    def test_failures_become_one_line_reasons(self):
        cases = (({"side_effect": FileNotFoundError(2, "No such file or directory: 'gh'")}, "gh unavailable: [Errno 2]"),
                 ({"side_effect": subprocess.TimeoutExpired("gh", 20)}, "gh unavailable: Command 'gh' timed out after 20 seconds"),
                 ({"return_value": subprocess.CompletedProcess([], 1, "", "none of the git remotes\nsecond line")},
                  "none of the git remotes"),
                 ({"return_value": subprocess.CompletedProcess([], 4, "", "")}, "gh exited 4"),
                 ({"return_value": subprocess.CompletedProcess([], 0, "not json", "")}, "gh printed no JSON"),
                 ({"return_value": subprocess.CompletedProcess([], 0, "{}", "")}, "gh printed an unexpected document"))
        for kw, reason in cases:
            with self.assertRaises(kaizen.GhError) as cm:
                self.run_gh(**kw)
            self.assertTrue(str(cm.exception).startswith(reason), (str(cm.exception), reason))
            self.assertNotIn("\n", str(cm.exception))


def run_cli(*argv):
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        rc = cli.main(list(argv))
    return rc, out.getvalue(), err.getvalue()


class CliTests(Store):
    """The command end to end against a fixture store and the existing grouping transcripts (repo /r/alpha, task ci)."""

    def setUp(self):
        super().setUp()
        self.log("events-2026-01.jsonl", [{"ts": "2026-01-06T08:59:00Z", "repo": ALPHA, "slug": "ci", "version": "0.2.0",
                                           "event": "TaskStarted"}])
        self.args = ("--factory-dir", self.factory, "--projects-dir", GROUPING, "--prices", PRICES_PATH, "--agents-dir", AGENTS)

    def gh(self, path):
        return [pr(1, "worktree-ci", "2026-01-06T09:30:00Z", "2026-01-06T09:40:00Z")]

    def test_json_end_to_end(self):
        with mock.patch.object(kaizen, "gh_prs", side_effect=self.gh):
            rc, out, _ = run_cli("kaizen", "--json", *self.args)
        self.assertEqual(rc, 0)
        d = json.loads(out)
        self.assertEqual([t["slug"] for t in d["tasks"]], ["ci"])
        t = d["tasks"][0]
        self.assertEqual((t["started"], t["landed"], t["brief_to_pr_s"], t["pr_to_merge_s"], t["review_rounds"], t["first_pass"]),
                         ("2026-01-06T08:59:00Z", True, 1860, 600, 1, True))
        self.assertEqual(t["cost"], {"total": 0.004366, "unpriced_tokens": 14, "rework": 0.0,
                                     "by_role": {"lead": 0.004227, "coder": 0.0, "reviewer": 0.000133, "explorer": 6e-06,
                                                 "other": 0.0}})
        self.assertEqual(d["prices"], price_file.load(PRICES_PATH).to_dict())
        self.assertEqual(d["summary"]["landed"], 1)

    def test_since_and_plain_summary(self):
        with mock.patch.object(kaizen, "gh_prs", side_effect=self.gh):
            rc, out, _ = run_cli("kaizen", "--json", "--since", "1h", *self.args)
            self.assertEqual((rc, json.loads(out)["tasks"]), (0, []))
            rc, out, _ = run_cli("kaizen", "--json", "--since", "2026-01-06T08:59:00", *self.args)
            self.assertEqual(len(json.loads(out)["tasks"]), 1)
            rc, out, _ = run_cli("kaizen", *self.args)
        self.assertEqual(rc, 0)
        self.assertIn("tasks 1, landed 1, in flight 0", out)
        self.assertRegex(out, r"ci +0\.2\.0 +landed +31m00s +10m00s +\$0\.0044\* +1 +0")

    def test_missing_directory_exits_zero_with_empty_lists(self):
        rc, out, _ = run_cli("kaizen", "--json", "--factory-dir", "/nonexistent-factory", "--projects-dir", GROUPING,
                             "--prices", PRICES_PATH)
        d = json.loads(out)
        self.assertEqual((rc, d["store_found"], d["tasks"], d["repos"], d["stops"], d["outbox"]["entries"]), (0, False, [], [], [], []))
        rc, out, _ = run_cli("kaizen", "--factory-dir", "/nonexistent-factory", "--projects-dir", GROUPING, "--prices", PRICES_PATH)
        self.assertEqual((rc, out), (0, "no factory tasks in /nonexistent-factory\n"))

    def test_document_keys(self):
        with mock.patch.object(kaizen, "gh_prs", side_effect=self.gh):
            _, out, _ = run_cli("kaizen", "--json", *self.args)
        d = json.loads(out)
        self.assertEqual(list(d), ["generated", "factory_dir", "store_found", "prices", "repos", "versions", "tasks", "summary",
                                   "stops", "slowed", "rewards", "outbox", "site_manager"])
        self.assertEqual(d["site_manager"], SiteManagerTests.EMPTY)
        self.assertEqual(list(d["tasks"][0]), ["repo", "repo_path", "slug", "version", "started", "state", "pr", "landed",
                                               "brief_to_pr_s", "pr_to_merge_s", "cost", "review_rounds", "first_pass", "stops",
                                               "stopped_s", "defects"])
        self.assertEqual(list(d["summary"]), ["tasks", "landed", "in_flight", "cost", "brief_to_pr_s", "pr_to_merge_s",
                                              "review_rounds", "first_pass", "rework_share", "by_role", "time", "defects",
                                              "trend_min", "trend"])
        self.assertEqual(list(d["outbox"]), ["issues", "decisions", "topics", "entries"])


if __name__ == "__main__":
    unittest.main()
