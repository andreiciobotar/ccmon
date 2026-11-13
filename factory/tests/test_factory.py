import importlib.machinery
import importlib.util
import io
import json
import os
import stat
import subprocess
import sys
import tempfile
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock

BIN = Path(__file__).resolve().parent.parent / "bin" / "factory"
_loader = importlib.machinery.SourceFileLoader("factory_bin", str(BIN))
factory = importlib.util.module_from_spec(importlib.util.spec_from_loader("factory_bin", _loader))
_loader.exec_module(factory)

ROOT = "/repo"
WT = f"{ROOT}/.claude/worktrees"
DAY = 86400


def porcelain(*entries):
    """entries: (path, branch or None, prunable)."""
    out = []
    for path, branch, prunable in entries:
        out += [f"worktree {path}", "HEAD abc", f"branch refs/heads/{branch}" if branch else "detached"]
        if prunable:
            out.append("prunable gitdir file points to non-existent location")
        out.append("")
    return "\n".join(out)


def task_entry(slug):
    return (f"{WT}/{slug}", f"worktree-{slug}", False)


class FakeRun:
    """Canned runner. `answers` maps an argv prefix (tuple) to (rc, out, err); longest prefix wins."""

    def __init__(self, answers):
        self.answers = answers
        self.calls = []

    def __call__(self, argv, cwd=None):
        self.calls.append(list(argv))
        best = max((p for p in self.answers if tuple(argv[:len(p)]) == p), key=len, default=None)
        return self.answers[best] if best is not None else (1, "", "unexpected call")

    def ran(self, *prefix):
        return any(c[:len(prefix)] == list(prefix) for c in self.calls)


DISPATCHER = {"sessionId": "sid-d", "name": "dispatcher-repo", "cwd": ROOT, "kind": "interactive"}


def invoke(argv, answers, settings=None, env=None):
    fake = FakeRun(answers)
    out, err = io.StringIO(), io.StringIO()
    environ = {"HOME": "", "CLAUDE_CODE_SESSION_ID": "sid-d", **(env or {})}
    with tempfile.TemporaryDirectory() as home, mock.patch.dict(os.environ, {**environ, "HOME": home}):
        if settings is not None:
            Path(home, ".claude", "factory").mkdir(parents=True)
            Path(home, ".claude", "factory", "settings.json").write_text(settings)
        with mock.patch.object(factory, "run", fake), redirect_stdout(out), redirect_stderr(err):
            code = factory.main(argv)
    return code, out.getvalue(), err.getvalue(), fake


def repo_answers(entries, sessions=(), *extras):
    answers = {
        ("git", "worktree", "list"): (0, porcelain((ROOT, "main", False), *entries), ""),
        ("git", "symbolic-ref"): (0, "refs/remotes/origin/main\n", ""),
        ("claude", "agents"): (0, json.dumps(list(sessions)), ""),
    }
    for extra in extras:
        answers.update(extra)
    return answers


class SlugTests(unittest.TestCase):
    def test_valid(self):
        for slug in ("abc", "tui-now-running", "a1b", "x" * 40, "a-b-c"):
            factory.validate_slug(slug)

    def test_invalid(self):
        for slug in ("ab", "x" * 41, "Abc", "a_b", "a--b", "-ab", "ab-", "a b", "agent-12ab", "ünï"):
            with self.assertRaises(factory.Fail, msg=slug):
                factory.validate_slug(slug)


class SettingsTests(unittest.TestCase):
    def load(self, text=None):
        with tempfile.TemporaryDirectory() as home, mock.patch.dict(os.environ, {"HOME": home}):
            if text is not None:
                Path(home, ".claude", "factory").mkdir(parents=True)
                Path(home, ".claude", "factory", "settings.json").write_text(text)
            return factory.load_settings()

    def test_defaults(self):
        want = {"worktree_cap": 5, "lead_model": "fable", "lead_permission_mode": "auto",
                "task_time_limit_hours": 2}
        self.assertEqual(self.load(), want)
        self.assertEqual(self.load("{}"), want)

    def test_unreadable_file_names_the_file(self):
        for text in ("not json", "[1]"):
            with self.assertRaisesRegex(factory.Fail, "settings.json"):
                self.load(text)

    def test_overrides(self):
        got = self.load('{"worktree_cap": 2, "lead_model": "opus", "lead_permission_mode": "plan", '
                        '"task_time_limit_hours": 0.5}')
        self.assertEqual(got, {"worktree_cap": 2, "lead_model": "opus", "lead_permission_mode": "plan",
                               "task_time_limit_hours": 0.5})
        self.assertEqual(self.load('{"worktree_cap": 9}')["lead_model"], "fable")

    def test_invalid_values_name_the_key(self):
        for text, key in (('{"worktree_cap": 0}', "worktree_cap"), ('{"worktree_cap": "3"}', "worktree_cap"),
                          ('{"worktree_cap": true}', "worktree_cap"), ('{"lead_model": ""}', "lead_model"),
                          ('{"lead_model": 3}', "lead_model"),
                          ('{"lead_permission_mode": "yolo"}', "lead_permission_mode"),
                          ('{"task_time_limit_hours": 0}', "task_time_limit_hours"),
                          ('{"task_time_limit_hours": -1}', "task_time_limit_hours"),
                          ('{"task_time_limit_hours": "2"}', "task_time_limit_hours"),
                          ('{"task_time_limit_hours": true}', "task_time_limit_hours")):
            with self.assertRaisesRegex(factory.Fail, key):
                self.load(text)


class NewTests(unittest.TestCase):
    ARGS = ["new", "demo-task", "--brief", "do it", "--dispatcher", "d1"]
    OK = {("claude", "--bg"): (0, "backgrounded · 96dc4869 · demo-task\n", "")}

    def answers(self, entries, sessions=(), *extras, dispatcher="d1"):
        return repo_answers(entries, [dict(DISPATCHER, name=dispatcher), *sessions], *extras)

    def test_launch_argv(self):
        with mock.patch.object(factory.time, "time", return_value=1794610677):
            code, out, err, fake = invoke(self.ARGS, self.answers([], (), self.OK))
        self.assertEqual(code, 0, err)
        self.assertIn("96dc4869", out)
        self.assertIn("demo-task", out)
        plugin = str(BIN.resolve().parent.parent)
        lead = (BIN.resolve().parent.parent / "roles" / "lead.md").read_text()
        want = ["claude", "--bg", "--worktree", "demo-task", "-n", "demo-task", "--model", "fable",
                "--permission-mode", "auto", "--plugin-dir", plugin, "--append-system-prompt", lead,
                "Your task slug is demo-task. The dispatcher's session name is d1. "
                "Your base branch is main (remote origin). You started at 2026-11-13T22:57:57Z. "
                "Your time limit is 2 hours. Your brief follows.\n\ndo it"]
        self.assertEqual(fake.calls[-1], want)

    def test_launch_argv_uses_settings(self):
        settings = '{"lead_model": "opus", "lead_permission_mode": "plan"}'
        _, _, _, fake = invoke(self.ARGS, self.answers([], (), self.OK), settings)
        argv = fake.calls[-1]
        self.assertEqual(argv[argv.index("--model") + 1], "opus")
        self.assertEqual(argv[argv.index("--permission-mode") + 1], "plan")

    def test_brief_file(self):
        with tempfile.NamedTemporaryFile("w", suffix=".md") as f:
            f.write("from file")
            f.flush()
            args = ["new", "demo-task", "--brief-file", f.name, "--dispatcher", "d1"]
            code, _, _, fake = invoke(args, self.answers([], (), self.OK))
        self.assertEqual(code, 0)
        self.assertTrue(fake.calls[-1][-1].endswith("\n\nfrom file"))

    def test_dispatcher_defaults_to_the_repo_directory_name(self):
        args = ["new", "demo-task", "--brief", "x"]
        code, _, err, fake = invoke(args, self.answers([], (), self.OK, dispatcher="dispatcher-repo"))
        self.assertEqual(code, 0, err)
        self.assertIn("The dispatcher's session name is dispatcher-repo.", fake.calls[-1][-1])

    def test_precondition_errors_exit_2_and_start_nothing(self):
        cases = {
            "bad slug": (["new", "Bad", "--brief", "x", "--dispatcher", "d1"], self.answers([], (), self.OK), "invalid slug"),
            "agent prefix": (["new", "agent-abc", "--brief", "x", "--dispatcher", "d1"], self.answers([], (), self.OK), "agent-"),
            "empty brief": (["new", "demo-task", "--brief", "  ", "--dispatcher", "d1"], self.answers([], (), self.OK), "empty"),
            "worktree exists": (self.ARGS, self.answers([task_entry("demo-task")], (), self.OK), "worktree already exists"),
            "branch exists": (self.ARGS, self.answers([], (), self.OK, {("git", "rev-parse"): (0, "sha\n", "")}), "branch already exists"),
            "session exists": (self.ARGS, self.answers([], [{"name": "demo-task", "cwd": "/elsewhere"}], self.OK), "session named"),
            "session inside the worktree": (self.ARGS, self.answers([], [{"cwd": f"{WT}/demo-task/sub"}], self.OK), "session named"),
            "cap": (self.ARGS, self.answers([task_entry(s) for s in "abcde"], (), self.OK), "cap of 5.*a, b, c, d, e"),
            "no repo": (self.ARGS, {("git",): (128, "", "not a repo")}, "not inside a git repository"),
            "agents unlistable": (self.ARGS, self.answers([], (), self.OK, {("claude", "agents"): (1, "", "boom")}), "cannot list sessions"),
            "wrong dispatcher name": (self.ARGS, self.answers([], (), self.OK, dispatcher="other"), "/rename d1"),
            "unnamed dispatcher": (self.ARGS, repo_answers([], [{"sessionId": "sid-d"}], self.OK), "/rename d1"),
            "caller not listed": (self.ARGS, repo_answers([], [], self.OK), "not listed"),
        }
        for name, (argv, answers, pattern) in cases.items():
            with self.subTest(name):
                code, _, err, fake = invoke(argv, answers)
                self.assertEqual(code, 2, err)
                self.assertRegex(err, pattern)
                self.assertEqual(len(err.strip().splitlines()), 1)
                self.assertFalse(fake.ran("claude", "--bg"))

    def test_dispatcher_session_id_unset_is_refused(self):
        for value in ("", "sid-other"):
            with self.subTest(value):
                code, _, err, fake = invoke(self.ARGS, self.answers([], (), self.OK),
                                            env={"CLAUDE_CODE_SESSION_ID": value})
                self.assertEqual(code, 2)
                self.assertRegex(err, "CLAUDE_CODE_SESSION_ID|not listed")
                self.assertFalse(fake.ran("claude", "--bg"))

    def stacked(self, extra, base_entry=None):
        answers = self.answers([base_entry or task_entry("alpha")], (), {
            ("git", "worktree", "add"): (0, "", ""), ("git", "config"): (0, "", ""),
            ("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", "")}, extra)
        return invoke(self.ARGS + ["--base", "alpha"], answers)

    def test_base_creates_worktree_records_base_and_launches(self):
        code, _, err, fake = self.stacked(self.OK)
        self.assertEqual(code, 0, err)
        add = ["git", "worktree", "add", "-b", "worktree-demo-task", f"{WT}/demo-task", "worktree-alpha"]
        cfg = ["git", "config", "branch.worktree-demo-task.factoryBase", "worktree-alpha"]
        self.assertLess(fake.calls.index(add), fake.calls.index(cfg))
        self.assertLess(fake.calls.index(cfg), len(fake.calls) - 1)
        launch = fake.calls[-1]
        self.assertEqual(launch[:6], ["claude", "--bg", "--worktree", "demo-task", "-n", "demo-task"])
        self.assertIn("Your base branch is worktree-alpha (remote origin).", launch[-1])
        self.assertFalse(fake.ran("git", "worktree", "remove"))

    def test_base_uses_the_base_tasks_actual_branch(self):
        code, _, err, fake = self.stacked(self.OK, (f"{WT}/alpha", "feature/odd", False))
        self.assertEqual(code, 0, err)
        self.assertIn(["git", "worktree", "add", "-b", "worktree-demo-task", f"{WT}/demo-task", "feature/odd"],
                      fake.calls)
        self.assertIn(["git", "config", "branch.worktree-demo-task.factoryBase", "feature/odd"], fake.calls)
        self.assertIn("Your base branch is feature/odd (remote origin).", fake.calls[-1][-1])

    def test_detached_base_refused(self):
        code, _, err, fake = self.stacked(self.OK, (f"{WT}/alpha", None, False))
        self.assertEqual(code, 2)
        self.assertIn("detached", err)
        self.assertFalse(fake.ran("git", "worktree", "add"))

    def test_unknown_base_refused_before_anything_is_created(self):
        code, _, err, fake = invoke(self.ARGS + ["--base", "ghost"], self.answers([task_entry("alpha")]))
        self.assertEqual(code, 2)
        self.assertIn("ghost", err)
        self.assertFalse(fake.ran("git", "worktree", "add"))
        self.assertFalse(fake.ran("claude", "--bg"))

    def test_launch_exit_nonzero_with_base_removes_worktree_and_branch(self):
        code, _, err, fake = self.stacked({("claude", "--bg"): (1, "", "boom")})
        self.assertEqual(code, 1)
        self.assertIn("boom", err)
        self.assertIn(["git", "worktree", "remove", "--force", f"{WT}/demo-task"], fake.calls)
        self.assertIn(["git", "branch", "-D", "worktree-demo-task"], fake.calls)

    def test_launch_exit_zero_without_line_removes_nothing(self):
        code, _, err, fake = self.stacked({("claude", "--bg"): (0, "something odd\n", "")})
        self.assertEqual(code, 1)
        self.assertIn("something odd", err)
        self.assertFalse(fake.ran("git", "worktree", "remove"))
        self.assertFalse(fake.ran("git", "branch"))

    def test_config_failure_removes_worktree_and_branch_and_exits_1(self):
        code, _, err, fake = self.stacked({("git", "config"): (1, "", "cannot lock")})
        self.assertEqual(code, 1)
        self.assertIn("cannot lock", err)
        self.assertIn(["git", "worktree", "remove", "--force", f"{WT}/demo-task"], fake.calls)
        self.assertIn(["git", "branch", "-D", "worktree-demo-task"], fake.calls)
        self.assertFalse(fake.ran("claude", "--bg"))

    def test_worktree_add_failure_exits_1(self):
        code, _, err, fake = self.stacked({("git", "worktree", "add"): (128, "", "bad ref")})
        self.assertEqual((code, "bad ref" in err), (1, True))
        self.assertFalse(fake.ran("claude", "--bg"))

    def test_launch_failure_without_base_creates_nothing_to_undo(self):
        code, _, _, fake = invoke(self.ARGS, self.answers([], (), {("claude", "--bg"): (1, "", "boom")}))
        self.assertEqual(code, 1)
        self.assertFalse(fake.ran("git", "worktree", "remove"))

    def test_cap_from_settings(self):
        code, _, err, _ = invoke(self.ARGS, self.answers([task_entry("a")], (), self.OK), '{"worktree_cap": 1}')
        self.assertEqual(code, 2)
        self.assertIn("cap of 1", err)

    def test_invalid_settings_exit_2(self):
        code, _, err, _ = invoke(self.ARGS, self.answers([]), '{"worktree_cap": 0}')
        self.assertEqual((code, "worktree_cap" in err), (2, True))
        code, _, err, _ = invoke(self.ARGS, self.answers([]), "not json")
        self.assertEqual((code, "settings.json" in err), (2, True))

    def test_claude_failure_passes_stderr(self):
        for result in ((1, "", "model not found\n"), (0, "no marker line\n", "")):
            with self.subTest(result=result):
                code, _, err, _ = invoke(self.ARGS, self.answers([], (), {("claude", "--bg"): result}))
                self.assertEqual(code, 1)
                self.assertRegex(err, "did not start|without a `backgrounded` line")
        _, _, err, _ = invoke(self.ARGS, self.answers([], (), {("claude", "--bg"): (1, "", "model not found\n")}))
        self.assertIn("model not found", err)

    def test_no_remote_defaults_to_main(self):
        answers = self.answers([], (), self.OK)
        answers[("git", "symbolic-ref")] = (128, "", "no such ref")
        self.assertEqual(invoke(self.ARGS, answers)[0], 0)


def git_task_answers(slug, now, *, dirty="", counts="0\t2", commit_age=0, pr=None):
    return {
        ("git", "status"): (0, dirty, ""),
        ("git", "rev-list"): (0, counts + "\n", ""),
        ("git", "log"): (0, f"{int(now - commit_age)} sha-{slug}\n", ""),
        ("gh",): (0, json.dumps(pr if pr is not None else []), "") if pr != "missing" else (127, "", "no gh"),
    }


class StatusTests(unittest.TestCase):
    def status(self, entries, sessions=(), as_json=True, **kw):
        now = time.time()
        answers = repo_answers(entries, sessions, git_task_answers("x", now, **kw))
        code, out, err, fake = invoke(["status", "--json"] if as_json else ["status"], answers)
        self.assertEqual(code, 0, err)
        return (json.loads(out) if as_json else out), fake

    def test_rows_counts_and_exclusions(self):
        entries = [task_entry("alpha"), (f"{WT}/agent-1a2b", "worktree-agent-1a2b", False),
                   ("/elsewhere/wt", "feature", False), (f"{WT}/gone", "worktree-gone", True)]
        session = {"id": "96dc4869", "cwd": f"{WT}/alpha", "kind": "background", "name": "alpha",
                   "status": "idle", "state": "done", "startedAt": int(time.time() * 1000)}
        other = {"cwd": ROOT, "kind": "interactive", "name": "d1", "status": "busy"}
        doc, _ = self.status(entries, [other, session], counts="3\t1", dirty=" M f\n",
                             pr=[{"state": "OPEN", "url": "http://pr/1", "headRefOid": "z"}])
        self.assertEqual((doc["cap"], doc["open"], doc["leftover_agent_worktrees"], doc["outside_worktrees"]),
                         (5, 1, 1, 1))
        (row,) = doc["rows"]
        self.assertEqual((row["slug"], row["branch"], row["session"], row["session_id"]),
                         ("alpha", "worktree-alpha", "done", "96dc4869"))
        self.assertEqual((row["dirty"], row["behind"], row["ahead"]), (True, 3, 1))
        self.assertEqual((row["pr_state"], row["pr_url"]), ("open", "http://pr/1"))
        self.assertFalse(row["stale"])

    def test_session_matching_is_by_worktree_path_prefix(self):
        inside = {"id": "in1", "cwd": f"{WT}/alpha/sub/dir", "kind": "background", "status": "idle", "state": "done"}
        sibling = {"id": "sib", "cwd": f"{WT}/alpha-two", "kind": "background", "status": "idle", "state": "done"}
        no_cwd = {"id": "nocwd", "kind": "background", "status": "idle", "state": "done"}
        empty_cwd = {"id": "empty", "cwd": "", "kind": "background", "status": "idle", "state": "done"}
        doc, _ = self.status([task_entry("alpha")], [sibling, no_cwd, empty_cwd])
        self.assertEqual((doc["rows"][0]["session"], doc["rows"][0]["session_id"]), ("none", None))
        doc, _ = self.status([task_entry("alpha")], [sibling, no_cwd, inside])
        self.assertEqual(doc["rows"][0]["session_id"], "in1")

    def test_table_shows_the_session_id(self):
        s = {"id": "96dc4869", "cwd": f"{WT}/alpha", "kind": "background", "status": "idle", "state": "done"}
        out, _ = self.status([task_entry("alpha")], [s], as_json=False)
        lines = out.splitlines()
        self.assertEqual(lines[1].index("96dc4869"), lines[0].index("ID"))

    def test_no_session_and_no_pr(self):
        doc, fake = self.status([task_entry("alpha")])
        row = doc["rows"][0]
        self.assertEqual((row["session"], row["pr_state"], row["dirty"]), ("none", "none", False))
        self.assertIn(["git", "rev-list", "--left-right", "--count", "origin/main...HEAD"], fake.calls)

    def test_missing_gh_is_unknown(self):
        doc, _ = self.status([task_entry("alpha")], pr="missing")
        self.assertEqual(doc["rows"][0]["pr_state"], "unknown")

    def test_sessions_unlistable(self):
        answers = repo_answers([task_entry("alpha")], (), {("claude", "agents"): (127, "", "no claude")},
                               git_task_answers("x", time.time()))
        _, out, _, _ = invoke(["status", "--json"], answers)
        self.assertEqual(json.loads(out)["rows"][0]["session"], "unknown")

    def test_no_remote_compares_against_main(self):
        answers = repo_answers([task_entry("alpha")], (), git_task_answers("x", time.time()))
        answers[("git", "symbolic-ref")] = (128, "", "")
        _, _, _, fake = invoke(["status"], answers)
        self.assertIn(["git", "rev-list", "--left-right", "--count", "main...HEAD"], fake.calls)

    def test_stale_rule(self):
        now_ms = int(time.time() * 1000)
        old_session = {"cwd": f"{WT}/alpha", "kind": "background", "status": "idle", "startedAt": now_ms - 8 * DAY * 1000}
        new_session = dict(old_session, startedAt=now_ms - DAY * 1000)
        cases = [
            ("old commit, no session", 8 * DAY, [], True),
            ("old commit, old session", 8 * DAY, [old_session], True),
            ("old commit, fresh session", 8 * DAY, [new_session], False),
            ("fresh commit, old session", DAY, [old_session], False),
            ("fresh commit, no session", DAY, [], False),
        ]
        for name, commit_age, sessions, stale in cases:
            with self.subTest(name):
                doc, _ = self.status([task_entry("alpha")], sessions, commit_age=commit_age)
                self.assertEqual(doc["rows"][0]["stale"], stale)

    def test_recorded_base_and_ahead_behind_against_it(self):
        answers = repo_answers([task_entry("alpha")], (), git_task_answers("x", time.time()),
                               {("git", "config"): (0, "worktree-other\n", "")})
        _, out, _, fake = invoke(["status", "--json"], answers)
        self.assertEqual(json.loads(out)["rows"][0]["base"], "worktree-other")
        self.assertIn(["git", "rev-list", "--left-right", "--count", "worktree-other...HEAD"], fake.calls)

    def test_default_base_when_none_recorded(self):
        doc, _ = self.status([task_entry("alpha")])
        self.assertEqual(doc["rows"][0]["base"], "origin/main")

    def test_elapsed_and_overtime_on_both_sides_of_the_limit(self):
        now_ms = int(time.time() * 1000)
        H = 3600 * 1000

        def session(hours, **kw):
            return dict({"cwd": f"{WT}/alpha", "kind": "background", "status": "busy", "state": "working",
                         "startedAt": now_ms - int(hours * H)}, **kw)
        cases = [
            ("working past the limit", session(2.5), True),
            ("working within the limit", session(1.5), False),
            ("state working, status idle", session(2.5, status="idle"), True),
            ("done past the limit", session(2.5, status="idle", state="done"), False),
        ]
        for name, s, overtime in cases:
            with self.subTest(name):
                doc, _ = self.status([task_entry("alpha")], [s])
                row = doc["rows"][0]
                self.assertEqual(row["overtime"], overtime)
                self.assertAlmostEqual(row["elapsed"], (now_ms - s["startedAt"]) / 1000, delta=5)
        doc, _ = self.status([task_entry("alpha")])
        self.assertEqual((doc["rows"][0]["elapsed"], doc["rows"][0]["overtime"]), (None, False))

    def test_overtime_follows_the_setting(self):
        s = {"cwd": f"{WT}/alpha", "kind": "background", "status": "busy", "state": "working",
             "startedAt": int(time.time() * 1000) - 90 * 60 * 1000}
        answers = repo_answers([task_entry("alpha")], [s], git_task_answers("x", time.time()))
        _, out, _, _ = invoke(["status", "--json"], answers, '{"task_time_limit_hours": 1}')
        self.assertTrue(json.loads(out)["rows"][0]["overtime"])

    def test_table_output(self):
        entries = [task_entry("alpha"), (f"{WT}/agent-1a2b", "b", False), ("/elsewhere", "c", False)]
        out, _ = self.status(entries, as_json=False)
        lines = out.splitlines()
        self.assertTrue(lines[0].startswith("SLUG"))
        self.assertTrue(lines[1].startswith("alpha"))
        self.assertEqual(lines[1].index("worktree-alpha"), lines[0].index("BRANCH"))
        self.assertEqual(lines[1].index("origin/main"), lines[0].index("BASE"))
        self.assertIn("open 1/5; leftover agent worktrees 1; worktrees outside .claude/worktrees 1", lines[-1])

    def test_empty_table(self):
        out, _ = self.status([], as_json=False)
        self.assertEqual(out.strip(), "open 0/5; leftover agent worktrees 0; worktrees outside .claude/worktrees 0")


class CleanTests(unittest.TestCase):
    HEAD = "sha-x"

    def clean(self, *, dirty="", pr=None, sessions=(), dry=False, extra=None, entries=None):
        if pr is None:
            pr = [{"state": "MERGED", "url": "u", "headRefOid": self.HEAD, "baseRefName": "main"}]
        answers = repo_answers(entries or [task_entry("x")], list(sessions),
                               git_task_answers("x", time.time(), dirty=dirty, pr=pr))
        answers.update({("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", ""),
                        ("claude", "rm"): (0, "", "")})
        answers.update(extra or {})
        return invoke(["clean"] + (["--dry-run"] if dry else []), answers)

    @staticmethod
    def removed_anything(fake):
        return any(c[:2] in (["git", "branch"], ["claude", "rm"]) or c[:3] == ["git", "worktree", "remove"]
                   for c in fake.calls)

    def pr(self, state="MERGED", base="main", head=HEAD):
        return [{"state": state, "url": "u", "headRefOid": head, "baseRefName": base}]

    def test_keep_reasons(self):
        def bg(**kw):
            return dict({"id": "ab", "cwd": f"{WT}/x", "kind": "background", "status": "idle", "state": "done"}, **kw)
        cases = {
            "uncommitted": (dict(dirty=" M a\n"), "uncommitted changes"),
            "unreadable status": (dict(extra={("git", "status"): (128, "", "bad")}), "cannot read git status"),
            "detached head": (dict(entries=[(f"{WT}/x", None, False)]), "detached HEAD"),
            "fresh task, no pr": (dict(pr=[]), "no merged pull request \\(none\\)"),
            "open pr": (dict(pr=self.pr("OPEN")), "no merged pull request \\(open\\)"),
            "closed pr": (dict(pr=self.pr("CLOSED")), "no merged pull request \\(closed\\)"),
            "gh missing": (dict(pr="missing"), "pull request state is unknown"),
            "merged into another task's branch": (dict(pr=self.pr(base="worktree-y")), "merged into worktree-y, not main"),
            "merged, base missing": (dict(pr=self.pr(base="")), "unknown branch"),
            "newer commits": (dict(pr=self.pr(head="other")), "newer than its merged pull request"),
            "empty head": (dict(pr=self.pr(head=""), extra={("git", "log"): (0, "", "")}), "cannot compare its head"),
            "empty head on both sides": (dict(pr=self.pr(head=""), extra={("git", "log"): (1, "", "")}),
                                         "cannot compare its head"),
            "busy session": (dict(sessions=[bg(status="busy", state="working")]), "busy or waiting"),
            "waiting session": (dict(sessions=[bg(status="waiting")]), "busy or waiting"),
            "interactive session": (dict(sessions=[{"sessionId": "i1", "cwd": f"{WT}/x", "kind": "interactive"}]),
                                    "interactive session"),
            "interactive session in a subdirectory": (
                dict(sessions=[{"sessionId": "i1", "cwd": f"{WT}/x/src/deep", "kind": "interactive"}]),
                "interactive session"),
        }
        for name, (kw, pattern) in cases.items():
            with self.subTest(name):
                code, out, _, fake = self.clean(**kw)
                self.assertEqual(code, 0)
                self.assertRegex(out, f"^kept x: .*{pattern}")
                self.assertFalse(self.removed_anything(fake))

    def test_sessions_that_do_not_belong_to_the_task_do_not_protect_it(self):
        elsewhere = [{"sessionId": "i1", "cwd": f"{WT}/x-other", "kind": "interactive"},
                     {"sessionId": "i2", "cwd": ROOT, "kind": "interactive"},
                     {"sessionId": "i3", "kind": "interactive"},
                     {"sessionId": "i4", "cwd": "", "kind": "interactive"}]
        code, out, _, _ = self.clean(sessions=elsewhere)
        self.assertEqual(out.strip(), "removed x")

    def test_sessions_unlistable_removes_nothing(self):
        for name, result in (("missing claude", (127, "", "no claude")), ("bad json", (0, "not json", "")),
                             ("not a list", (0, "{}", "")), ("nonzero exit", (1, "[]", "boom"))):
            with self.subTest(name):
                for dry in (False, True):
                    code, out, err, fake = self.clean(dry=dry, extra={("claude", "agents"): result})
                    self.assertEqual(code, 2)
                    self.assertIn("cannot list sessions", err)
                    self.assertEqual(out, "")
                    self.assertFalse(self.removed_anything(fake))

    def test_kept_when_another_task_is_stacked_on_it(self):
        answers = repo_answers([task_entry("x"), task_entry("y")], (),
                               git_task_answers("x", time.time(), pr=self.pr()),
                               {("git", "config", "--get", "branch.worktree-y.factoryBase"): (0, "worktree-x\n", "")})
        code, out, _, fake = invoke(["clean"], answers)
        self.assertIn("kept x: y is stacked on it", out)
        self.assertFalse(fake.ran("git", "worktree", "remove", f"{WT}/x"))

    def test_remove_without_session_uses_git(self):
        code, out, _, fake = self.clean()
        self.assertEqual((code, out.strip()), (0, "removed x"))
        self.assertIn(["git", "worktree", "remove", f"{WT}/x"], fake.calls)
        self.assertIn(["git", "branch", "-d", "worktree-x"], fake.calls)
        self.assertNotIn("--force", [a for c in fake.calls for a in c])
        self.assertFalse(fake.ran("claude", "rm"))

    def test_remove_with_session_uses_claude_rm(self):
        idle = {"id": "ab12", "cwd": f"{WT}/x", "kind": "background", "status": "idle", "state": "done"}
        code, out, _, fake = self.clean(sessions=[idle])
        self.assertEqual((code, out.strip()), (0, "removed x"))
        self.assertIn(["claude", "rm", "ab12"], fake.calls)
        self.assertFalse(fake.ran("git", "worktree", "remove"))
        self.assertFalse(fake.ran("git", "branch"))

    def test_dry_run_changes_nothing(self):
        code, out, _, fake = self.clean(dry=True)
        self.assertEqual(out.strip(), "would remove x")
        self.assertFalse(self.removed_anything(fake))

    def test_failed_removal_exits_1_and_says_why(self):
        code, out, err, _ = self.clean(extra={("git", "worktree", "remove"): (1, "", "locked")})
        self.assertEqual(code, 1)
        self.assertIn("locked", err)
        self.assertNotIn("removed", out)

    def test_failed_branch_delete_names_the_branch(self):
        code, out, err, fake = self.clean(extra={("git", "branch"): (1, "", "not fully merged")})
        self.assertEqual(code, 1)
        self.assertRegex(err, "branch worktree-x")
        self.assertIn("not fully merged", err)
        self.assertIn(["git", "worktree", "remove", f"{WT}/x"], fake.calls)


class EndToEndTests(unittest.TestCase):
    FAKE_CLAUDE = """#!/usr/bin/env python3
import json, os, sys
if sys.argv[1:2] == ["agents"]:
    print(os.environ["FAKE_AGENTS"])
elif sys.argv[1:2] == ["--bg"]:
    json.dump(sys.argv, open(os.environ["FAKE_ARGV"], "w"))
    print("backgrounded · 96dc4869 · " + sys.argv[sys.argv.index("-n") + 1])
else:
    sys.exit(3)
"""
    FAKE_GH = """#!/usr/bin/env python3
import os
print(os.environ["FAKE_PR"])
"""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        tmp = Path(self.tmp.name).resolve()
        self.repo, self.home, bindir = tmp / "repo", tmp / "home", tmp / "bin"
        for d in (self.repo, self.home, bindir):
            d.mkdir()
        for name, text in (("claude", self.FAKE_CLAUDE), ("gh", self.FAKE_GH)):
            tool = bindir / name
            tool.write_text(text)
            tool.chmod(tool.stat().st_mode | stat.S_IXUSR)
        self.argv_file = tmp / "argv.json"
        self.dispatcher = {"sessionId": "sid-d", "name": "dispatcher-repo", "kind": "interactive",
                           "cwd": str(self.repo)}
        self.env = {**os.environ, "HOME": str(self.home), "PATH": f"{bindir}{os.pathsep}{os.environ['PATH']}",
                    "FAKE_ARGV": str(self.argv_file), "FAKE_AGENTS": json.dumps([self.dispatcher]),
                    "FAKE_PR": "[]", "CLAUDE_CODE_SESSION_ID": "sid-d", "GIT_AUTHOR_NAME": "t",
                    "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}
        self.git("init", "-q", "-b", "main")
        self.git("commit", "-q", "--allow-empty", "-m", "init")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env, check=True, capture_output=True, text=True).stdout

    def factory(self, *args, env=None):
        return subprocess.run([sys.executable, str(BIN), *args], cwd=self.repo, env={**self.env, **(env or {})},
                              capture_output=True, text=True)

    def test_new_status_clean(self):
        p = self.factory("new", "demo-task", "--brief", "build it")
        self.assertEqual((p.returncode, p.stderr), (0, ""))
        self.assertIn("96dc4869", p.stdout)
        argv = json.loads(self.argv_file.read_text())
        self.assertEqual(argv[1:6], ["--bg", "--worktree", "demo-task", "-n", "demo-task"])
        self.assertIn("build it", argv[-1])
        self.assertIn("dispatcher-repo", argv[-1])

        # the fake does not make worktrees; create the one claude would have made
        self.git("worktree", "add", "-q", "-b", "worktree-demo-task", ".claude/worktrees/demo-task")
        task_dir = self.repo / ".claude/worktrees/demo-task"
        session = {"id": "96dc4869", "cwd": str(task_dir), "kind": "background", "name": "demo-task",
                   "status": "idle", "state": "done", "startedAt": int(time.time() * 1000)}
        agents = json.dumps([self.dispatcher, session])
        p = self.factory("status", "--json", env={"FAKE_AGENTS": agents})
        self.assertEqual(p.returncode, 0, p.stderr)
        doc = json.loads(p.stdout)
        (row,) = doc["rows"]
        self.assertEqual((row["slug"], row["session"], row["session_id"], row["ahead"], row["behind"]),
                         ("demo-task", "done", "96dc4869", 0, 0))
        self.assertEqual(row["pr_state"], "none")
        self.assertEqual((doc["open"], doc["leftover_agent_worktrees"], doc["outside_worktrees"]), (1, 0, 0))

        p = self.factory("new", "demo-task", "--brief", "again", env={"FAKE_AGENTS": agents})
        self.assertEqual(p.returncode, 2)

        # a fresh task with no pull request is kept, even though its tip sits on main
        p = self.factory("clean", env={"FAKE_AGENTS": agents})
        self.assertEqual((p.returncode, p.stdout.split(":")[0]), (0, "kept demo-task"))
        self.assertTrue(task_dir.is_dir())

        # sessions that cannot be listed: nothing removed
        p = self.factory("clean", env={"FAKE_AGENTS": "not json"})
        self.assertEqual(p.returncode, 2)
        self.assertTrue(task_dir.is_dir())

        # merged pull request at the same head: --dry-run changes nothing, a real clean removes it
        head = self.git("-C", str(task_dir), "rev-parse", "HEAD").strip()
        merged = json.dumps([{"state": "MERGED", "url": "u", "headRefOid": head, "baseRefName": "main"}])
        no_session = {"FAKE_AGENTS": json.dumps([self.dispatcher]), "FAKE_PR": merged}
        p = self.factory("clean", "--dry-run", env=no_session)
        self.assertEqual(p.stdout.strip(), "would remove demo-task")
        self.assertTrue(task_dir.is_dir())
        p = self.factory("clean", env=no_session)
        self.assertEqual((p.returncode, p.stdout.strip(), p.stderr), (0, "removed demo-task", ""))
        self.assertFalse(task_dir.exists())
        self.assertEqual(self.git("branch", "--list", "worktree-demo-task"), "")


if __name__ == "__main__":
    unittest.main()
