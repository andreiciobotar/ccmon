import io
import json
import os
import plistlib
import tempfile
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from test_factory import DISPATCHER, ROOT, WT, FakeRun, factory, repo_answers, task_entry

NOW = 1_800_000_000
HOUR = 3600
SID = "11111111-aaaa"


def iso(ts):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))


def row(slug, **kw):
    return {"slug": slug, "branch": f"worktree-{slug}", "path": f"{WT}/{slug}", "base": "main", "phase": "motion",
            "overtime": False, "elapsed": None, "last_commit": None, "pr_url": "", "pr_state": "none", "state": None,
            "deliverable": None, **kw}


def lead(slug, sid=SID, **kw):
    return {"sessionId": sid, "name": slug, "cwd": f"{WT}/{slug}", "kind": "background", "status": "busy",
            "state": "working", "startedAt": (NOW - 3 * HOUR) * 1000, **kw}


FINISHED = {"status": "idle", "state": "done"}


def pic(**kw):
    sessions = kw.pop("sessions", [DISPATCHER | {"name": "dispatcher-repo"}])
    return {"now": NOW, "repo": SimpleNamespace(root=ROOT, default="main"), "dispatcher": "dispatcher-repo",
            "sessions": sessions, "rows": [], "declared": {}, "events": [],
            "models": {"coder": "sonnet", "explorer": "haiku", "reviewer": "opus"},
            "gh": lambda *a: [], **kw}


def check(name, p, threshold=None):
    return factory.CRITERIA[name][0](p, threshold)


def ev(slug, event, **kw):
    return {"repo": ROOT, "slug": slug, "event": event, **kw}


class Home(unittest.TestCase):
    """A temporary HOME for the tests that read or write the store or ~/.claude/projects."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        patcher = mock.patch.dict(os.environ, {"HOME": self.tmp.name, "CLAUDE_CODE_SESSION_ID": "sid-d"})
        patcher.start()
        self.addCleanup(patcher.stop)
        self.store = self.home / ".claude" / "factory"
        self.store.mkdir(parents=True)

    def transcript(self, name, mtime, sid=SID):
        path = self.home / ".claude" / "projects" / "proj" / name.format(sid=sid)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("{}\n")
        os.utime(path, (mtime, mtime))
        return path


class CriteriaFileTests(unittest.TestCase):
    def load(self, text):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d, "criteria.json")
            path.write_text(text)
            with mock.patch.object(factory, "CRITERIA_PATH", path):
                return factory.load_criteria()

    def entry(self, **kw):
        return json.dumps([{"name": "quiet", "escalate": "judgment", "threshold": 20, "what": "w", **kw}])

    def test_the_shipped_file_loads_and_names_every_criterion_once(self):
        shipped = factory.load_criteria()
        self.assertEqual(sorted(c["name"] for c in shipped), sorted(factory.CRITERIA))
        self.assertTrue(all(c["what"].strip() for c in shipped))

    def test_refusals(self):
        for text, why in ((self.entry(name="nope"), "unknown criterion"),
                          (self.entry(escalate="sometimes"), "escalate"),
                          (self.entry(threshold=None), "threshold"),
                          (self.entry(threshold=0), "threshold"),
                          (self.entry(threshold=-5), "threshold"),
                          (self.entry(threshold=True), "threshold"),
                          (self.entry(what=" "), "what"),
                          ('{"name": "quiet"}', "JSON list"),
                          ("not json", "JSON")):
            with self.subTest(why=why, text=text), self.assertRaises(factory.Fail) as cm:
                self.load(text)
            self.assertIn(why, str(cm.exception))
            self.assertEqual(cm.exception.code, 2)

    def test_a_threshold_is_optional_where_none_is_needed(self):
        text = json.dumps([{"name": "past-limit", "escalate": "always", "threshold": None, "what": "w"}])
        self.assertEqual(len(self.load(text)), 1)

    def test_a_deleted_line_turns_its_criterion_off(self):
        p = pic(rows=[row("x", overtime=True, elapsed=3 * HOUR)])
        shipped = factory.load_criteria()
        firings, _ = factory.evaluate(p, shipped)
        self.assertIn("past-limit", [f["criterion"] for f in firings])
        firings, _ = factory.evaluate(p, [c for c in shipped if c["name"] != "past-limit"])
        self.assertNotIn("past-limit", [f["criterion"] for f in firings])


class CriteriaTests(Home):
    def test_past_limit(self):
        self.assertEqual(check("past-limit", pic(rows=[row("x", overtime=True, elapsed=3 * HOUR)])),
                         [("x", "x", "working for 3h")])
        self.assertEqual(check("past-limit", pic(rows=[row("x", elapsed=HOUR)])), [])

    def test_ci_red_default(self):
        def runs(*items):
            return pic(gh=lambda *a: [{"workflowName": w, "status": s, "conclusion": c, "url": f"u/{w}"}
                                      for w, s, c in items])
        red = check("ci-red-default", runs(("ci", "completed", "failure")))
        self.assertEqual(red, [(None, "ci", "failure u/ci")])
        self.assertEqual(check("ci-red-default", runs(("ci", "completed", "success"))), [])
        cancelled_over_failure = runs(("ci", "completed", "cancelled"), ("ci", "completed", "failure"))
        self.assertEqual(check("ci-red-default", cancelled_over_failure), [(None, "ci", "failure u/ci")])
        cancelled_over_success = runs(("ci", "completed", "cancelled"), ("ci", "completed", "success"),
                                      ("ci", "completed", "failure"))
        self.assertEqual(check("ci-red-default", cancelled_over_success), [])
        self.assertEqual(check("ci-red-default", runs(("ci", "in_progress", None), ("ci", "completed", "success"))), [])
        two = check("ci-red-default", runs(("ci", "completed", "success"), ("deploy", "completed", "failure")))
        self.assertEqual([f[1] for f in two], ["deploy"])
        asked = []
        check("ci-red-default", pic(gh=lambda *a: asked.append(a) or []))
        self.assertEqual(asked[0][:5], (list, "run", "list", "--branch", "main"))

    def test_dispatcher_missing(self):
        d = DISPATCHER | {"name": "dispatcher-repo"}
        live = [row("x")]
        cases = {"zero": ([], 1), "one": ([d], 0), "two": ([d, d | {"sessionId": "sid-e"}], 1),
                 "a finished one does not count": ([d, d | {"sessionId": "sid-e", "kind": "background", **FINISHED}], 0),
                 "other names do not count": ([DISPATCHER | {"name": "other"}], 1)}
        for label, (sessions, fires) in cases.items():
            with self.subTest(label):
                found = check("dispatcher-missing", pic(sessions=sessions, rows=live))
                self.assertEqual(len(found), fires)
        self.assertEqual(check("dispatcher-missing", pic(sessions=[], rows=[])), [])
        self.assertEqual(check("dispatcher-missing", pic(sessions=[], rows=[row("x", phase="settled")])), [])
        for phase in ("blocked", "review"):
            self.assertEqual(len(check("dispatcher-missing", pic(sessions=[], rows=[row("x", phase=phase)]))), 1)

    def quiet_pic(self, **kw):
        r = row("x", **kw)
        return pic(rows=[r], sessions=[lead("x")]), r

    def test_quiet_both_sides_of_the_threshold(self):
        p, _ = self.quiet_pic()
        self.transcript("{sid}.jsonl", NOW - 21 * 60)
        self.assertEqual(len(check("quiet", p, 20)), 1)
        self.transcript("{sid}.jsonl", NOW - 19 * 60)
        self.assertEqual(check("quiet", p, 20), [])

    def test_quiet_counts_subagent_transcripts_and_commits(self):
        p, _ = self.quiet_pic()
        self.transcript("{sid}.jsonl", NOW - 60 * 60)
        self.assertEqual(len(check("quiet", p, 20)), 1)
        self.transcript("{sid}/subagents/agent-1.jsonl", NOW - 60)
        self.assertEqual(check("quiet", p, 20), [])
        p, _ = self.quiet_pic(last_commit=NOW - 60)
        self.assertEqual(check("quiet", p, 20), [])

    def test_quiet_needs_a_working_lead_in_motion(self):
        self.transcript("{sid}.jsonl", NOW - 60 * 60)
        for r, s in ((row("x", phase="review"), lead("x")), (row("x"), lead("x", **FINISHED)),
                     (row("x"), None)):
            with self.subTest(phase=r["phase"], session=s):
                self.assertEqual(check("quiet", pic(rows=[r], sessions=[s] if s else []), 20), [])

    def test_ended_undeclared(self):
        r = row("x")
        done = lead("x", status="idle", state="done", startedAt=(NOW - HOUR) * 1000)
        self.assertEqual(len(check("ended-undeclared", pic(rows=[r], sessions=[done]), 10)), 1)
        young = lead("x", status="idle", state="done", startedAt=(NOW - 5 * 60) * 1000)
        self.assertEqual(check("ended-undeclared", pic(rows=[r], sessions=[young]), 10), [])
        self.assertEqual(check("ended-undeclared", pic(rows=[r], sessions=[lead("x")]), 10), [])
        self.assertEqual(check("ended-undeclared", pic(rows=[row("x", phase="review")], sessions=[done]), 10), [])

    def test_blocked_unanswered(self):
        for minutes, fires in ((31, 1), (29, 0)):
            with self.subTest(minutes):
                p = pic(rows=[row("x", phase="blocked")], declared={"x": {"ts": iso(NOW - minutes * 60)}})
                self.assertEqual(len(check("blocked-unanswered", p, 30)), fires)
        p = pic(rows=[row("x", phase="motion")], declared={"x": {"ts": iso(NOW - HOUR)}})
        self.assertEqual(check("blocked-unanswered", p, 30), [])

    def test_handover_unacknowledged(self):
        for hours, fires in ((25, 1), (23, 0)):
            with self.subTest(hours):
                r = row("x", phase="review", state="handed-over", deliverable="u")
                p = pic(rows=[r], declared={"x": {"ts": iso(NOW - hours * HOUR)}})
                self.assertEqual(len(check("handover-unacknowledged", p, 24)), fires)
        r = row("x", phase="review", state=None, pr_url="u")
        self.assertEqual(check("handover-unacknowledged", pic(rows=[r], declared={"x": {"ts": iso(0)}}), 24), [])

    def test_review_loops_threshold_and_slug_reuse(self):
        reviewers = [ev("x", "SubagentStart", role="reviewer")] * 3
        p = pic(rows=[row("x")], events=[ev("x", "TaskStarted"), *reviewers])
        self.assertEqual(len(check("review-loops", p, 3)), 1)
        p = pic(rows=[row("x")], events=[ev("x", "TaskStarted"), *reviewers[:2]])
        self.assertEqual(check("review-loops", p, 3), [])
        reused = [ev("x", "TaskStarted"), *reviewers, ev("x", "TaskRemoved"), ev("x", "TaskStarted"), reviewers[0]]
        self.assertEqual(check("review-loops", pic(rows=[row("x")], events=reused), 3), [])
        other = [ev("y", "SubagentStart", role="reviewer")] * 3
        self.assertEqual(check("review-loops", pic(rows=[row("x")], events=other), 3), [])
        self.assertEqual(check("review-loops", pic(rows=[row("x", phase="review")], events=reviewers), 3), [])

    def test_wrong_model(self):
        started = ev("x", "TaskStarted", lead_model="fable")
        for label, events, expected in (
                ("a null model is not judged", [started, ev("x", "SubagentStart", role="coder", model=None)], []),
                ("the pinned model", [started, ev("x", "PostToolUse", role="coder", model="claude-sonnet-5-5")], []),
                ("a wrong coder", [started, ev("x", "PostToolUse", role="coder", model="claude-opus-5-5")],
                 [("x", "x/coder", "claude-opus-5-5, pinned sonnet")]),
                ("the lead's model", [started, ev("x", "PostToolUse", role="lead", model="claude-fable-5-2")], []),
                ("a wrong lead", [started, ev("x", "PostToolUse", role="lead", model="claude-opus-5-5")],
                 [("x", "x/lead", "claude-opus-5-5, pinned fable")]),
                ("a null lead model", [started, ev("x", "PostToolUse", role="lead", model=None)], []),
                ("no TaskStarted: no lead check", [ev("x", "PostToolUse", role="lead", model="claude-opus-5-5")], []),
                ("a role that is not pinned", [started, ev("x", "PostToolUse", role="other", model="claude-opus-5-5")],
                 []),
                ("before the last TaskStarted", [ev("x", "PostToolUse", role="coder", model="claude-opus-5-5"),
                                                 started], [])):
            with self.subTest(label):
                self.assertEqual(check("wrong-model", pic(rows=[row("x")], events=events)), expected)

    def test_ci_red_handover(self):
        r = row("x", phase="review", pr_url="https://pr/1")
        asked = []

        def checks(*a):
            asked.append(a)
            return [{"name": "test", "bucket": "fail"}, {"name": "lint", "bucket": "pass"}]
        found = check("ci-red-handover", pic(rows=[r], gh=checks))
        self.assertEqual(found, [("x", "x", "failed: test https://pr/1")])
        self.assertEqual(asked, [(list, "pr", "checks", "https://pr/1", "--json", "name,bucket")])
        green = pic(rows=[r], gh=lambda *a: [{"name": "t", "bucket": "pass"}, {"name": "p", "bucket": "pending"}])
        self.assertEqual(check("ci-red-handover", green), [])
        nothing = pic(rows=[row("x", phase="motion", pr_url="u"), row("y", phase="review")], gh=checks)
        self.assertEqual(check("ci-red-handover", nothing), [])

    def test_deep_stack_at_and_above_the_threshold(self):
        def chain(n):
            return [row(f"t{i}", base="main" if i == 0 else f"worktree-t{i - 1}") for i in range(n)]
        self.assertEqual(check("deep-stack", pic(rows=chain(3)), 3), [])
        found = check("deep-stack", pic(rows=chain(4)), 3)
        self.assertEqual(found, [("t3", "t3", "4 deep: t3 on t2 on t1 on t0")])
        loop = [row("a", base="worktree-b"), row("b", base="worktree-a")]
        self.assertEqual(check("deep-stack", pic(rows=loop), 3), [])

    def test_merged_not_cleaned(self):
        r = row("x", phase="merged", pr_url="https://pr/1")
        for hours, fires in ((3, 1), (1, 0)):
            with self.subTest(hours):
                p = pic(rows=[r], gh=lambda *a: {"mergedAt": iso(NOW - hours * HOUR)})
                self.assertEqual(len(check("merged-not-cleaned", p, 2)), fires)
        for hours, fires in ((3, 1), (1, 0)):
            with self.subTest(settled=hours):
                p = pic(rows=[row("x", phase="settled")], declared={"x": {"ts": iso(NOW - hours * HOUR)}})
                self.assertEqual(len(check("merged-not-cleaned", p, 2)), fires)

    def test_what_cannot_be_read_is_not_evaluated(self):
        criteria = factory.load_criteria()
        firings, skipped = factory.evaluate(pic(sessions=None, rows=None, events=None, models={}), criteria)
        self.assertEqual(firings, [])
        names = {s["criterion"] for s in skipped}
        self.assertEqual(names, {c["name"] for c in criteria} - {"ci-red-default"})
        why = {s["criterion"]: s["why"] for s in skipped}
        self.assertIn("sessions cannot be listed", why["past-limit"])
        self.assertIn("no pinned models", why["wrong-model"])

    def test_no_event_log_and_gh_failing(self):
        _, skipped = factory.evaluate(pic(events=None, rows=[row("x")]), [c for c in factory.load_criteria()
                                                          if c["name"] in ("review-loops", "wrong-model")])
        self.assertEqual({s["criterion"]: s["why"] for s in skipped},
                         {"review-loops": "no event log", "wrong-model": "no event log"})

        def broken(*args):
            raise factory.NotEvaluated("gh run list failed: no auth")
        firings, skipped = factory.evaluate(pic(gh=broken, rows=[row("x", phase="review", pr_url="u")]),
                                            [c for c in factory.load_criteria()
                                             if c["name"] in ("ci-red-default", "ci-red-handover",
                                                               "merged-not-cleaned", "past-limit")])
        self.assertEqual(firings, [])
        self.assertEqual({s["criterion"] for s in skipped}, {"ci-red-default", "ci-red-handover"})

    def test_gh_json_reports_failures_wrong_shapes_and_keeps_json_from_a_failing_exit(self):
        repo = SimpleNamespace(root=ROOT)
        for result in ((1, "", "not logged in"), (1, "garbage", "")):
            with mock.patch.object(factory, "run", FakeRun({("gh",): result})), self.assertRaises(factory.NotEvaluated):
                factory.gh_json(repo, list, "pr", "checks", "u")
        for kind, out in ((list, "{}"), (dict, "[]"), (list, "[1]"), (list, "null")):
            with self.subTest(kind=kind, out=out), mock.patch.object(factory, "run", FakeRun({("gh",): (0, out, "")})), \
                    self.assertRaises(factory.NotEvaluated) as cm:
                factory.gh_json(repo, kind, "run", "list")
            self.assertNotIsInstance(cm.exception, factory.NoJson)
        with mock.patch.object(factory, "run", FakeRun({("gh",): (8, '[{"bucket": "fail"}]', "")})):
            self.assertEqual(factory.gh_json(repo, list, "pr", "checks", "u"), [{"bucket": "fail"}])

    def test_a_wrong_shape_from_gh_is_not_evaluated_but_a_pr_without_checks_is_skipped(self):
        repo = SimpleNamespace(root=ROOT, default="main")
        gh = lambda *a: factory.gh_json(repo, *a)  # noqa: E731
        listed = {("gh", "run"): (0, '{"a": 1}', ""), ("gh", "pr", "view"): (0, "[]", ""),
                  ("gh", "pr", "checks"): (1, "", "no checks reported")}
        reviewing = row("x", phase="review", pr_url="https://pr/1")
        merged = row("y", phase="merged", pr_url="https://pr/2")
        with mock.patch.object(factory, "run", FakeRun(listed)):
            _, skipped = factory.evaluate(pic(gh=gh, rows=[reviewing, merged]), [
                c for c in factory.load_criteria() if c["name"] in ("ci-red-default", "ci-red-handover",
                                                                    "merged-not-cleaned")])
        self.assertEqual({s["criterion"] for s in skipped}, {"ci-red-default", "merged-not-cleaned"})
        with mock.patch.object(factory, "run", FakeRun(listed)):
            self.assertEqual(check("ci-red-handover", pic(gh=gh, rows=[reviewing])), [])

    def test_phase_criteria_are_not_evaluated_when_phases_cannot_be_trusted(self):
        phase_based = [c for c in factory.load_criteria() if c["name"] in (
            "dispatcher-missing", "quiet", "ended-undeclared", "blocked-unanswered", "handover-unacknowledged",
            "review-loops", "ci-red-handover", "merged-not-cleaned", "base-merged")]
        trusted = [c for c in factory.load_criteria() if c["name"] in ("past-limit", "wrong-model", "deep-stack")]
        unplaced = {"id": "s9", "kind": "background", "status": "busy", "state": "working"}
        for label, p in (("an unplaced session", pic(rows=[row("x")], sessions=[unplaced])),
                         ("an unknown pull request state", pic(rows=[row("x", pr_state="unknown")]))):
            with self.subTest(label):
                firings, skipped = factory.evaluate(p, phase_based)
                self.assertEqual(firings, [])
                self.assertEqual([s["criterion"] for s in skipped], [c["name"] for c in phase_based])
                self.assertIn("no working directory" if "unplaced" in label else "pull request state is unknown",
                              skipped[0]["why"])
                self.assertEqual(factory.evaluate(p, trusted)[1], [])
        self.assertEqual(factory.evaluate(pic(rows=[row("x", branch=None, pr_state="unknown")]), phase_based)[1], [])

    def test_last_activity_survives_a_null_start(self):
        r = row("x", last_commit=NOW - 100)
        self.assertEqual(factory.last_activity(pic(), r, lead("x", startedAt=None)), NOW - 100)
        self.assertEqual(factory.last_activity(pic(), row("x"), lead("x", startedAt=None)), None)

    def test_fold_skips_records_without_a_string_id(self):
        records = [{"repo": ROOT, "decision": "escalated"}, {"repo": ROOT, "id": 7, "cleared": True},
                   {"repo": ROOT, "id": ["a"], "criterion": "c"}, fired("a1")]
        folded, misses = factory.fold(records)
        self.assertEqual(([f["id"] for f in folded], misses), (["a1"], []))


    def test_a_failing_pr_checks_call_is_not_evaluated(self):
        repo = SimpleNamespace(root=ROOT, default="main")
        gh = lambda *a: factory.gh_json(repo, *a)  # noqa: E731
        reviewing = row("x", phase="review", pr_url="https://pr/1")
        with mock.patch.object(factory, "run", FakeRun({("gh", "pr", "checks"): (1, "", "API rate limit exceeded")})):
            with self.assertRaises(factory.NotEvaluated):
                check("ci-red-handover", pic(gh=gh, rows=[reviewing]))

    def test_an_exited_dispatcher_does_not_count(self):
        exited = {"sessionId": "sid-d", "name": "dispatcher-repo", "cwd": ROOT, "kind": "background", "state": "done"}
        self.assertEqual(len(check("dispatcher-missing", pic(sessions=[exited], rows=[row("x")]))), 1)

    def test_events_are_read_from_the_month_files_too(self):
        (self.store / "events.jsonl").write_text(json.dumps(ev("x", "TaskStarted", ts=iso(NOW - 9 * HOUR))) + "\n")
        (self.store / "events-2027-01.jsonl").write_text("".join(
            json.dumps(e) + "\n" for e in [ev("x", "SubagentStart", role="reviewer", ts=iso(NOW - HOUR)),
                                           ev("x", "SubagentStart", role="reviewer", ts=iso(NOW - 2 * HOUR)),
                                           ev("y", "SubagentStart", role="reviewer", ts=iso(NOW)) | {"repo": "/other"}]))
        events = factory.repo_log("events", ROOT)
        self.assertEqual([e["event"] for e in events], ["TaskStarted", "SubagentStart", "SubagentStart"])
        self.assertEqual([e["ts"] for e in events], sorted(e["ts"] for e in events))
        self.assertEqual(check("review-loops", pic(events=events, rows=[row("x")]), 2),
                         [("x", "x", "2 reviewer dispatches since it started")])
        self.assertIsNone(factory.repo_log("nothing", ROOT))

    def test_the_tail_of_a_long_transcript_drops_the_cut_line_and_odd_lines(self):
        entry = lambda i: json.dumps({"timestamp": f"T{i}", "message": {"role": "user", "content": f"m{i}"}})  # noqa: E731
        path = self.transcript("{sid}.jsonl", NOW)
        lines = [entry(i) + " " * 200 for i in range(400)]
        lines[-3] = json.dumps({"timestamp": "TU", "message": {"role": "user", "content": "a\u2028b"}},
                               ensure_ascii=False)
        lines[-2] = "[" * 20000
        path.write_text("\n".join(lines) + "\n")
        tail = factory.transcript_tail(path)
        self.assertEqual(len(tail), 15)
        self.assertEqual(tail[-1], "T399 user: m399")
        self.assertEqual(tail[-2], "TU user: a\u2028b")
        self.assertTrue(all(t.startswith("T") for t in tail))
        size = path.stat().st_size
        self.assertGreater(size, 65536)
        with open(path, "rb") as f:
            f.seek(size - 65536)
            cut = f.read().split(b"\n")[0]
        self.assertNotEqual(cut, b"")


class RunTests(unittest.TestCase):
    def test_a_timeout_returns_124(self):
        self.assertEqual(factory.run(["sleep", "5"], timeout=0.1), (124, "", "timed out"))
        self.assertEqual(factory.run(["true"], timeout=5)[0], 0)


class Harness(Home):
    """Runs the CLI against a faked `run`, a temporary store, a fixed clock and its own criteria and role files."""

    CRITERIA = ["past-limit", "dispatcher-missing", "blocked-unanswered"]

    def setUp(self):
        super().setUp()
        self.fake = None
        self.role = self.home / "site-manager.md"
        self.role.write_text("SITE MANAGER RULES")
        self.criteria_path = self.home / "criteria.json"
        self.use(*self.CRITERIA)
        self.clock = NOW
        for patcher in (mock.patch.object(factory, "CRITERIA_PATH", self.criteria_path),
                        mock.patch.object(factory, "ROLE_PATH", self.role),
                        mock.patch.object(factory, "now", lambda: iso(self.clock)),
                        mock.patch.object(factory.time, "time", lambda: self.clock)):
            patcher.start()
            self.addCleanup(patcher.stop)

    def use(self, *names):
        shipped = {c["name"]: c for c in factory.load_criteria()}
        self.criteria_path.write_text("[" + ",\n".join(json.dumps(shipped[n]) for n in names) + "]")

    def seed(self, name, records):
        with open(self.store / f"{name}.jsonl", "a") as f:
            f.writelines(json.dumps(r) + "\n" for r in records)

    def records(self, name="firings"):
        path = self.store / f"{name}.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def cli(self, *argv, answers=None, sessions=(DISPATCHER | {"name": "dispatcher-repo"},), entries=(), at=None):
        base = repo_answers([task_entry(s) for s in entries], sessions)
        base[("claude", "-p")] = reply()
        base[("gh", "pr", "list")] = (0, "[]", "")
        self.fake = TimedRun({**base, **(answers or {})})
        if at is not None:
            self.clock = at
        out, err = io.StringIO(), io.StringIO()
        with mock.patch.object(factory, "run", self.fake), redirect_stdout(out), redirect_stderr(err):
            code = factory.main(list(argv))
        return code, out.getvalue(), err.getvalue()

    def claude_runs(self):
        return [c for c in self.fake.calls if c[:2] == ["claude", "-p"]]

    def overtime(self, slug="x"):
        return lead(slug, startedAt=int((self.clock - 3 * HOUR) * 1000))

    def tick(self, **kw):
        kw.setdefault("sessions", [DISPATCHER | {"name": "dispatcher-repo"}, self.overtime()])
        kw.setdefault("entries", ["x"])
        return self.cli("watch", **kw)


def reply(decisions=(), sent=True, cost=0.15):
    doc = {"structured_output": {"sent": sent, "decisions": list(decisions)}}
    return 0, json.dumps({**doc, **({"total_cost_usd": cost} if cost is not None else {})}), ""


def choice(fid, decision="escalated", reason="because"):
    return {"id": fid, "decision": decision, "reason": reason}


class TimedRun(FakeRun):
    def __init__(self, answers):
        super().__init__(answers)
        self.timeouts = []

    def __call__(self, argv, cwd=None, timeout=None):
        self.timeouts.append(timeout)
        return super().__call__(argv, cwd)


def fired(fid, criterion="past-limit", escalate="always", subject="x", ts=NOW - 10 * HOUR, repo=ROOT):
    return {"ts": iso(ts), "repo": repo, "id": fid, "criterion": criterion, "escalate": escalate, "slug": subject,
            "subject": subject, "detail": "d"}


def decided(fid, decision, ts=NOW - HOUR, reason="why"):
    return {"ts": iso(ts), "repo": ROOT, "id": fid, "decision": decision, "reason": reason, "by": "site-manager"}


class HealthTests(Harness):
    def test_table_and_json_write_nothing(self):
        self.seed("events", [ev("x", "TaskStarted")])
        before = {p.name: p.read_bytes() for p in self.store.iterdir()}
        code, out, _ = self.cli("health", sessions=[DISPATCHER | {"name": "dispatcher-repo"}, self.overtime()],
                                entries=["x"])
        self.assertEqual((code, out), (0, "past-limit  always  x  working for 3h\n"))
        code, out, _ = self.cli("health", "--json", sessions=[DISPATCHER | {"name": "dispatcher-repo"},
                                                                self.overtime()], entries=["x"])
        doc = json.loads(out)
        self.assertEqual([(f["criterion"], f["escalate"], f["slug"], f["subject"]) for f in doc["firings"]],
                         [("past-limit", "always", "x", "x")])
        self.assertTrue(doc["firings"][0]["what"])
        self.assertEqual(doc["not_evaluated"], [])
        self.assertEqual({p.name: p.read_bytes() for p in self.store.iterdir()}, before)

    def test_nothing_fired_and_not_evaluated_lines(self):
        code, out, _ = self.cli("health")
        self.assertEqual((code, out), (0, "nothing fired\n"))
        code, out, _ = self.cli("health", answers={("claude", "agents"): (1, "", "boom")})
        self.assertEqual(code, 0)
        self.assertIn("not evaluated: past-limit: sessions cannot be listed", out)

    def test_the_dispatcher_option(self):
        _, out, _ = self.cli("health", "--dispatcher", "boss", entries=["x"])
        self.assertIn("0 unfinished sessions named boss", out)

    def test_a_bad_criteria_file_is_a_usage_error(self):
        self.criteria_path.write_text('[{"name": "nope"}]')
        code, _, err = self.cli("health")
        self.assertEqual(code, 2)
        self.assertIn("unknown criterion", err)


class WatchTests(Harness):
    def test_nothing_fired_starts_no_claude(self):
        code, out, _ = self.tick(sessions=[DISPATCHER | {"name": "dispatcher-repo"}], entries=[])
        self.assertEqual(code, 0)
        self.assertTrue(out.startswith(f"{iso(NOW)} watch repo\n"))
        self.assertIn("nothing new", out)
        self.assertEqual(self.claude_runs(), [])
        self.assertEqual(self.records(), [])

    def test_a_new_firing_is_logged_and_sent(self):
        code, out, _ = self.tick()
        [record] = self.records()
        self.assertEqual((code, record["criterion"], record["subject"], record["escalate"], record["repo"]),
                         (0, "past-limit", "x", "always", ROOT))
        self.assertEqual(len(record["id"]), 12)
        self.assertEqual(len(self.claude_runs()), 1)
        self.assertIn(f"{record['id']}  past-limit  x  undecided", out)

    def test_the_launch_argv(self):
        path = self.transcript("{sid}.jsonl", NOW - 60)
        entries = [{"timestamp": "T0", "message": {"role": "user", "content": "start"}}] + [
            {"timestamp": f"T{i}", "message": {"role": "assistant", "content": [
                {"type": "text", "text": "x" * 400}, {"type": "tool_use", "name": "Bash", "input": {"cmd": "ls"}}]}}
            for i in range(1, 17)]
        entries.append({"timestamp": "TR", "message": {"role": "user", "content": [
            {"type": "tool_result", "content": [{"type": "text", "text": "ok"}]}]}})
        path.write_text("partial\n" + "".join(json.dumps(e) + "\nnot json\n" for e in entries) + '{"no": "message"}\n')
        self.tick()
        [argv] = self.claude_runs()
        self.assertEqual(argv[:-1], [
            "claude", "-p", "--model", "sonnet", "--permission-mode", "dontAsk", "--allowedTools", "SendMessage",
            "ToolSearch", "--output-format", "json", "--json-schema", json.dumps(factory.RUN_SCHEMA),
            "--append-system-prompt", "SITE MANAGER RULES"])
        schema = factory.RUN_SCHEMA
        self.assertEqual(schema["required"], ["sent", "decisions"])
        self.assertEqual(schema["properties"]["decisions"]["items"]["required"], ["id", "decision", "reason"])
        self.assertEqual(schema["properties"]["decisions"]["items"]["properties"]["decision"]["enum"],
                         ["escalated", "suppressed"])
        head, _, body = argv[-1].partition("\n\n")
        self.assertEqual(head, f"Job: watch. Repo: {ROOT}. The dispatcher's session name is dispatcher-repo.")
        [sent] = json.loads(body)
        self.assertEqual(sent["id"], self.records()[0]["id"])
        self.assertEqual((sent["criterion"], sent["escalate"], sent["slug"], sent["subject"]),
                         ("past-limit", "always", "x", "x"))
        self.assertTrue(sent["what"] and sent["detail"])
        self.assertNotIn("session_id", sent)
        self.assertNotIn("transcript", sent)
        self.assertEqual(sent["task"], {"phase": "motion", "state": None, "session": "working", "elapsed": "3h",
                                        "last_commit": "?", "dirty": None, "ahead": None, "behind": None,
                                        "pr_state": "none", "pr_url": "", "overtime": True})
        tail = sent["transcript_tail"]
        self.assertEqual(len(tail), 15)
        self.assertEqual(tail[0][:20], "T3 assistant: " + "x" * 6)
        self.assertEqual(tail[-1], "TR user: [result] ok")
        self.assertEqual(tail[-2], "T16 assistant: " + "x" * 300 + ' [tool Bash] {"cmd": "ls"}')
        self.assertEqual(self.fake.timeouts[-1], 600)

    def test_a_firing_without_a_transcript_or_a_task_carries_no_tail(self):
        self.tick(sessions=[DISPATCHER | {"name": "dispatcher-repo"}, self.overtime()])
        [sent] = json.loads(self.claude_runs()[0][-1].partition("\n\n")[2])
        self.assertNotIn("transcript_tail", sent)
        self.assertIn("task", sent)

    def decisions(self):
        return [(r["id"], r["decision"], r["reason"], r["by"]) for r in self.records() if "decision" in r]

    def test_decisions_are_recorded_from_the_runs_output(self):
        self.seeded_run(choice("aaa", "escalated", " red "))
        self.assertEqual(self.decisions(), [("aaa", "escalated", "red", "site-manager")])

    def test_output_lines_show_the_decision_and_the_cost(self):
        self.seed("firings", [fired("aaa", escalate="judgment", criterion="blocked-unanswered")])
        self.seed("tasks", [{"repo": ROOT, "slug": "x", "state": "blocked", "ts": iso(NOW - 2 * HOUR)}])
        _, out, _ = self.cli("watch", entries=["x"], answers={("claude", "-p"): reply([choice("aaa", "suppressed")])})
        self.assertIn("aaa  blocked-unanswered  x  suppressed", out)
        self.assertTrue(out.endswith("site manager run cost $0.15\n"))
        self.assertEqual(self.decisions(), [("aaa", "suppressed", "because", "site-manager")])

    def test_no_cost_line_without_a_numeric_cost(self):
        _, out, _ = self.tick(answers={("claude", "-p"): reply(cost=None)})
        self.assertNotIn("cost", out)

    def seeded_run(self, *decisions, sent=True, escalate="judgment"):
        """One tick with firing `aaa` open and undecided: a blocked task (judgment) or an overtime lead (always)."""
        if escalate == "judgment":
            self.seed("firings", [fired("aaa", criterion="blocked-unanswered", escalate="judgment")])
            self.seed("tasks", [{"repo": ROOT, "slug": "x", "state": "blocked", "ts": iso(NOW - 2 * HOUR)}])
            kw = {"sessions": [DISPATCHER | {"name": "dispatcher-repo"}]}
        else:
            self.seed("firings", [fired("aaa")])
            kw = {}
        _, out, _ = self.tick(answers={("claude", "-p"): reply(decisions, sent)}, **kw)
        return out

    def test_suppressed_for_an_always_firing_is_not_recorded(self):
        out = self.seeded_run(choice("aaa", "suppressed"), escalate="always")
        self.assertEqual(self.decisions(), [])
        self.assertIn("aaa  past-limit  x  undecided", out)

    def test_sent_false_records_no_escalation(self):
        self.seeded_run(choice("aaa", "escalated"), sent=False)
        self.assertEqual(self.decisions(), [])

    def test_sent_false_still_records_a_suppression(self):
        self.seeded_run(choice("aaa", "suppressed"), sent=False)
        self.assertEqual(self.decisions(), [("aaa", "suppressed", "because", "site-manager")])

    def test_ids_not_sent_malformed_and_repeated_decisions_are_ignored(self):
        self.seeded_run(choice("zzz"), choice("aaa", "maybe"), choice("aaa", "escalated", "  "), "junk",
                        {"id": ["aaa"], "decision": "escalated", "reason": "r"}, {"id": "aaa"},
                        choice("aaa", "escalated", "first"), choice("aaa", "suppressed", "second"))
        self.assertEqual(self.decisions(), [("aaa", "escalated", "first", "site-manager")])

    def test_output_that_is_not_the_expected_json_exits_1_and_records_nothing(self):
        for answer in ((0, "", ""), (0, "not json", ""), (0, "[]", ""), (0, "{}", ""),
                       (0, json.dumps({"structured_output": {"sent": True}}), ""),
                       (0, json.dumps({"structured_output": {"sent": True, "decisions": "x"}}), ""),
                       (1, reply([choice("aaa")])[1], "boom")):
            with self.subTest(answer=answer):
                self.seed("firings", [fired("aaa", escalate="judgment")])
                code, out, err = self.tick(answers={("claude", "-p"): answer})
                self.assertEqual(code, 1)
                self.assertIn("aaa  past-limit  x  undecided", out)
                self.assertEqual(self.decisions(), [])
                self.store.joinpath("firings.jsonl").unlink()

    def test_an_open_blocked_firing_survives_an_unplaced_session(self):
        self.seed("firings", [fired("aaa", criterion="blocked-unanswered", escalate="judgment")])
        unplaced = {"id": "s9", "kind": "background", "status": "busy", "state": "working"}
        code, out, _ = self.cli("watch", sessions=[DISPATCHER | {"name": "dispatcher-repo"}, unplaced], entries=["x"])
        self.assertEqual(code, 0)
        self.assertIn("not evaluated: blocked-unanswered: session s9 has no working directory", out)
        self.assertEqual(self.records(), [fired("aaa", criterion="blocked-unanswered", escalate="judgment")])

    def test_an_escalated_firing_is_not_sent_again(self):
        self.tick()
        fid = self.records()[0]["id"]
        self.seed("firings", [decided(fid, "escalated", ts=NOW)])
        self.tick(at=NOW + 1800)
        self.assertEqual(len(self.claude_runs()), 0)
        self.assertEqual(len(self.records()), 2)

    def test_an_undecided_firing_is_sent_again_under_the_same_id(self):
        self.tick()
        _, out, _ = self.tick(at=NOW + 1800)
        self.assertEqual(len(self.records()), 1)
        self.assertEqual(len(self.claude_runs()), 1)
        self.assertIn(self.records()[0]["id"], out)

    def test_a_suppressed_firing_comes_back_only_after_the_resurface_time(self):
        self.seed("firings", [fired("aaa", ts=NOW - 5 * HOUR), decided("aaa", "suppressed", ts=NOW - 60 * 60,
                                                                       reason="noise")])
        self.tick()
        self.assertEqual(self.claude_runs(), [])
        self.assertEqual(len(self.records()), 2)
        self.tick(at=NOW + 61 * 60 + 1)
        [argv] = self.claude_runs()
        records = self.records()
        self.assertEqual([r.get("cleared") for r in records[2:3]], [True])
        self.assertEqual(records[2]["id"], "aaa")
        self.assertTrue(records[3]["detail"].endswith("; suppressed earlier: noise"))
        self.assertNotEqual(records[3]["id"], "aaa")
        self.assertIn(records[3]["id"], argv[-1])

    def test_a_condition_that_ended_is_cleared_and_fires_anew_under_a_new_id(self):
        self.tick()
        first = self.records()[0]["id"]
        code, out, _ = self.tick(at=NOW + 1800, sessions=[DISPATCHER | {"name": "dispatcher-repo"}])
        self.assertIn("nothing new", out)
        self.assertEqual(self.records()[1], {"ts": iso(NOW + 1800), "repo": ROOT, "id": first, "cleared": True})
        self.tick(at=NOW + 3600)
        self.assertEqual([r["id"] for r in self.records()][0], first)
        again = self.records()[2]
        self.assertNotEqual(again["id"], first)
        self.assertEqual(again["criterion"], "past-limit")

    def test_a_criterion_that_was_not_evaluated_clears_nothing(self):
        self.seed("firings", [fired("aaa")])
        code, out, _ = self.tick(answers={("claude", "agents"): (1, "", "boom")})
        self.assertEqual(code, 0)
        self.assertIn("not evaluated: past-limit", out)
        self.assertEqual(self.records(), [fired("aaa")])
        self.assertEqual(self.claude_runs(), [])

    def test_a_firing_of_another_repo_is_ignored(self):
        self.seed("firings", [fired("zzz", repo="/elsewhere")])
        self.tick()
        self.assertEqual(len(self.claude_runs()), 1)
        self.assertEqual(self.records()[1]["subject"], "x")

    def test_dispatcher_missing_notifies_records_and_runs_no_claude(self):
        self.seed("tasks", [{"repo": ROOT, "slug": "x", "state": "blocked", "ts": iso(NOW - 2 * HOUR)}])
        answers = {("osascript",): (0, "", "")}
        code, out, _ = self.cli("watch", sessions=[], entries=["x"], answers=answers)
        self.assertEqual(code, 0)
        self.assertEqual(self.claude_runs(), [])
        [notice] = [c for c in self.fake.calls if c[0] == "osascript"]
        self.assertEqual(notice[1], "-e")
        self.assertIn('with title "factory: repo"', notice[2])
        self.assertIn("0 unfinished sessions named dispatcher-repo", notice[2])
        by_criterion = {r["criterion"]: r for r in self.records() if "criterion" in r}
        [decision] = [r for r in self.records() if "decision" in r]
        self.assertEqual((decision["id"], decision["decision"], decision["by"]),
                         (by_criterion["dispatcher-missing"]["id"], "escalated", "watch"))
        self.assertIn("notified on this machine", decision["reason"])
        self.assertIn("blocked-unanswered", by_criterion)
        self.assertIn(f"{by_criterion['blocked-unanswered']['id']}  blocked-unanswered  x  undecided", out)

    def test_dispatcher_missing_holds_the_others_while_it_fires(self):
        self.seed("tasks", [{"repo": ROOT, "slug": "x", "state": "blocked", "ts": iso(NOW - 2 * HOUR)}])
        answers = {("osascript",): (0, "", "")}
        self.cli("watch", sessions=[], entries=["x"], answers=answers)
        self.cli("watch", sessions=[], entries=["x"], answers=answers, at=NOW + 1800)
        self.assertFalse(self.fake.ran("osascript"))
        self.assertEqual(self.claude_runs(), [])
        code, _, _ = self.cli("watch", entries=["x"], at=NOW + 3600)
        self.assertEqual(len(self.claude_runs()), 1)
        self.assertIn("blocked-unanswered", self.claude_runs()[0][-1])
        self.assertNotIn("dispatcher-missing", self.claude_runs()[0][-1])

    def test_a_failed_notification_leaves_it_undecided_for_the_next_tick(self):
        self.seed("tasks", [{"repo": ROOT, "slug": "x", "state": "blocked", "ts": iso(NOW - 2 * HOUR)}])
        failing = {("osascript",): (1, "", "no display")}
        code, out, _ = self.cli("watch", sessions=[], entries=["x"], answers=failing)
        self.assertEqual(code, 0)
        self.assertEqual([r for r in self.records() if "decision" in r], [])
        self.assertIn("dispatcher-missing  -  undecided", out)
        self.cli("watch", sessions=[], entries=["x"], answers=failing, at=NOW + 1800)
        self.assertTrue(self.fake.ran("osascript"))

    def test_a_timed_out_run_exits_1(self):
        code, out, err = self.tick(answers={("claude", "-p"): (124, "", "timed out")})
        self.assertEqual(code, 1)
        self.assertIn("timed out", err)
        self.assertIn("undecided", out)
        self.assertEqual(self.fake.timeouts[-1], 600)

    def test_a_run_that_cannot_start_exits_1(self):
        code, _, err = self.tick(answers={("claude", "-p"): (127, "", "no such file")})
        self.assertEqual(code, 1)
        self.assertIn("no such file", err)


class RepoWideTests(Harness):
    CRITERIA = ["ci-red-default"]

    def test_a_repo_wide_firing_carries_no_task(self):
        red = json.dumps([{"workflowName": "ci", "status": "completed", "conclusion": "failure", "url": "u"}])
        self.tick(answers={("gh", "run", "list"): (0, red, "")})
        [sent] = json.loads(self.claude_runs()[0][-1].partition("\n\n")[2])
        self.assertEqual((sent["criterion"], sent["slug"]), ("ci-red-default", None))
        self.assertNotIn("task", sent)
        self.assertNotIn("transcript_tail", sent)


class FiringTests(Harness):
    def setUp(self):
        super().setUp()
        self.seed("firings", [fired("abc111", escalate="judgment"), decided("abc111", "escalated"),
                              fired("abc222", escalate="judgment"), decided("abc222", "escalated"),
                              fired("def333", criterion="ci-red-default", subject="ci"),
                              decided("def333", "escalated"),
                              fired("sss444", escalate="judgment"), decided("sss444", "suppressed"),
                              fired("uuu555", escalate="judgment"), "junk", {"repo": ROOT, "decision": "escalated"}])

    def refused(self, *argv, why):
        code, _, err = self.cli(*argv)
        self.assertEqual(code, 2, err)
        self.assertIn(why, err)

    def outcomes(self):
        return [(r["id"], r["outcome"]) for r in self.records() if "outcome" in r]

    def test_an_outcome_by_prefix_id_and_the_last_one_wins(self):
        self.assertEqual(self.cli("firing", "def", "ignored")[0], 0)
        self.assertEqual(self.records()[-1], {"ts": iso(NOW), "repo": ROOT, "id": "def333", "outcome": "ignored"})
        self.cli("firing", "def", "acted")
        self.assertEqual(self.outcomes(), [("def333", "ignored"), ("def333", "acted")])
        folded, _ = factory.fold([r for r in self.records() if isinstance(r, dict)])
        self.assertEqual([f["outcome"] for f in folded if f["id"] == "def333"], ["acted"])

    def test_refusals(self):
        self.refused("firing", "zzz", "acted", why="no firing matches")
        self.refused("firing", "abc", "acted", why="matches 2 firings")
        self.refused("firing", "sss", "acted", why="was not escalated")
        self.refused("firing", "uuu", "ignored", why="was not escalated")
        self.assertEqual(self.outcomes(), [])
        for argv in (("firing", "abc1", "escalated"), ("firing", "abc1", "acted", "--reason", "r")):
            with self.subTest(argv), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                factory.parse_args(list(argv))

    def test_an_outcome_is_allowed_after_the_firing_cleared(self):
        self.seed("firings", [{"ts": iso(NOW), "repo": ROOT, "id": "def333", "cleared": True}])
        self.assertEqual(self.cli("firing", "def", "acted")[0], 0)

    def test_a_record_without_a_string_id_does_not_crash_the_commands(self):
        self.assertEqual(self.cli("firings")[0], 0)
        self.assertEqual(self.cli("firing", "abc1", "acted")[0], 0)


class MissTests(Harness):
    def setUp(self):
        super().setUp()
        self.seed("firings", [fired("aaa", escalate="judgment"), decided("aaa", "escalated"),
                              fired("bbb", escalate="judgment"), decided("bbb", "suppressed"),
                              fired("ccc", escalate="judgment")])

    def test_a_miss_that_never_fired(self):
        self.assertEqual(self.cli("miss", "a lead sat on a question", "--slug", "x")[0], 0)
        self.assertEqual(self.records()[-1], {"ts": iso(NOW), "repo": ROOT, "miss": "a lead sat on a question",
                                              "slug": "x", "firing": None})

    def test_a_miss_names_a_suppressed_or_undecided_firing_by_prefix(self):
        self.cli("miss", "filter was wrong", "--firing", "bb")
        self.cli("miss", "never sent", "--firing", "cc")
        self.assertEqual([r["firing"] for r in self.records() if "miss" in r], ["bbb", "ccc"])

    def test_refusals(self):
        for argv, why in ((("miss", "t", "--firing", "aaa"), "was escalated"), (("miss", "t", "--firing", "zzz"), "no firing"),
                          (("miss", " "), "empty")):
            with self.subTest(argv):
                code, _, err = self.cli(*argv)
                self.assertEqual(code, 2)
                self.assertIn(why, err)
        self.assertEqual([r for r in self.records() if "miss" in r], [])


class FiringsTests(Harness):
    def seed_log(self):
        self.seed("firings", [
            fired("a1", ts=NOW - 5 * HOUR), decided("a1", "escalated"), {"repo": ROOT, "id": "a1", "outcome": "acted"},
            fired("a2", ts=NOW - 4 * HOUR), decided("a2", "escalated"), {"repo": ROOT, "id": "a2", "outcome": "ignored"},
            fired("a3", ts=NOW - 3 * HOUR), decided("a3", "escalated"),
            fired("s1", ts=NOW - 2 * HOUR), decided("s1", "suppressed", reason="noise"),
            {**fired("u1", ts=NOW - HOUR), "ts": iso(NOW - HOUR)}, {"repo": ROOT, "id": "u1", "cleared": True},
            "not json", ["a list"],
            {"ts": iso(NOW), "repo": ROOT, "miss": "late", "slug": "x", "firing": "s1"},
            {"ts": iso(NOW), "repo": ROOT, "miss": "never", "slug": None, "firing": None},
            {"ts": iso(NOW), "repo": ROOT, "miss": "never", "slug": None, "firing": None},
            {"ts": iso(NOW), "repo": "/other", "miss": "other repo", "slug": None, "firing": None}])

    def test_table_and_summary(self):
        self.seed_log()
        code, out, _ = self.cli("firings")
        lines = out.splitlines()
        self.assertEqual(code, 0)
        self.assertEqual(lines[0], f"a1  {iso(NOW - 5 * HOUR)}  past-limit  x  escalated  acted  open  why")
        self.assertEqual(lines[3], f"s1  {iso(NOW - 2 * HOUR)}  past-limit  x  suppressed  -  open  noise")
        self.assertEqual(lines[4], f"u1  {iso(NOW - HOUR)}  past-limit  x  undecided  -  cleared  -")
        self.assertEqual(lines[5], f"miss  {iso(NOW)}  x  s1  late")
        self.assertEqual(lines[-1], "escalated 3 (acted 1, ignored 1, no outcome 1); suppressed 1; "
                                    "missed 3 (1 suppressed first, 2 never fired); precision 0.50; recall 0.25")

    def test_json(self):
        self.seed_log()
        _, out, _ = self.cli("firings", "--json")
        doc = json.loads(out)
        self.assertEqual([f["id"] for f in doc["firings"]], ["a1", "a2", "a3", "s1", "u1"])
        self.assertEqual(len(doc["misses"]), 3)
        self.assertEqual(doc["summary"], {"escalated": 3, "acted": 1, "ignored": 1, "no_outcome": 1, "suppressed": 1,
                                          "missed": 3, "missed_suppressed": 1, "missed_never_fired": 2,
                                          "precision": 0.5, "recall": 0.25})

    def test_zero_denominators(self):
        code, out, _ = self.cli("firings")
        self.assertEqual(out, "escalated 0 (acted 0, ignored 0, no outcome 0); suppressed 0; "
                              "missed 0 (0 suppressed first, 0 never fired); precision -; recall -\n")
        _, out, _ = self.cli("firings", "--json")
        self.assertEqual((json.loads(out)["summary"]["precision"], json.loads(out)["summary"]["recall"]), (None, None))
        self.seed("firings", [{"ts": iso(NOW), "repo": ROOT, "miss": "m", "slug": None, "firing": None}])
        summary = json.loads(self.cli("firings", "--json")[1])["summary"]
        self.assertEqual((summary["precision"], summary["recall"]), (None, 0.0))


class ScheduleTests(Harness):
    LABEL = "com.claude.factory.watch.repo"

    def schedule(self, *argv, platform="darwin", answers=None, path="/plug/factory/bin/factory"):
        with mock.patch.object(factory.sys, "platform", platform), mock.patch.object(factory, "FACTORY", Path(path)):
            return self.cli("schedule", *argv, answers={("launchctl",): (0, "", ""), **(answers or {})})

    def launchctl(self):
        return [c for c in self.fake.calls if c[0] == "launchctl"]

    @property
    def plist(self):
        return self.home / "Library" / "LaunchAgents" / f"{self.LABEL}.plist"

    def test_install_writes_the_plist_and_replaces_the_job(self):
        with mock.patch.dict(os.environ, {"PATH": "/opt/bin:/usr/bin"}):
            code, out, err = self.schedule("install")
        self.assertEqual((code, err), (0, ""))
        job = plistlib.loads(self.plist.read_bytes())
        log = str(self.store / "watch.log")
        self.assertEqual(job, {"Label": self.LABEL,
                               "ProgramArguments": [factory.sys.executable, "/plug/factory/bin/factory", "watch",
                                                    "--dispatcher", "dispatcher-repo"],
                               "WorkingDirectory": ROOT, "StartInterval": 1800,
                               "EnvironmentVariables": {"PATH": "/opt/bin:/usr/bin"},
                               "StandardOutPath": log, "StandardErrorPath": log})
        uid = os.getuid()
        self.assertEqual(self.launchctl(), [["launchctl", "bootout", f"gui/{uid}/{self.LABEL}"],
                                            ["launchctl", "bootstrap", f"gui/{uid}", str(self.plist)]])

    def test_install_with_a_named_dispatcher_and_a_failing_bootstrap(self):
        code, _, err = self.schedule("install", "--dispatcher", "boss", answers={
            ("launchctl", "bootstrap"): (5, "", "Bootstrap failed")})
        self.assertEqual(code, 1)
        self.assertIn("Bootstrap failed", err)
        self.assertEqual(plistlib.loads(self.plist.read_bytes())["ProgramArguments"][-1], "boss")

    def test_install_from_a_worktree_warns(self):
        _, _, err = self.schedule("install", path=f"{WT}/y/factory/bin/factory")
        self.assertIn("task worktree", err)
        _, _, err = self.schedule("install")
        self.assertNotIn("worktree", err)

    def test_install_off_macos_prints_the_cron_line(self):
        code, _, err = self.schedule("install", "--dispatcher", "boss", platform="linux")
        self.assertEqual(code, 2)
        self.assertIn(f"*/30 * * * * cd {ROOT} && {factory.sys.executable} /plug/factory/bin/factory watch "
                      f"--dispatcher boss >> {self.store / 'watch.log'} 2>&1", err)
        self.assertEqual(self.launchctl(), [])
        self.assertFalse(self.plist.exists())

    def test_remove(self):
        code, out, _ = self.schedule("remove")
        self.assertEqual((code, "nothing installed" in out, self.launchctl()), (0, True, []))
        self.schedule("install")
        code, out, _ = self.schedule("remove")
        self.assertEqual(code, 0)
        self.assertFalse(self.plist.exists())
        self.assertEqual(self.launchctl(), [["launchctl", "bootout", f"gui/{os.getuid()}/{self.LABEL}"]])

    def test_status(self):
        _, out, _ = self.schedule("status")
        self.assertEqual(out, f"{self.LABEL}: plist absent, loaded\n")
        self.schedule("install")
        _, out, _ = self.schedule("status", answers={("launchctl", "print"): (113, "", "not found")})
        self.assertEqual(out, f"{self.LABEL}: plist present, not loaded\n")
        self.assertEqual(self.launchctl(), [["launchctl", "print", f"gui/{os.getuid()}/{self.LABEL}"]])


if __name__ == "__main__":
    unittest.main()
