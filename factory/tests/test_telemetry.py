import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock

import test_factory as tf

PLUGIN = Path(__file__).resolve().parent.parent
BIN = PLUGIN / "bin" / "factory"
factory = tf.factory
VERSION = json.loads((PLUGIN / ".claude-plugin" / "plugin.json").read_text())["version"]

REPO = "/r"
TASK = f"{REPO}/.claude/worktrees/my-task"
SID = "11111111-aaaa"


class StoreCase(unittest.TestCase):
    """A temporary HOME and STORE, an environment without the session's own variables."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: shutil.rmtree(self.tmp, ignore_errors=True))
        self.store = self.tmp / ".claude" / "factory"
        env = {k: v for k, v in os.environ.items() if not k.startswith("CLAUDE_")}
        patches = [mock.patch.dict(os.environ, {**env, "HOME": str(self.tmp)}, clear=True),
                   mock.patch.object(factory, "STORE", self.store)]
        for p in patches:
            p.start()
            self.addCleanup(p.stop)

    def lines(self, name="events"):
        return [json.loads(line) for path in sorted(self.store.glob(f"{name}*.jsonl")) for line in path.read_text().splitlines()]

    def transcript(self, text, name="t.jsonl"):
        path = self.tmp / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return str(path)

    def run_main(self, argv, stdin="", env=None):
        out, err = io.StringIO(), io.StringIO()
        with mock.patch.dict(os.environ, env or {}), mock.patch.object(sys, "stdin", io.StringIO(stdin)), \
                redirect_stdout(out), redirect_stderr(err):
            code = factory.main(argv)
        return code, out.getvalue(), err.getvalue()

    def fire(self, payload, project=TASK):
        code, out, err = self.run_main(["hook"], json.dumps(payload), {"CLAUDE_PROJECT_DIR": project})
        self.assertEqual((code, out, err), (0, "", ""))


class TagTests(StoreCase):
    def test_repo_and_slug(self):
        cases = {TASK: (REPO, "my-task"), f"{TASK}/src/deep": (REPO, "my-task"),
                 f"{REPO}/.claude/worktrees/agent-ab12": (REPO, None), "/plain/dir": ("/plain/dir", None)}
        for path, want in cases.items():
            with self.subTest(path=path):
                t = factory.tags({"cwd": path})
                self.assertEqual((t["repo"], t["slug"]), want)

    def test_session_directory_precedence(self):
        with mock.patch.dict(os.environ, {"CLAUDE_PROJECT_DIR": TASK}):
            self.assertEqual(factory.tags({"cwd": "/plain/dir"})["slug"], "my-task")
        with mock.patch.object(os, "getcwd", return_value=TASK):
            self.assertEqual(factory.tags()["slug"], "my-task")

    def test_role(self):
        self.assertEqual(factory.tags({"cwd": TASK, "agent_type": "factory:coder"})["role"], "coder")
        self.assertEqual(factory.tags({"cwd": TASK})["role"], "lead")
        self.assertEqual(factory.tags({"cwd": TASK, "agent_id": "a1"})["role"], "subagent")
        self.assertEqual(factory.tags({"cwd": "/plain/dir"})["role"], "other")

    def test_version_and_session(self):
        t = factory.tags({"cwd": TASK, "session_id": SID})
        self.assertEqual((t["version"], t["session"]), (VERSION, SID))
        self.assertRegex(t["ts"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        with mock.patch.dict(os.environ, {"CLAUDE_CODE_SESSION_ID": "env-sid"}):
            self.assertEqual(factory.tags({"cwd": TASK})["session"], "env-sid")

    def test_model_from_payload(self):
        self.assertEqual(factory.tags({"cwd": TASK, "model": "m1"})["model"], "m1")

    def test_model_from_transcript_tail(self):
        old = '{"message":{"model":"old"}}\n' + "x" * 70000 + "\n"
        new = '{"message": {"model" : "claude-opus"}}\n{"message":{"model":"<synthetic>"}}\n'
        path = self.transcript(old + new)
        self.assertEqual(factory.tags({"cwd": TASK, "transcript_path": path})["model"], "claude-opus")
        path = self.transcript('{"message":{"model":"head-only"}}\n' + "x" * 70000)
        self.assertIsNone(factory.tags({"cwd": TASK, "transcript_path": path})["model"])

    def test_model_from_subagent_transcript(self):
        parent = self.transcript('{"message":{"model":"lead-model"}}\n', "p/s1.jsonl")
        self.transcript('{"message":{"model":"agent-model"}}\n', "p/s1/subagents/agent-a1.jsonl")
        t = factory.tags({"cwd": TASK, "transcript_path": parent, "agent_id": "a1"})
        self.assertEqual(t["model"], "agent-model")
        direct = self.transcript('{"message":{"model":"direct"}}\n', "elsewhere.jsonl")
        t = factory.tags({"cwd": TASK, "transcript_path": parent, "agent_id": "a1", "agent_transcript_path": direct})
        self.assertEqual(t["model"], "direct")

    def test_model_from_session_transcript_for_commands(self):
        self.transcript('{"message":{"model":"cmd-model"}}\n', f".claude/projects/-r/{SID}.jsonl")
        with mock.patch.dict(os.environ, {"CLAUDE_CODE_SESSION_ID": SID}):
            self.assertEqual(factory.tags()["model"], "cmd-model")

    def test_model_missing(self):
        self.assertIsNone(factory.tags({"cwd": TASK})["model"])
        self.assertIsNone(factory.tags({"cwd": TASK, "transcript_path": "/nonexistent"})["model"])


class HookTests(StoreCase):
    def event(self, payload, **want):
        self.fire(dict(session_id=SID, **payload))
        line = self.lines()[-1]
        base = {"repo": REPO, "slug": "my-task", "role": "lead", "model": None, "version": VERSION, "session": SID}
        line.pop("ts")
        self.assertEqual(line, {**base, **want})

    def test_one_case_per_row(self):
        e = self.event
        e({"hook_event_name": "SessionStart", "source": "startup", "model": "m"}, event="SessionStart", source="startup", model="m")
        e({"hook_event_name": "SessionEnd", "reason": "other", "prompt": "ignored"}, event="SessionEnd", reason="other")
        for prompt, who in (("<agent-message from=x>", "subagent"), ("<cross-session-message>", "session"),
                            ("<task-notification>", "notification"), ("hello", "human")):
            e({"hook_event_name": "UserPromptSubmit", "prompt": prompt}, event="UserPromptSubmit", **{"from": who})
        e({"hook_event_name": "Stop", "background_tasks": [{"id": 1}, {"id": 2}], "last_assistant_message": "x"},
          event="Stop", background_tasks=2)
        e({"hook_event_name": "SubagentStart", "agent_id": "a1", "agent_type": "factory:explorer"},
          event="SubagentStart", agent_id="a1", role="explorer")
        e({"hook_event_name": "SubagentStop", "agent_id": "a1", "agent_type": "factory:coder", "last_assistant_message": "done"},
          event="SubagentStop", agent_id="a1", role="coder")
        e({"hook_event_name": "Notification", "notification_type": "permission_prompt", "message": "needs you"},
          event="Notification", notification_type="permission_prompt", message="needs you")
        e({"hook_event_name": "PermissionRequest", "tool_name": "Bash", "tool_input": {"command": "x"}},
          event="PermissionRequest", tool_name="Bash")
        e({"hook_event_name": "PermissionDenied", "tool_name": "Bash", "reason": "classifier"},
          event="PermissionDenied", tool_name="Bash", reason="classifier")
        e({"hook_event_name": "PostToolUse", "tool_name": "SubagentHandback", "agent_id": "a1",
           "agent_type": "factory:reviewer", "tool_input": {"message": "report"}},
          event="PostToolUse", tool_name="SubagentHandback", agent_id="a1", role="reviewer")
        for message, kind in (("QUESTION: which?", "QUESTION"), ("BLOCKED: x", "BLOCKED"), ("OVERTIME: x", "OVERTIME"),
                              ("SPLIT: x", "SPLIT"), ("DONE: x", "DONE"), ("DONE x", None), ("hi", None)):
            e({"hook_event_name": "PostToolUse", "tool_name": "SendMessage", "tool_input": {"to": "d1", "message": message}},
              event="PostToolUse", tool_name="SendMessage", to="d1", kind=kind)

    def test_unknown_event_has_tags_only(self):
        self.event({"hook_event_name": "Mystery", "tool_name": "x"}, event="Mystery")

    def run_subprocess(self, stdin, home):
        env = {**os.environ, "HOME": str(home), "CLAUDE_PROJECT_DIR": TASK}
        return subprocess.run([sys.executable, str(BIN), "hook"], input=stdin, capture_output=True, text=True, env=env)

    def test_invalid_json_exits_silently(self):
        p = self.run_subprocess("not json", self.tmp)
        self.assertEqual((p.returncode, p.stdout, p.stderr), (0, "", ""))
        self.assertEqual(self.lines(), [])

    def test_unwritable_store_exits_silently(self):
        (self.tmp / ".claude").mkdir()
        (self.tmp / ".claude" / "factory").write_text("a file, not a directory")
        p = self.run_subprocess(json.dumps({"hook_event_name": "Stop", "session_id": SID}), self.tmp)
        self.assertEqual((p.returncode, p.stdout, p.stderr), (0, "", ""))

    def test_never_runs_git_claude_or_gh(self):
        with mock.patch.object(factory, "run", side_effect=AssertionError("run called")):
            self.fire({"hook_event_name": "Stop", "session_id": SID})
        self.assertEqual(len(self.lines()), 1)


class ParseTests(unittest.TestCase):
    def items(self, text):
        section = factory.feedback_section(text)
        return None if section is None else factory.feedback_items(section)

    def test_heading_forms(self):
        for head in ("## Factory feedback", "**Factory feedback**", "Factory feedback:", "factory FEEDBACK", "### **Factory feedback:**"):
            with self.subTest(head=head):
                self.assertEqual(self.items(f"report\n\n{head}\n- one\n- two\n"), ["one", "two"])

    def test_markers_and_continuations(self):
        text = "Factory feedback\n1. first\n   more of first\n2. second\n* third\n- fourth"
        self.assertEqual(self.items(text), ["first\n   more of first", "second", "third", "fourth"])

    def test_no_markers_is_one_item(self):
        self.assertEqual(self.items("Factory feedback\nthe brief was silent\non the base branch\n"),
                         ["the brief was silent\non the base branch"])

    def test_none_and_empty(self):
        for body in ("none", "None.", " NONE \n", "", "\n\n", "- none", "- \"none\"\n", "1. None.", "**None**", "- _none_"):
            with self.subTest(body=body):
                self.assertEqual(self.items(f"## Factory feedback\n{body}"), [])

    def test_no_heading(self):
        self.assertIsNone(self.items("- an item\n- another"))

    def test_next_heading_ends_section_and_last_heading_wins(self):
        text = "Factory feedback\n- old\n\n## Other\n- not mine\n\n## Factory feedback\n- new\n\n## Tail\n- tail"
        self.assertEqual(self.items(text), ["new"])


class FilingTests(StoreCase):
    REPORT = "Done.\n\n## Factory feedback\n- the brief omitted the branch name\n"

    def feedback(self, *argv):
        return self.run_main(["feedback", *argv])

    def test_from_subagent_handback(self):
        self.fire({"hook_event_name": "PostToolUse", "tool_name": "SubagentHandback", "session_id": SID,
                   "agent_id": "a1", "agent_type": "factory:coder", "tool_input": {"message": self.REPORT}})
        (entry,) = self.lines("feedback")
        self.assertEqual((entry["role"], entry["slug"], entry["text"], entry["status"]),
                         ("coder", "my-task", "the brief omitted the branch name", "open"))
        self.assertRegex(entry["id"], r"^[0-9a-f]{12}$")

    def test_from_done_send_message_has_role_lead_and_not_from_question(self):
        send = {"hook_event_name": "PostToolUse", "tool_name": "SendMessage", "session_id": SID}
        self.fire({**send, "tool_input": {"to": "d1", "message": "QUESTION: x\n\n" + self.REPORT}})
        self.assertEqual(self.lines("feedback"), [])
        self.fire({**send, "tool_input": {"to": "d1", "message": "DONE: url\n\n" + self.REPORT}})
        (entry,) = self.lines("feedback")
        self.assertEqual(entry["role"], "lead")

    def test_not_from_subagent_stop(self):
        self.fire({"hook_event_name": "SubagentStop", "session_id": SID, "agent_id": "a1",
                   "agent_type": "factory:explorer", "last_assistant_message": self.REPORT})
        self.assertEqual(self.lines("feedback"), [])

    def test_odd_shapes_still_record_the_event(self):
        for extra in ({"tool_input": "text"}, {"tool_input": {"message": {"type": "shutdown_request"}}}):
            self.fire({"hook_event_name": "PostToolUse", "tool_name": "SendMessage", "session_id": SID, **extra})
        self.fire({"hook_event_name": "UserPromptSubmit", "session_id": SID, "prompt": 5})
        self.assertEqual([e["event"] for e in self.lines()], ["PostToolUse", "PostToolUse", "UserPromptSubmit"])

    def test_same_item_twice_is_one_entry(self):
        payload = {"hook_event_name": "PostToolUse", "tool_name": "SubagentHandback", "session_id": SID,
                   "agent_id": "a1", "agent_type": "factory:coder", "tool_input": {"message": self.REPORT}}
        self.fire(payload)
        self.fire(payload)
        self.assertEqual(len(self.lines("feedback")), 2)
        self.assertEqual(len(factory.read_feedback()), 1)

    def file(self, text="- first\n- second", *argv):
        code, out, err = self.run_main(["feedback", "add", "--role", "lead", *argv], text, {"CLAUDE_PROJECT_DIR": TASK})
        self.assertEqual((code, err), (0, ""))
        return out.split()

    def test_add_uses_items_or_section_and_overrides(self):
        ids = self.file()
        self.assertEqual(len(ids), 2)
        self.assertEqual(self.file("Factory feedback\n- only\n"), self.file("- only"))
        self.assertEqual(self.file("none"), ["no", "items"])
        self.file("- x", "--slug", "other-task", "--model", "m9")
        last = self.lines("feedback")[-1]
        self.assertEqual((last["slug"], last["model"], last["repo"]), ("other-task", "m9", REPO))

    def test_set_folds_and_refiling_does_not_reopen(self):
        a, b = self.file("- alpha\n- beta")
        code, _, _ = self.feedback("set", a, "accepted")
        self.assertEqual(code, 0)
        self.feedback("set", a, "done", "--pr", "https://x/pr/1")
        self.file("- alpha")
        entries = {e["id"]: e for e in factory.read_feedback()}
        self.assertEqual((entries[a]["status"], entries[a]["pr"], entries[b]["status"]), ("done", "https://x/pr/1", "open"))
        self.assertNotIn("pr", entries[b])
        self.assertEqual(len(entries), 2)

    def test_set_prefix_ambiguous_and_unknown(self):
        a, b = self.file("- alpha\n- beta")
        self.assertEqual(self.feedback("set", a[:8], "rejected")[0], 0)
        self.assertEqual(factory.read_feedback()[0]["status"], "rejected")
        self.assertEqual(self.feedback("set", "zzzz", "done")[0], 2)
        with mock.patch.object(factory, "read_feedback", return_value=[{"id": "ab12"}, {"id": "ab34"}]):
            code, _, err = self.feedback("set", "ab", "done")
        self.assertEqual(code, 2)
        self.assertIn("several", err)

    def test_listing_filter_and_json(self):
        a, b = self.file("- alpha\n- beta\n  continued")
        self.feedback("set", a, "done")
        code, out, _ = self.feedback()
        self.assertEqual(out.splitlines()[1], f"{b}  open  my-task  lead  beta continued")
        self.assertEqual(self.feedback("--status", "done")[1].split()[:2], [a, "done"])
        data = json.loads(self.feedback("--json", "--status", "open")[1])
        self.assertEqual([(d["id"], d["status"]) for d in data], [(b, "open")])
        self.assertEqual(json.loads(self.feedback("--json", "--status", "rejected")[1]), [])

    def decide(self, text):
        return self.run_main(["feedback", "decide", text], env={"CLAUDE_PROJECT_DIR": TASK})

    def test_decide_files_one_line_and_refiling_is_one_entry(self):
        code, out, err = self.decide("  rows keep the full name.\n Why: it says which definition ran. ")
        self.assertEqual((code, err), (0, ""))
        self.decide("rows keep the full name. Why: it says which definition ran.")
        (entry,) = factory.read_feedback()
        self.assertEqual((entry["id"], entry["role"], entry["slug"], entry["kind"], entry["status"], entry["text"]),
                         (out.strip(), "lead", "my-task", "decision", "open",
                          "DECISION: rows keep the full name. Why: it says which definition ran."))

    def test_decide_refuses_empty_and_over_long(self):
        self.assertEqual(self.decide("x" * 200)[0], 0)
        for text in ("", " \n ", "x" * 201):
            code, out, err = self.decide(text)
            self.assertEqual((code, out, len(err.splitlines())), (2, "", 1))
        self.assertEqual(len(self.lines("feedback")), 1)

    def test_kind_filter_and_json(self):
        issue, old = self.file("- the brief omitted the base\n- DECISION: filed before decide existed. Why: x")
        new = self.decide("b. Why: c")[1].strip()
        self.feedback("set", new, "accepted")
        kinds = {e["id"]: e["kind"] for e in json.loads(self.feedback("--json")[1])}
        self.assertEqual(kinds, {issue: "issue", old: "decision", new: "decision"})
        self.assertEqual([line.split()[0] for line in self.feedback("--kind", "issue")[1].splitlines()], [issue])
        self.assertEqual([line.split()[0] for line in self.feedback("--kind", "decision")[1].splitlines()], [old, new])
        data = json.loads(self.feedback("--json", "--kind", "decision", "--status", "open")[1])
        self.assertEqual([d["id"] for d in data], [old])
        self.assertNotIn("kind", self.lines("feedback")[0])

    def test_unreadable_lines_are_skipped(self):
        a, = self.file("- alpha")
        with open(self.store / "feedback.jsonl", "a") as f:
            f.write("garbage\n[1, 2]\n\n")
        self.assertEqual([e["id"] for e in factory.read_feedback()], [a])

    def test_unwritable_store_exits_1(self):
        (self.tmp / ".claude").mkdir()
        self.store.write_text("a file")
        code, _, err = self.run_main(["feedback", "add", "--role", "lead"], "- x", {"CLAUDE_PROJECT_DIR": TASK})
        self.assertEqual(code, 1)
        self.assertEqual(len(err.splitlines()), 1)


class RetroTests(StoreCase):
    GOOD = {"defects": [{"caught_by": "reviewer", "what": "off by one"}], "slowed": ["ci"], "rules_missing": [],
            "rules_dead_weight": ["x"], "brief_questions": []}

    def retro(self, doc, project=TASK):
        text = doc if isinstance(doc, str) else json.dumps(doc)
        return self.run_main(["retro"], text, {"CLAUDE_PROJECT_DIR": project, "CLAUDE_CODE_SESSION_ID": SID})

    def test_valid_object_is_stored_with_tags(self):
        self.assertEqual(self.retro(self.GOOD), (0, "", ""))
        (line,) = self.lines("retros")
        self.assertEqual({k: line[k] for k in ("repo", "slug", "role", "version", "session")},
                         {"repo": REPO, "slug": "my-task", "role": "lead", "version": VERSION, "session": SID})
        self.assertEqual({k: v for k, v in line.items() if k in self.GOOD}, self.GOOD)

    def test_invalid_shapes_are_refused(self):
        bad = {"not json": "{", "not an object": [], "missing key": {k: v for k, v in self.GOOD.items() if k != "slowed"},
               "extra key": {**self.GOOD, "more": []}, "list not list": {**self.GOOD, "slowed": "ci"},
               "empty string": {**self.GOOD, "slowed": [""]}, "blank string": {**self.GOOD, "slowed": ["  "]},
               "non string": {**self.GOOD, "rules_missing": [1]}, "defects not list": {**self.GOOD, "defects": {}},
               "defect not object": {**self.GOOD, "defects": ["x"]},
               "bad caught_by": {**self.GOOD, "defects": [{"caught_by": "luck", "what": "x"}]},
               "empty what": {**self.GOOD, "defects": [{"caught_by": "ci", "what": ""}]},
               "extra defect key": {**self.GOOD, "defects": [{"caught_by": "ci", "what": "x", "n": 1}]}}
        for name, doc in bad.items():
            with self.subTest(name):
                code, out, err = self.retro(doc)
                self.assertEqual((code, out), (2, ""))
                self.assertEqual(len(err.splitlines()), 1)
        self.assertEqual(self.lines("retros"), [])

    def test_empty_lists_are_valid(self):
        empty = {"defects": [], **{k: [] for k in factory.RETRO_LISTS}}
        self.assertEqual(self.retro(empty)[0], 0)

    def test_refused_outside_a_task_worktree(self):
        for project in ("/plain/dir", f"{REPO}/.claude/worktrees/agent-ab12"):
            self.assertEqual(self.retro(self.GOOD, project)[0], 2)
        self.assertEqual(self.lines("retros"), [])

    def test_unwritable_store_exits_1(self):
        (self.tmp / ".claude").mkdir()
        self.store.write_text("a file")
        self.assertEqual(self.retro(self.GOOD)[0], 1)


class TaskEventTests(StoreCase):
    def new(self):
        answers = tf.repo_answers([], [dict(tf.DISPATCHER, name="d1")], tf.NewTests.OK)
        return tf.invoke(["new", "demo-task", "--brief", "do it", "--dispatcher", "d1"], answers)

    def clean(self):
        answers = tf.repo_answers([tf.task_entry("x")], [], tf.git_task_answers("x", time.time(), dirty="", pr=[
            {"state": "MERGED", "url": "u", "headRefOid": "sha-x", "baseRefName": "main"}]))
        answers.update({("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", "")})
        return tf.invoke(["clean"], answers)

    def test_new_records_task_started(self):
        code, out, err, _ = self.new()
        self.assertEqual(code, 0, err)
        (e,) = self.lines()
        self.assertEqual({k: v for k, v in e.items() if k != "ts"},
                         {"repo": tf.ROOT, "slug": "demo-task", "role": "dispatcher", "model": None, "version": VERSION,
                          "session": "sid-d", "event": "TaskStarted", "base": "main", "lead_model": "fable"})

    def test_clean_records_task_removed(self):
        code, out, _, _ = self.clean()
        self.assertEqual((code, out.strip()), (0, "removed x"))
        (e,) = self.lines()
        self.assertEqual((e["event"], e["repo"], e["slug"], e["role"], e["model"]), ("TaskRemoved", tf.ROOT, "x", "dispatcher", None))

    def test_unwritable_store_changes_nothing_else(self):
        self.store.parent.mkdir(parents=True)
        self.store.write_text("a file")
        code, out, err, _ = self.new()
        self.assertEqual((code, out.strip()), (0, "started demo-task (session 96dc4869)"))
        code, out, err, _ = self.clean()
        self.assertEqual((code, out.strip(), err), (0, "removed x", ""))


class LogCase(StoreCase):
    NOW = "2026-11-14T10:00:00Z"

    def put(self, name, *records, mtime=None):
        self.store.mkdir(parents=True, exist_ok=True)
        path = self.store / f"{name}.jsonl"
        path.write_text("".join(json.dumps(r) + "\n" for r in records))
        if mtime:
            os.utime(path, (mtime, mtime))
        return path

    def names(self):
        return sorted(p.name for p in self.store.glob("*.jsonl"))

    def clock(self):
        return mock.patch.object(factory, "now", return_value=self.NOW)


class LogFileTests(LogCase):
    def test_log_writes_to_the_records_month(self):
        factory.log("events", {"ts": "2026-03-01T00:00:00Z", "n": 1})
        self.assertEqual(self.names(), ["events-2026-03.jsonl"])

    def test_other_names_stay_unsuffixed(self):
        factory.append("tasks", {"ts": "2026-03-01T00:00:00Z"})
        self.assertEqual(self.names(), ["tasks.jsonl"])

    def test_log_files_order_and_stray_names(self):
        self.put("events-2026-10", {})
        self.put("events-2026-02", {})
        self.put("events", {}, mtime=time.mktime((2025, 6, 15, 12, 0, 0, 0, 0, -1)))
        for stray in ("events-2026-11.jsonl.bak", "events-old.jsonl", "events-2026-1.jsonl", "retros-2026-01.jsonl"):
            (self.store / stray).write_text("{}\n")
        self.assertEqual([(m, p.name) for m, p in factory.log_files("events")],
                         [("2025-06", "events.jsonl"), ("2026-02", "events-2026-02.jsonl"), ("2026-10", "events-2026-10.jsonl")])
        self.assertEqual(factory.log_files("tasks"), [])

    def test_read_log_bounds_are_half_open(self):
        ts = ["2026-10-31T23:59:59Z", "2026-11-14T09:30:00Z", "2026-11-14T09:30:01Z", "2026-11-30T00:00:00Z", "2026-12-01T00:00:00Z"]
        for t in ts:
            factory.log("events", {"ts": t})

        def got(**kw):
            return [r["ts"] for r in factory.read_log("events", **kw)]

        self.assertEqual(got(), ts)
        self.assertEqual(got(since="2026-11"), ts[1:])
        self.assertEqual(got(until="2026-12"), ts[:4])
        self.assertEqual(got(since="2026-11", until="2026-12"), ts[1:4])
        self.assertEqual(got(since="2026-11-14", until="2026-11-15"), ts[1:3])
        self.assertEqual(got(since="2026-11-14T09:30:00Z", until="2026-11-14T09:30:01Z"), ts[1:2])
        self.assertEqual(got(since="2026-11-14T09:30:01Z"), ts[2:])

    def test_months_outside_the_range_are_not_opened(self):
        self.store.mkdir(parents=True)
        for month in ("2026-01", "2026-12"):
            (self.store / f"events-{month}.jsonl").write_bytes(b"\xff\xfe garbage\n")
        factory.log("events", {"ts": "2026-06-01T00:00:00Z"})
        self.assertEqual(len(list(factory.read_log("events", since="2026-02", until="2026-11"))), 1)

    def test_unsuffixed_file_is_always_read_and_filtered(self):
        self.put("events", {"ts": "2026-01-01T00:00:00Z", "n": 1}, {"ts": "2026-06-01T00:00:00Z", "n": 2}, {"n": 3})
        factory.log("events", {"ts": "2026-06-02T00:00:00Z", "n": 4})
        self.assertEqual([r["n"] for r in factory.read_log("events", since="2026-06")], [2, 4])
        self.assertEqual([r["n"] for r in factory.read_log("events")], [1, 2, 3, 4])

    def test_bad_lines_and_vanished_files_are_skipped(self):
        path = self.put("events-2026-05", {"ts": "2026-05-01T00:00:00Z", "n": 1})
        with open(path, "a") as f:
            f.write("garbage\n[1]\n\n42\n")
        ghost = self.store / "events-2026-06.jsonl"
        files = factory.log_files("events") + [("2026-06", ghost)]
        with mock.patch.object(factory, "log_files", return_value=files):
            self.assertEqual([r["n"] for r in factory.read_log("events")], [1])

    def test_feedback_folds_across_files(self):
        self.put("feedback", {"id": "aa", "ts": "2026-05-01T00:00:00Z", "text": "t", "status": "open", "slug": "s", "role": "lead"})
        factory.log("feedback", {"id": "aa", "ts": "2026-11-01T00:00:00Z", "status": "accepted"})
        self.assertEqual([(e["id"], e["status"]) for e in factory.read_feedback()], [("aa", "accepted")])
        self.assertEqual(self.run_main(["feedback", "set", "a", "done", "--pr", "u"])[0], 0)
        (e,) = factory.read_feedback()
        self.assertEqual((e["status"], e["pr"]), ("done", "u"))


class PruneTests(LogCase):
    def old(self, months=13):
        n = 2026 * 12 + 10 - months
        return f"events-{n // 12:04d}-{n % 12 + 1:02d}"

    def entry(self, fid, ts, status="open"):
        """An entry line, then a status line unless it stays open."""
        lines = [{"id": fid, "ts": ts, "text": fid, "status": "open", "slug": None, "role": "lead"}]
        return lines + ([{"id": fid, "ts": ts, "status": status}] if status != "open" else [])

    def test_events_older_than_twelve_months_go(self):
        for name in (self.old(14), self.old(13), self.old(12), "events-2026-11"):
            self.put(name, {})
        old_legacy = self.put("events", {}, mtime=time.mktime((2024, 1, 1, 12, 0, 0, 0, 0, -1)))
        with self.clock():
            factory.prune("events")
        self.assertEqual(self.names(), ["events-2025-11.jsonl", "events-2026-11.jsonl"])
        self.assertFalse(old_legacy.exists())

    def test_fresh_unsuffixed_events_file_stays(self):
        self.put("events", {})
        with self.clock():
            factory.prune("events")
        self.assertEqual(self.names(), ["events.jsonl"])

    def test_retros_are_never_pruned(self):
        self.put("retros-2020-01", {})
        with self.clock():
            factory.prune("retros")
        self.assertEqual(self.names(), ["retros-2020-01.jsonl"])

    def prune_feedback(self, **files):
        for name, records in files.items():
            self.put(name.replace("_", "-"), *records)
        with self.clock():
            factory.prune("feedback")
        return self.names()

    def test_old_closed_feedback_goes(self):
        self.assertEqual(self.prune_feedback(feedback_2025_01=self.entry("a", "2025-01-01T00:00:00Z", "done")
                                                               + self.entry("b", "2025-01-01T00:00:00Z", "rejected"),
                                             feedback_2026_11=self.entry("c", "2026-11-01T00:00:00Z")),
                         ["feedback-2026-11.jsonl"])

    def folded(self):
        return {e["id"]: (e["status"], e.get("pr")) for e in factory.read_feedback()}

    def test_open_or_accepted_feedback_is_carried_to_the_current_month(self):
        old = (self.entry("a", "2025-01-01T00:00:00Z") + self.entry("b", "2025-01-02T00:00:00Z", "accepted")
               + self.entry("c", "2025-01-03T00:00:00Z", "done"))
        self.put("feedback", old[0], mtime=1)
        self.assertEqual(self.prune_feedback(feedback_2025_01=old[1:]), ["feedback-2026-11.jsonl"])
        self.assertEqual(self.folded(), {"a": ("open", None), "b": ("accepted", None)})
        self.assertEqual(self.lines("feedback"), old[:3])

    def test_a_status_line_in_a_later_old_file_follows_its_entry(self):
        names = self.prune_feedback(
            feedback_2025_01=self.entry("a", "2025-01-01T00:00:00Z"),
            feedback_2025_02=[{"id": "a", "ts": "2025-02-01T00:00:00Z", "status": "accepted", "pr": "u"}]
            + self.entry("b", "2025-02-01T00:00:00Z", "done"),
            feedback_2025_03=[{"id": "b", "ts": "2025-03-01T00:00:00Z", "status": "open"}])
        self.assertEqual(names, ["feedback-2026-11.jsonl"])
        self.assertEqual(self.folded(), {"a": ("accepted", "u"), "b": ("open", None)})
        self.assertEqual(len(self.lines("feedback")), 5)

    def test_closed_in_the_old_file_and_refiled_later_stays_closed(self):
        names = self.prune_feedback(feedback_2025_01=self.entry("a", "2025-01-01T00:00:00Z", "done"),
                                    feedback_2026_10=self.entry("a", "2026-10-01T00:00:00Z"))
        self.assertEqual(names, ["feedback-2026-10.jsonl", "feedback-2026-11.jsonl"])
        self.assertEqual(self.folded(), {"a": ("done", None)})

    def test_a_carry_that_cannot_be_written_keeps_the_old_file(self):
        with mock.patch.object(factory, "append", side_effect=PermissionError):
            names = self.prune_feedback(feedback_2025_01=self.entry("a", "2025-01-01T00:00:00Z"))
        self.assertEqual(names, ["feedback-2025-01.jsonl"])

    def test_the_latest_status_wins_whatever_the_file_order(self):
        self.put("feedback", *self.entry("a", "2026-11-01T00:00:00Z"),
                 {"id": "a", "ts": "2026-11-14T11:00:00Z", "status": "done"})
        self.put("feedback-2026-11", {"id": "a", "ts": "2026-11-14T10:00:00Z", "status": "accepted"})
        self.assertEqual(self.folded(), {"a": ("done", None)})

    def test_the_first_write_of_a_month_prunes(self):
        self.put(self.old(), {})
        with self.clock():
            factory.log("events", {"ts": self.NOW, "n": 1})
            self.assertEqual(self.names(), ["events-2026-11.jsonl"])
            self.put(self.old(), {})
            factory.log("events", {"ts": self.NOW, "n": 2})
        self.assertEqual(len(self.names()), 2)

    def test_a_failing_prune_does_not_fail_the_write(self):
        self.put(self.old(), {})
        with self.clock(), mock.patch.object(Path, "unlink", side_effect=PermissionError):
            factory.log("events", {"ts": self.NOW, "n": 1})
        self.assertEqual(len(self.lines()), 2)


WORKER = """
import importlib.machinery, importlib.util, sys
from pathlib import Path
loader = importlib.machinery.SourceFileLoader("factory_bin", sys.argv[1])
f = importlib.util.module_from_spec(importlib.util.spec_from_loader("factory_bin", loader))
loader.exec_module(f)
f.STORE = Path(sys.argv[2])
f.now = lambda: "2026-11-14T10:00:00Z"
if sys.argv[3] == "prune":
    while not Path(sys.argv[4]).exists():
        f.prune("events")
else:
    pid = int(sys.argv[3])
    for seq in range(300):
        f.log("events", {"ts": f.now(), "p": pid, "seq": seq, "pad": "x" * (100 + (seq * 997 + pid * 131) % 16000)})
"""


class ConcurrencyTests(LogCase):
    def test_parallel_writers_with_a_pruner(self):
        for month in ("2024-01", "2025-01", "2025-06"):
            self.put(f"events-{month}", {"ts": f"{month}-01T00:00:00Z"})
        done = self.tmp / "done"
        cmd = [sys.executable, "-c", WORKER, str(BIN), str(self.store)]
        pruner = subprocess.Popen(cmd + ["prune", str(done)])
        writers = [subprocess.Popen(cmd + [str(p)]) for p in range(8)]
        self.assertEqual([w.wait(timeout=60) for w in writers], [0] * 8)
        done.write_text("")
        self.assertEqual(pruner.wait(timeout=60), 0)
        raw = (self.store / "events-2026-11.jsonl").read_text().splitlines()
        self.assertEqual(len(raw), 2400)
        self.assertEqual(sorted((r["p"], r["seq"]) for r in map(json.loads, raw)), [(p, s) for p in range(8) for s in range(300)])
        self.assertEqual(self.names(), ["events-2026-11.jsonl"])


class LogCommandTests(LogCase):
    def test_prints_records_in_range(self):
        for t in ("2026-10-31T00:00:00Z", "2026-11-14T00:00:00Z", "2026-12-01T00:00:00Z"):
            factory.log("events", {"ts": t})
        code, out, _ = self.run_main(["log", "events", "--since", "2026-11", "--until", "2026-12"])
        self.assertEqual((code, [json.loads(line)["ts"] for line in out.splitlines()]), (0, ["2026-11-14T00:00:00Z"]))
        self.assertEqual(len(self.run_main(["log", "events"])[1].splitlines()), 3)
        with self.assertRaises(SystemExit), mock.patch("sys.stderr"):
            factory.main(["log", "feedback"])

    def test_bad_bound_exits_2(self):
        for bound in ("2026", "2026-1", "yesterday", "2026-11-14 09:00", ""):
            with self.subTest(bound):
                code, out, err = self.run_main(["log", "events", "--since", bound])
                self.assertEqual((code, out), (2, ""))
                self.assertEqual(len(err.splitlines()), 1)

    def test_empty_store_prints_nothing(self):
        for name in ("events", "retros"):
            self.assertEqual(self.run_main(["log", name]), (0, "", ""))


class HooksJsonTests(unittest.TestCase):
    def test_every_command_is_the_factory_hook_line(self):
        doc = json.loads((PLUGIN / "hooks" / "hooks.json").read_text())
        events = doc["hooks"]
        self.assertEqual(set(events), {"SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "SubagentStart",
                                       "SubagentStop", "Notification", "PermissionRequest", "PermissionDenied", "PostToolUse"})
        for name, groups in events.items():
            self.assertEqual(len(groups), 1)
            self.assertEqual(groups[0]["hooks"], [{"type": "command", "timeout": 10,
                                                   "command": '"${CLAUDE_PLUGIN_ROOT}/bin/factory" hook'}])
            self.assertEqual(groups[0].get("matcher"), "SendMessage|SubagentHandback" if name == "PostToolUse" else None)


if __name__ == "__main__":
    unittest.main()
