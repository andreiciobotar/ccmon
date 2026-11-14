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
        path = self.store / f"{name}.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

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
