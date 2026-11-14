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
    """entries: (path, branch or None, prunable[, lock reason; "" is a lock without one])."""
    out = []
    for path, branch, prunable, *lock in entries:
        out += [f"worktree {path}", "HEAD abc", f"branch refs/heads/{branch}" if branch else "detached"]
        out += [f"locked {reason}".rstrip() for reason in lock]
        if prunable:
            out.append("prunable gitdir file points to non-existent location")
        out.append("")
    return "\n".join(out)


def task_entry(slug):
    return (f"{WT}/{slug}", f"worktree-{slug}", False)


class FakeRun:
    """Canned runner. `answers` maps an argv prefix (tuple) to (rc, out, err) or a list of them; longest prefix wins."""

    def __init__(self, answers):
        self.answers = answers
        self.calls = []

    def __call__(self, argv, cwd=None):
        self.calls.append(list(argv))
        best = max((p for p in self.answers if tuple(argv[:len(p)]) == p), key=len, default=None)
        if best is None:
            return 1, "", "unexpected call"
        result = self.answers[best]
        if isinstance(result, list):  # successive answers; the last one repeats
            return result.pop(0) if len(result) > 1 else result[0]
        return result

    def ran(self, *prefix):
        return any(c[:len(prefix)] == list(prefix) for c in self.calls)


DISPATCHER = {"sessionId": "sid-d", "name": "dispatcher-repo", "cwd": ROOT, "kind": "interactive"}
# The lead of task `x` after its process has exited, as `claude agents --json --all` lists it.
EXITED = {"id": "ex1", "sessionId": "sid-x", "name": "x", "kind": "background", "state": "done", "cwd": ROOT,
          "startedAt": 1}


def task_rec(slug, state, deliverable=None, repo=ROOT):
    return {"repo": repo, "slug": slug, "state": state, **({"deliverable": deliverable} if deliverable else {})}


def invoke(argv, answers, settings=None, env=None, tasks=(), cwd=None):
    """`tasks` seeds tasks.jsonl (records, or raw lines); the records written are returned as `fake.tasks`."""
    fake = FakeRun(answers)
    out, err = io.StringIO(), io.StringIO()
    environ = {"HOME": "", "CLAUDE_CODE_SESSION_ID": "sid-d", **(env or {})}
    with tempfile.TemporaryDirectory() as home, mock.patch.dict(os.environ, {**environ, "HOME": home}):
        store = Path(home, ".claude", "factory")
        store.mkdir(parents=True)
        if settings is not None:
            (store / "settings.json").write_text(settings)
        (store / "tasks.jsonl").write_text("".join((t if isinstance(t, str) else json.dumps(t)) + "\n" for t in tasks))
        seeded = (store / "tasks.jsonl").read_text().splitlines()
        with mock.patch.object(factory, "run", fake), redirect_stdout(out), redirect_stderr(err), \
                mock.patch.object(os, "getcwd", return_value=cwd or ROOT):
            code = factory.main(argv)
        fake.tasks = [json.loads(line) for line in (store / "tasks.jsonl").read_text().splitlines()[len(seeded):]]
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


BUSY = [{"id": "s1", "cwd": f"{WT}/x", "kind": "background", "status": "busy", "state": "working"}]


class PhaseTests(unittest.TestCase):
    def test_every_row_of_the_table(self):
        want = {  # state -> pull request state -> phase, with no session unfinished
            None: dict(none="motion", closed="motion", unknown="motion", open="review", merged="merged"),
            "working": dict(none="motion", closed="motion", unknown="motion", open="motion", merged="motion"),
            "blocked": dict(none="blocked", closed="blocked", unknown="blocked", open="blocked", merged="blocked"),
            "handed-over": dict(none="review", closed="review", unknown="review", open="review", merged="merged"),
            "acknowledged": dict(none="settled", closed="settled", unknown="settled", open="settled", merged="merged"),
            "dismissed": dict(none="settled", closed="settled", unknown="settled", open="settled", merged="merged"),
        }
        for state, by_pr in want.items():
            for pr, expected in by_pr.items():
                with self.subTest(state=state, pr=pr):
                    self.assertEqual(factory.phase(pr, [], state), expected)

    def test_an_unfinished_or_unplaced_session_is_always_motion(self):
        for state in (None, *factory.STATES):
            for pr in ("none", "open", "merged"):
                for mine in (BUSY, [{"kind": "interactive"}], None):
                    with self.subTest(state=state, pr=pr, mine=mine):
                        self.assertEqual(factory.phase(pr, mine, state), "motion")

    def test_a_finished_session_lets_go(self):
        done = [{"kind": "background", "status": "idle", "state": "done"}]
        self.assertEqual(factory.phase("open", done), "review")
        self.assertEqual(factory.phase("none", done, "handed-over"), "review")

    def test_state_defaults_to_undeclared(self):
        self.assertEqual(factory.phase("open", []), "review")
        self.assertEqual(factory.phase("none", []), "motion")


class StoreTests(unittest.TestCase):
    def read(self, lines=None):
        with tempfile.TemporaryDirectory() as home, mock.patch.dict(os.environ, {"HOME": home}):
            if lines is not None:
                Path(home, ".claude", "factory").mkdir(parents=True)
                Path(home, ".claude", "factory", "tasks.jsonl").write_text("\n".join(lines) + "\n")
            return {slug: r["state"] for slug, r in factory.read_tasks(ROOT).items()}

    def test_last_record_wins(self):
        lines = [json.dumps(task_rec("a", "working")), json.dumps(task_rec("a", "blocked")),
                 json.dumps(task_rec("b", "working"))]
        self.assertEqual(self.read(lines), {"a": "blocked", "b": "working"})

    def test_other_repos_are_ignored(self):
        lines = [json.dumps(task_rec("a", "working")), json.dumps(task_rec("a", "blocked", repo="/other"))]
        self.assertEqual(self.read(lines), {"a": "working"})

    def test_garbage_lines_are_skipped(self):
        lines = [json.dumps(task_rec("a", "working")), "not json", "[1]", '{"repo": "/repo"}', ""]
        self.assertEqual(self.read(lines), {"a": "working"})

    def test_unknown_last_state_is_undeclared(self):
        lines = [json.dumps(task_rec("a", "working")), json.dumps(task_rec("a", "nonsense")),
                 json.dumps(task_rec("b", None))]
        self.assertEqual(self.read(lines), {})

    def test_missing_file(self):
        self.assertEqual(self.read(), {})


class StateTests(unittest.TestCase):
    CWD = f"{WT}/x/sub"

    def state(self, *args, cwd=CWD, tasks=()):
        return invoke(["state", *args], repo_answers([task_entry("x")]), cwd=cwd, tasks=tasks)

    def test_each_state_is_written_with_the_repo_root_and_slug(self):
        for state in ("working", "blocked"):
            with self.subTest(state):
                code, out, err, fake = self.state(state)
                self.assertEqual((code, out, err), (0, f"x: {state}\n", ""))
                (rec,) = fake.tasks
                self.assertEqual((rec["repo"], rec["slug"], rec["state"], rec["role"], rec["model"]),
                                 (ROOT, "x", state, "lead", None))
                self.assertEqual(rec["session"], "sid-d")
                self.assertNotIn("deliverable", rec)

    def test_handed_over_stores_a_link_as_given(self):
        code, out, _, fake = self.state("handed-over", "https://github.com/o/r/pull/9")
        self.assertEqual((code, out), (0, "x: handed-over\n"))
        self.assertEqual(fake.tasks[0]["deliverable"], "https://github.com/o/r/pull/9")

    def test_handed_over_resolves_a_file(self):
        with tempfile.NamedTemporaryFile() as f:
            code, _, err, fake = self.state("handed-over", f.name)
            self.assertEqual(code, 0, err)
            self.assertEqual(fake.tasks[0]["deliverable"], os.path.realpath(f.name))

    def test_refusals_write_nothing(self):
        cases = {
            "outside any task worktree": (("working",), ROOT, "inside a task worktree"),
            "the main checkout's subdirectory": (("working",), f"{ROOT}/src", "inside a task worktree"),
            "a sibling of a task": (("working",), f"{WT}/x-other", "inside a task worktree"),
            "handed-over without a deliverable": (("handed-over",), self.CWD, "needs a deliverable"),
            "working with a deliverable": (("working", "https://x/y"), self.CWD, "take none"),
            "blocked with a deliverable": (("blocked", "https://x/y"), self.CWD, "take none"),
            "a missing file": (("handed-over", "/no/such/file"), self.CWD, "does not exist"),
        }
        for name, (args, cwd, pattern) in cases.items():
            with self.subTest(name):
                code, out, err, fake = self.state(*args, cwd=cwd)
                self.assertEqual((code, out, fake.tasks), (2, "", []))
                self.assertRegex(err, pattern)

    def test_from_the_worktree_root_and_a_subdirectory(self):
        for cwd in (f"{WT}/x", self.CWD):
            self.assertEqual(self.state("working", cwd=cwd)[0], 0)


class AckTests(unittest.TestCase):
    def ack(self, slug, state, tasks=()):
        return invoke(["ack", slug, state], repo_answers([task_entry("x")]), tasks=tasks)

    def test_refused_unless_handed_over(self):
        cases = {"unknown slug": ("ghost", [task_rec("ghost", "handed-over")]), "undeclared": ("x", []),
                 "working": ("x", [task_rec("x", "working")]), "blocked": ("x", [task_rec("x", "blocked")]),
                 "another repo's record": ("x", [task_rec("x", "handed-over", repo="/other")])}
        for name, (slug, tasks) in cases.items():
            with self.subTest(name):
                code, out, err, fake = self.ack(slug, "acknowledged", tasks)
                self.assertEqual((code, out, fake.tasks), (2, "", []))
                self.assertEqual(len(err.strip().splitlines()), 1)

    def test_accepted_after_handed_over_and_copies_the_deliverable(self):
        for state in ("acknowledged", "dismissed"):
            with self.subTest(state):
                code, out, err, fake = self.ack("x", state, [task_rec("x", "handed-over", "https://x/y")])
                self.assertEqual((code, out, err), (0, f"x: {state}\n", ""))
                (rec,) = fake.tasks
                self.assertEqual((rec["state"], rec["deliverable"], rec["role"], rec["slug"], rec["repo"]),
                                 (state, "https://x/y", "dispatcher", "x", ROOT))

    def test_a_settled_task_can_be_settled_again_and_working_voids_it(self):
        code, _, _, fake = self.ack("x", "dismissed", [task_rec("x", "acknowledged", "/f")])
        self.assertEqual((code, fake.tasks[0]["deliverable"]), (0, "/f"))
        code, _, err, _ = self.ack("x", "acknowledged", [task_rec("x", "acknowledged", "/f"), task_rec("x", "working")])
        self.assertEqual(code, 2, err)


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
            "second session with the dispatcher name": (
                self.ARGS, self.answers([], [{"sessionId": "sid-2", "name": "d1"}], self.OK), "also named 'd1'"),
            "old merged pull request": (
                self.ARGS, self.answers([], (), self.OK, {("gh",): (0, '[{"state": "MERGED"}]', "")}),
                "merged pull request already exists"),
            "old open pull request": (
                self.ARGS, self.answers([], (), self.OK, {("gh",): (0, '[{"state": "OPEN"}]', "")}),
                "open pull request already exists"),
        }
        for name, (argv, answers, pattern) in cases.items():
            with self.subTest(name):
                code, _, err, fake = invoke(argv, answers)
                self.assertEqual(code, 2, err)
                self.assertRegex(err, pattern)
                self.assertEqual(len(err.strip().splitlines()), 1)
                self.assertFalse(fake.ran("claude", "--bg"))

    def test_unanswerable_pull_request_check_warns_and_proceeds(self):
        for gh in ((127, "", "no gh"), (0, "not json", ""), (0, "[{}]", "")):
            with self.subTest(gh=gh):
                code, _, err, fake = invoke(self.ARGS, self.answers([], (), self.OK, {("gh",): gh}))
                self.assertEqual(code, 0, err)
                self.assertEqual(len(err.strip().splitlines()), 1)
                self.assertRegex(err, "warning: cannot check for an existing pull request on worktree-demo-task")
                self.assertTrue(fake.ran("claude", "--bg"))
        code, _, err, fake = invoke(self.ARGS, self.answers([], (), self.OK, {("gh",): (0, "[]", "")}))
        self.assertEqual((code, err), (0, ""))
        gh = next(c for c in fake.calls if c[0] == "gh")
        self.assertEqual(gh[:6], ["gh", "pr", "list", "--head", "worktree-demo-task", "--state"])
        self.assertEqual(gh[6], "all")

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

    def test_launch_exit_nonzero_with_backgrounded_line_removes_nothing(self):
        code, _, err, fake = self.stacked({("claude", "--bg"): (1, "backgrounded · 96dc4869 · demo-task\n", "late")})
        self.assertEqual(code, 1)
        self.assertIn("exited 1 after printing a `backgrounded` line", err)
        self.assertFalse(fake.ran("git", "worktree", "remove"))
        self.assertFalse(fake.ran("git", "branch"))

    def test_launch_exit_nonzero_with_backgrounded_line_on_stderr_removes_nothing(self):
        code, _, err, fake = self.stacked({("claude", "--bg"): (1, "", "backgrounded · 96dc4869 · demo-task\n")})
        self.assertEqual(code, 1)
        self.assertIn("exited 1 after printing a `backgrounded` line", err)
        self.assertFalse(fake.ran("git", "worktree", "remove"))
        self.assertFalse(fake.ran("git", "branch"))

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

    def test_new_declares_working(self):
        code, _, err, fake = invoke(self.ARGS, self.answers([], (), self.OK))
        self.assertEqual(code, 0, err)
        (rec,) = fake.tasks
        self.assertEqual((rec["repo"], rec["slug"], rec["state"], rec["role"]), (ROOT, "demo-task", "working", "dispatcher"))

    def test_a_failed_launch_declares_nothing(self):
        _, _, _, fake = invoke(self.ARGS, self.answers([], (), {("claude", "--bg"): (1, "", "boom")}))
        self.assertEqual(fake.tasks, [])

    def test_a_reused_slug_starts_clean(self):
        code, _, _, fake = invoke(self.ARGS, self.answers([], (), self.OK), tasks=[task_rec("demo-task", "acknowledged")])
        self.assertEqual((code, fake.tasks[0]["state"]), (0, "working"))

    def test_cap_from_settings(self):
        code, _, err, _ = invoke(self.ARGS, self.answers([task_entry("a")], (), self.OK), '{"worktree_cap": 1}')
        self.assertEqual(code, 2)
        self.assertIn("cap of 1", err)

    def at_cap(self, pr, sessions=(), tasks=()):
        """`new` with a cap of 2 and tasks a and b; b has no pull request, a's pull requests are `pr`."""
        answers = self.answers([task_entry("a"), task_entry("b")], sessions, self.OK, {
            ("gh",): (0, "[]", ""), ("gh", "pr", "list", "--head", "worktree-a"): (0, json.dumps(pr), "")})
        code, _, err, fake = invoke(self.ARGS, answers, '{"worktree_cap": 2}', tasks=tasks)
        return code, err, fake

    def test_cap_counts_only_tasks_in_motion(self):
        done = {"cwd": f"{WT}/a", "kind": "background", "id": "s1", "status": "idle", "state": "done"}
        frees = {
            "open, no session": ([{"state": "OPEN"}], ()),
            "open, finished session": ([{"state": "OPEN"}], [done]),
            "open, session idle with another state word": ([{"state": "OPEN"}], [dict(done, state="working")]),
            "merged, not cleaned": ([{"state": "MERGED"}], ()),
        }
        for name, (pr, sessions) in frees.items():
            with self.subTest(name):
                code, err, fake = self.at_cap(pr, sessions)
                self.assertEqual(code, 0, err)
                self.assertTrue(fake.ran("claude", "--bg"))
        holds = {
            "no pull request": ([], ()),
            "closed": ([{"state": "CLOSED"}], ()),
            "state unknown": ("not a list", ()),
            "open, session working": ([{"state": "OPEN"}], [dict(done, status="busy", state="working")]),
            "open, session waiting": ([{"state": "OPEN"}], [dict(done, status="waiting")]),
            "open, interactive session": ([{"state": "OPEN"}], [dict(done, kind="interactive")]),
            "merged, session working": ([{"state": "MERGED"}], [dict(done, status="busy", state="working")]),
            "open, an unfinished session without cwd": ([{"state": "OPEN"}], [{"kind": "background", "status": "busy"}]),
        }
        for name, (pr, sessions) in holds.items():
            with self.subTest(name):
                code, err, fake = self.at_cap(pr, sessions)
                self.assertEqual(code, 2, err)
                self.assertRegex(err, "cap of 2 tasks in motion: a, b$")
                self.assertFalse(fake.ran("claude", "--bg"))

    def test_cap_counts_declared_states(self):
        busy = [{"cwd": f"{WT}/a", "kind": "background", "id": "s1", "status": "busy", "state": "working"}]
        link = "https://x/y"
        code, err, fake = self.at_cap([{"state": "OPEN"}], tasks=[task_rec("a", "blocked")])
        self.assertEqual(code, 2)
        self.assertRegex(err, "cap of 2 tasks in motion: a \\(blocked\\), b$")
        self.assertFalse(fake.ran("claude", "--bg"))
        code, err, _ = self.at_cap([{"state": "OPEN"}], tasks=[task_rec("a", "working")])
        self.assertRegex(err, "in motion: a, b$")
        for name, pr in (("no pull request", []), ("closed", [{"state": "CLOSED"}])):
            with self.subTest(name):
                code, err, fake = self.at_cap(pr, tasks=[task_rec("a", "handed-over", link)])
                self.assertEqual(code, 0, err)
                self.assertTrue(fake.ran("claude", "--bg"))
        code, err, _ = self.at_cap([], busy, tasks=[task_rec("a", "handed-over", link)])
        self.assertEqual(code, 2)
        self.assertRegex(err, "in motion: a, b$")
        code, err, _ = self.at_cap([{"state": "OPEN"}], tasks=[task_rec("a", "acknowledged", link)])
        self.assertEqual(code, 0, err)

    def test_cap_lists_only_the_tasks_in_motion(self):
        answers = self.answers([task_entry(s) for s in "abc"], (), self.OK, {
            ("gh",): (0, "[]", ""), ("gh", "pr", "list", "--head", "worktree-a"): (0, '[{"state": "OPEN"}]', "")})
        code, _, err, _ = invoke(self.ARGS, answers, '{"worktree_cap": 2}')
        self.assertEqual(code, 2)
        self.assertRegex(err, "cap of 2 tasks in motion: b, c$")

    def test_detached_task_counts_as_in_motion(self):
        answers = self.answers([(f"{WT}/a", None, False)], (), self.OK, {("gh",): (0, '[{"state": "OPEN"}]', "")})
        code, _, err, _ = invoke(self.ARGS, answers, '{"worktree_cap": 1}')
        self.assertEqual(code, 2)
        self.assertRegex(err, "cap of 1 tasks in motion: a$")

    def test_below_the_cap_asks_gh_only_about_the_new_branch(self):
        code, _, err, fake = invoke(self.ARGS, self.answers([task_entry("a")], (), self.OK, {("gh",): (0, "[]", "")}))
        self.assertEqual(code, 0, err)
        self.assertEqual([c[4] for c in fake.calls if c[0] == "gh"], ["worktree-demo-task"])

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
        self.assertEqual((row["phase"], doc["in_motion"], doc["in_review"], doc["merged"]), ("review", 0, 1, 0))

    def test_phase_and_counts(self):
        entries = [task_entry(s) for s in ("moving", "working", "review", "merged")]
        busy = {"id": "s1", "cwd": f"{WT}/working", "kind": "background", "status": "busy", "state": "working"}
        prs = {("gh", "pr", "list", "--head", f"worktree-{slug}"): (0, json.dumps([{"state": state}]), "")
               for slug, state in (("working", "OPEN"), ("review", "OPEN"), ("merged", "MERGED"))}
        answers = repo_answers(entries, [busy], git_task_answers("x", time.time()), prs)
        code, out, err, _ = invoke(["status", "--json"], answers)
        self.assertEqual(code, 0, err)
        doc = json.loads(out)
        self.assertEqual([r["phase"] for r in doc["rows"]], ["motion", "motion", "review", "merged"])
        self.assertEqual((doc["open"], doc["in_motion"], doc["in_review"], doc["merged"]), (4, 2, 1, 1))
        _, out, _, _ = invoke(["status"], answers)
        lines = out.splitlines()
        self.assertEqual(lines[3].index("review", 6), lines[0].index("PHASE"))
        self.assertTrue(next(l for l in lines if l.startswith("in motion")).startswith(
            "in motion 2/5 (0 blocked); waiting for review 1; merged, not cleaned 1; settled, not cleaned 0; "))
        self.assertEqual(lines[-1], "waiting on the human: review -")

    def test_declared_state_deliverable_phases_and_five_counts(self):
        slugs = ("moving", "asking", "handed", "merged", "settled")
        tasks = [task_rec("asking", "blocked"), task_rec("handed", "handed-over", "/out/video.mp4"),
                 task_rec("merged", "handed-over", "https://x/y"), task_rec("settled", "dismissed", "https://x/z")]
        prs = {("gh", "pr", "list", "--head", "worktree-merged"): (0, json.dumps([{"state": "MERGED"}]), "")}
        answers = repo_answers([task_entry(s) for s in slugs], (), git_task_answers("x", time.time()), prs)
        code, out, err, _ = invoke(["status", "--json"], answers, tasks=tasks)
        self.assertEqual(code, 0, err)
        doc = json.loads(out)
        self.assertEqual([(r["phase"], r["state"], r["deliverable"]) for r in doc["rows"]],
                         [("motion", None, None), ("blocked", "blocked", None),
                          ("review", "handed-over", "/out/video.mp4"), ("merged", "handed-over", "https://x/y"),
                          ("settled", "dismissed", "https://x/z")])
        self.assertEqual((doc["in_motion"], doc["blocked"], doc["in_review"], doc["merged"], doc["settled"]),
                         (2, 1, 1, 1, 1))

    def test_table_shows_state_count_line_and_what_waits_on_the_human(self):
        slugs = ("moving", "asking", "handed", "pr-only")
        tasks = [task_rec("asking", "blocked"), task_rec("handed", "handed-over", "/out/video.mp4"),
                 task_rec("pr-only", "handed-over")]
        prs = {("gh", "pr", "list", "--head", "worktree-pr-only"): (0, json.dumps([{"state": "OPEN", "url": "http://pr/7"}]), "")}
        answers = repo_answers([task_entry(s) for s in slugs], (), git_task_answers("x", time.time()), prs)
        _, out, _, _ = invoke(["status"], answers, tasks=tasks)
        lines = out.splitlines()
        self.assertEqual(lines[0].split()[:3], ["SLUG", "PHASE", "STATE"])
        for line, state in zip(lines[1:5], ("-", "blocked", "handed-over", "handed-over")):
            self.assertTrue(line[lines[0].index("STATE"):].startswith(state), line)
        self.assertEqual(lines[5:], [
            "in motion 2/5 (1 blocked); waiting for review 2; merged, not cleaned 0; settled, not cleaned 0; "
            "leftover agent worktrees 0; worktrees outside .claude/worktrees 0",
            "waiting on the human: handed /out/video.mp4", "waiting on the human: pr-only http://pr/7"])

    def test_a_review_task_without_deliverable_or_pull_request_waits_with_a_dash(self):
        answers = repo_answers([task_entry("handed")], (), git_task_answers("x", time.time()))
        _, out, _, _ = invoke(["status"], answers, tasks=[task_rec("handed", "handed-over")])
        self.assertEqual(out.splitlines()[-1], "waiting on the human: handed -")

    def test_sessions_unlistable_counts_as_in_motion(self):
        doc, _ = self.status([task_entry("alpha")], pr=[{"state": "OPEN"}])
        self.assertEqual(doc["rows"][0]["phase"], "review")
        answers = repo_answers([task_entry("alpha")], (), git_task_answers("x", time.time(), pr=[{"state": "OPEN"}]),
                               {("claude", "agents"): (1, "", "boom")})
        _, out, _, _ = invoke(["status", "--json"], answers)
        self.assertEqual((json.loads(out)["rows"][0]["phase"], json.loads(out)["in_motion"]), ("motion", 1))
        doc, _ = self.status([task_entry("alpha")], [{"kind": "background", "status": "busy"}], pr=[{"state": "OPEN"}])
        self.assertEqual(doc["rows"][0]["phase"], "motion")

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

    def test_exited_lead_is_matched_by_name_at_the_main_checkout(self):
        exited = EXITED | {"name": "alpha"}
        pr = [{"state": "OPEN", "url": "u", "headRefOid": "z"}]
        row = self.status([task_entry("alpha")], [exited], pr=pr)[0]["rows"][0]
        self.assertEqual((row["session"], row["session_id"], row["phase"], row["overtime"]),
                         ("done", "ex1", "review", False))
        for name, other in {"another name": {"name": "beta"}, "another repo": {"cwd": "/other"},
                            "process alive": {"pid": 7}, "interactive": {"kind": "interactive"},
                            "no cwd": {"cwd": None}}.items():
            with self.subTest(name):
                row = self.status([task_entry("alpha")], [exited | other], pr=pr)[0]["rows"][0]
                self.assertEqual((row["session"], row["session_id"]), ("none", None))

    def test_worktree_locks_are_parsed(self):
        entries = factory.parse_worktrees(porcelain((ROOT, "main", False), (f"{WT}/a", "worktree-a", False, ""),
                                                    (f"{WT}/b", "worktree-b", False, "claude session b (pid 1)")))
        self.assertEqual([e["locked"] for e in entries], [None, "", "claude session b (pid 1)"])

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

    def stacked(self, x_pr, *, x_worktree=True, x_branch=True):
        """The row of y, whose recorded base is worktree-x, and the calls; `x_pr` is x's pull requests."""
        entries = [task_entry("x"), task_entry("y")] if x_worktree else [task_entry("y")]
        answers = repo_answers(entries, (), git_task_answers("y", time.time()),
                               {("git", "config", "--get", "branch.worktree-y.factoryBase"): (0, "worktree-x\n", ""),
                                ("git", "rev-parse", "--verify", "--quiet", "refs/heads/worktree-x"):
                                    (0 if x_branch else 1, "", ""),
                                ("gh", "pr", "list", "--head", "worktree-x"):
                                    (0, json.dumps(x_pr), "") if x_pr != "missing" else (127, "", "no gh")})
        code, out, err, fake = invoke(["status", "--json"], answers)
        self.assertEqual(code, 0, err)
        return json.loads(out)["rows"][-1], fake

    def test_recorded_base_and_ahead_behind_against_it(self):
        merged_elsewhere = [{"state": "MERGED", "url": "u", "headRefOid": "h", "baseRefName": "worktree-w"}]
        cases = [("base task open", [{"state": "OPEN", "url": "u", "headRefOid": "h", "baseRefName": "main"}], True),
                 ("base task has no pull request", [], True),
                 ("base task's pull request unknown", "missing", True),
                 ("base task merged into another branch", merged_elsewhere, True),
                 ("base has no worktree, branch left, not merged", [], False)]
        for name, x_pr, x_worktree in cases:
            with self.subTest(name):
                row, fake = self.stacked(x_pr, x_worktree=x_worktree)
                self.assertEqual((row["base"], row["stacked_on"]), ("worktree-x", "worktree-x"))
                self.assertIn(["git", "rev-list", "--left-right", "--count", "worktree-x...HEAD"], fake.calls)

    def test_a_merged_or_missing_recorded_base_is_not_shown(self):
        merged = [{"state": "MERGED", "url": "u", "headRefOid": "h", "baseRefName": "main"}]
        cases = [("base task merged into the default branch", merged, True, True),
                 ("base has no worktree, branch left, merged", merged, False, True),
                 ("base branch is gone", [], False, False)]
        for name, x_pr, x_worktree, x_branch in cases:
            with self.subTest(name):
                row, fake = self.stacked(x_pr, x_worktree=x_worktree, x_branch=x_branch)
                self.assertEqual((row["base"], row["stacked_on"]), ("origin/main", "worktree-x"))
                self.assertIn(["git", "rev-list", "--left-right", "--count", "origin/main...HEAD"], fake.calls)
                self.assertFalse(fake.ran("git", "rev-list", "--left-right", "--count", "worktree-x...HEAD"))
        _, out, _, _ = invoke(["status"], repo_answers(
            [task_entry("x"), task_entry("y")], (), git_task_answers("y", time.time(), pr=merged),
            {("git", "config", "--get", "branch.worktree-y.factoryBase"): (0, "worktree-x\n", "")}))
        self.assertNotIn("worktree-x ", out.splitlines()[2].split("worktree-y", 1)[1])

    def test_default_base_when_none_recorded(self):
        doc, _ = self.status([task_entry("alpha")])
        self.assertEqual((doc["rows"][0]["base"], doc["rows"][0]["stacked_on"]), ("origin/main", None))

    def test_elapsed_and_overtime_on_both_sides_of_the_limit(self):
        now_ms = int(time.time() * 1000)
        H = 3600 * 1000

        def session(hours, **kw):
            return dict({"cwd": f"{WT}/alpha", "kind": "background", "status": "busy", "state": "working",
                         "startedAt": now_ms - int(hours * H)}, **kw)
        cases = [
            ("working past the limit", session(2.5), True),
            ("working within the limit", session(1.5), False),
            ("waiting past the limit", session(2.5, status="waiting"), True),
            ("turn ended, state working", session(2.5, status="idle"), False),
            ("turn ended, state done", session(2.5, status="idle", state="done"), False),
            ("stopped", {k: v for k, v in session(2.5, state="stopped").items() if k != "status"}, False),
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
        self.assertEqual(lines[-1], "in motion 1/5 (0 blocked); waiting for review 0; merged, not cleaned 0; "
                                    "settled, not cleaned 0; leftover agent worktrees 1; "
                                    "worktrees outside .claude/worktrees 1")

    def test_empty_table(self):
        out, _ = self.status([], as_json=False)
        self.assertEqual(out.strip(), "in motion 0/5 (0 blocked); waiting for review 0; merged, not cleaned 0; "
                                      "settled, not cleaned 0; leftover agent worktrees 0; "
                                      "worktrees outside .claude/worktrees 0")


class MalformedGhTests(unittest.TestCase):
    def test_bad_entries_are_unknown_not_a_traceback(self):
        for name, pr in (("empty object", [{}]), ("null state", [{"state": None}]), ("number state", [{"state": 3}]),
                         ("strings", ["x"]), ("null entry", [None])):
            with self.subTest(name):
                answers = repo_answers([task_entry("x")], (), git_task_answers("x", time.time(), pr=pr))
                code, out, err, _ = invoke(["status", "--json"], answers)
                self.assertEqual(code, 0, err)
                self.assertEqual(json.loads(out)["rows"][0]["pr_state"], "unknown")


class CleanTests(unittest.TestCase):
    HEAD = "sha-x"

    def clean(self, *, dirty="", pr=None, sessions=(), dry=False, extra=None, entries=None, tasks=()):
        if pr is None:
            pr = [{"state": "MERGED", "url": "u", "headRefOid": self.HEAD, "baseRefName": "main"}]
        answers = repo_answers(entries or [task_entry("x")], list(sessions),
                               git_task_answers("x", time.time(), dirty=dirty, pr=pr))
        answers.update({("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", ""),
                        ("git", "branch", "-r"): (0, "  origin/worktree-x\n", ""), ("claude", "rm"): (0, "", "")})
        answers.update(extra or {})
        return invoke(["clean"] + (["--dry-run"] if dry else []), answers, tasks=tasks)

    @staticmethod
    def removed_anything(fake):
        return any(c[:3] in (["git", "branch", "-d"], ["git", "worktree", "remove"]) or c[:2] == ["claude", "rm"]
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
            "busy session": (dict(sessions=[bg(status="busy", state="working")]),
                             "not finished \\(status busy, state working\\)"),
            "waiting session": (dict(sessions=[bg(status="waiting")]), "not finished \\(status waiting, state done\\)"),
            "unknown status": (dict(sessions=[bg(status="needs_input")]), "status needs_input, state done"),
            "missing status, process alive": (dict(sessions=[EXITED | {"pid": 7, "cwd": f"{WT}/x"}]),
                                              "status missing, state done"),
            "exited, not done": (dict(sessions=[EXITED | {"state": "failed"}]), "status missing, state failed"),
            "exited, status present": (dict(sessions=[EXITED | {"status": "busy"}]), "status busy, state done"),
            "two exited sessions carry its name": (dict(sessions=[EXITED, EXITED | {"id": "ex2"}]),
                                                   "more than one exited session"),
            "locked, no session": (
                dict(entries=[task_entry("x") + ("claude session x (pid 7 start Sat)",)]),
                f"locked \\(claude session x \\(pid 7 start Sat\\)\\) and no session .*run: git worktree unlock {WT}/x$"),
            "locked without a reason, no session": (
                dict(entries=[task_entry("x") + ("",)]), f"locked and no session .*run: git worktree unlock {WT}/x$"),
            "locked, exited session of another name": (
                dict(entries=[task_entry("x") + ("",)], sessions=[EXITED | {"name": "y"}]), "locked and no session"),
            "exited, no state": (dict(sessions=[{k: v for k, v in EXITED.items() if k != "state"}]),
                                 "status missing, state missing"),
            "pid, no status, stopped": (dict(sessions=[EXITED | {"pid": 7, "cwd": f"{WT}/x", "state": "stopped"}]),
                                        "status missing, state stopped"),
            "one finished, one not": (dict(sessions=[bg(pid=1), bg(id="cd", pid=2, status="busy")]),
                                      "session cd is not finished"),
            "worktree nested inside": (
                dict(entries=[task_entry("x"), (f"{WT}/x/.claude/worktrees/agent-1", "worktree-agent-1", False)]),
                "registered inside it: .*/x/.claude/worktrees/agent-1"),
            "session turns busy before removal": (
                dict(extra={("claude", "agents"): [(0, "[]", ""), (0, json.dumps([bg(status="busy", state="working")]), "")]}),
                "not finished \\(status busy"),
            "exited lead is rechecked before removal": (
                dict(extra={("claude", "agents"): [(0, json.dumps([EXITED]), ""),
                                                   (0, json.dumps([EXITED | {"state": "failed"}]), "")]}),
                "status missing, state failed"),
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

    def settled(self, state="acknowledged", deliverable="https://x/y", **kw):
        kw.setdefault("pr", [])
        return self.clean(tasks=[task_rec("x", state, deliverable)], **kw)

    def test_settled_task_is_removed_when_every_check_holds(self):
        for state in ("acknowledged", "dismissed"):
            for deliverable in ("https://x/y", "/out/video.mp4"):
                with self.subTest(state=state, deliverable=deliverable):
                    code, out, _, fake = self.settled(state, deliverable)
                    self.assertEqual((code, out.strip()), (0, "removed x"))
                    self.assertIn(["git", "branch", "-r", "--contains", "HEAD"], fake.calls)
        code, out, _, _ = self.settled(pr=self.pr("CLOSED"))
        self.assertEqual(out.strip(), "removed x")

    def test_settled_task_is_kept_when_a_check_fails(self):
        bg = {"id": "ab", "cwd": f"{WT}/x", "kind": "background", "status": "busy", "state": "working"}
        cases = {
            "merely handed over": (dict(state="handed-over"), "no merged pull request \\(none\\)"),
            "working": (dict(state="working"), "no merged pull request \\(none\\)"),
            "pull request open": (dict(pr=self.pr("OPEN")), "settled, but its pull request is open"),
            "pull request unknown": (dict(extra={("gh",): (1, "", "no gh")}), "settled, but its pull request is unknown"),
            "no deliverable": (dict(deliverable=None), "no deliverable is recorded"),
            "deliverable is the worktree": (dict(deliverable=f"{WT}/x"), "deliverable is inside the worktree"),
            "deliverable inside the worktree": (dict(deliverable=f"{WT}/x/out/video.mp4"),
                                                "deliverable is inside the worktree"),
            "head on no remote": (dict(extra={("git", "branch", "-r"): (0, "\n", "")}), "on no remote-tracking branch"),
            "git branch -r fails": (dict(extra={("git", "branch", "-r"): (128, "", "bad")}), "cannot tell whether"),
            "dirty tree": (dict(dirty=" M a\n"), "uncommitted changes"),
            "unfinished session": (dict(sessions=[bg]), "not finished"),
        }
        for name, (kw, pattern) in cases.items():
            with self.subTest(name):
                code, out, _, fake = self.settled(**kw)
                self.assertEqual(code, 0)
                self.assertRegex(out, f"^kept x: .*{pattern}")
                self.assertFalse(self.removed_anything(fake))

    def test_a_sibling_directory_of_the_worktree_is_not_inside_it(self):
        code, out, _, _ = self.settled(deliverable=f"{WT}/x-out/video.mp4")
        self.assertEqual(out.strip(), "removed x")

    def test_rule_1_ignores_the_declared_state(self):
        for tasks in ([], [task_rec("x", "working")], [task_rec("x", "blocked")], [task_rec("x", "handed-over")],
                      [task_rec("x", "acknowledged")]):
            with self.subTest(tasks=tasks):
                code, out, _, fake = self.clean(tasks=tasks, extra={("git", "branch", "-r"): (128, "", "bad")})
                self.assertEqual(out.strip(), "removed x")
                self.assertFalse(fake.ran("git", "branch", "-r"))

    def test_sessions_that_do_not_belong_to_the_task_do_not_protect_it(self):
        elsewhere = [{"sessionId": "i1", "cwd": f"{WT}/x-other", "kind": "interactive"},
                     {"sessionId": "i2", "cwd": ROOT, "kind": "interactive"}]
        code, out, _, _ = self.clean(sessions=elsewhere)
        self.assertEqual(out.strip(), "removed x")

    def test_relist_failure_before_removal_stops_and_exits_2(self):
        entries = [task_entry("x"), task_entry("y")]
        agents = [(0, "[]", ""), (1, "", "boom")]
        code, out, err, fake = self.clean(entries=entries, extra={("claude", "agents"): agents})
        self.assertEqual(code, 2)
        self.assertIn("cannot list sessions again before removing x", err)
        self.assertEqual(out, "")
        self.assertFalse(self.removed_anything(fake))

    def test_sessions_unlistable_removes_nothing(self):
        for name, result in (("missing claude", (127, "", "no claude")), ("bad json", (0, "not json", "")),
                             ("not a list", (0, "{}", "")), ("nonzero exit", (1, "[]", "boom")),
                             ("entry not an object", (0, '["junk"]', ""))):
            with self.subTest(name):
                for dry in (False, True):
                    code, out, err, fake = self.clean(dry=dry, extra={("claude", "agents"): result})
                    self.assertEqual(code, 2)
                    self.assertIn("cannot list sessions", err)
                    self.assertEqual(out, "")
                    self.assertFalse(self.removed_anything(fake))

    def test_unfinished_session_without_cwd_removes_nothing(self):
        for name, session in (("busy background", {"kind": "background", "id": "ab", "status": "busy",
                                                   "state": "working"}),
                              ("interactive", {"kind": "interactive", "sessionId": "s9", "status": "idle"}),
                              ("empty cwd", {"kind": "background", "id": "ab", "status": "busy", "cwd": ""})):
            with self.subTest(name):
                for dry in (False, True):
                    code, out, err, fake = self.clean(dry=dry, sessions=[session])
                    self.assertEqual(code, 2)
                    self.assertIn("has no working directory and is not finished", err)
                    self.assertEqual(out, "")
                    self.assertFalse(self.removed_anything(fake))

    def test_finished_session_without_cwd_does_not_block(self):
        session = {"kind": "background", "id": "ab", "status": "idle", "state": "done"}
        code, out, _, _ = self.clean(sessions=[session])
        self.assertEqual((code, out.strip()), (0, "removed x"))

    def stacked(self, y_pr):
        """`clean` with y's recorded base still x; x is merged into main, y's pull requests are `y_pr`."""
        answers = repo_answers([task_entry("x"), task_entry("y")], (),
                               git_task_answers("x", time.time(), pr=self.pr()),
                               {("git", "config", "--get", "branch.worktree-y.factoryBase"): (0, "worktree-x\n", ""),
                                ("gh", "pr", "list", "--head", "worktree-y"): (0, json.dumps(y_pr), ""),
                                ("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", "")})
        _, out, _, fake = invoke(["clean"], answers)
        return out, fake

    def test_kept_when_another_task_is_stacked_on_it(self):
        for name, y_pr in (("open", self.pr("OPEN", "worktree-x")), ("no pull request", []),
                           ("merged into the base task", self.pr(base="worktree-x")),
                           ("open, retargeted to main", self.pr("OPEN"))):
            with self.subTest(name):
                out, fake = self.stacked(y_pr)
                self.assertIn("kept x: y is stacked on it", out)
                self.assertFalse(fake.ran("git", "worktree", "remove", f"{WT}/x"))

    def test_a_merged_stack_is_removed_although_the_recorded_base_was_never_cleared(self):
        out, fake = self.stacked(self.pr())
        self.assertEqual(out.splitlines(), ["removed x", "removed y"])
        self.assertTrue(fake.ran("git", "worktree", "remove", f"{WT}/x"))

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

    def test_a_turn_that_has_ended_is_finished_whatever_the_state_word(self):
        live = {"id": "ab12", "cwd": f"{WT}/x", "kind": "background", "status": "idle", "pid": 7}
        cases = {"idle, working": (live | {"state": "working"}, "ab12"),
                 "idle, blocked": (live | {"state": "blocked"}, "ab12"),
                 "idle, no state": (live, "ab12"),
                 "stopped": (EXITED | {"state": "stopped"}, "ex1"),
                 "stopped, cwd still in the worktree": (EXITED | {"state": "stopped", "cwd": f"{WT}/x"}, "ex1")}
        for name, (session, sid) in cases.items():
            with self.subTest(name):
                code, out, _, fake = self.clean(sessions=[session])
                self.assertEqual((code, out.strip()), (0, "removed x"))
                self.assertIn(["claude", "rm", sid], fake.calls)

    def test_exited_lead_is_removed_with_claude_rm(self):
        locked = [task_entry("x") + ("claude session x (pid 7 start Sat)",)]
        for name, kw in {"unlocked": {}, "locked": dict(entries=locked),
                         "exits between listing and removal": dict(extra={("claude", "agents"): [
                             (0, json.dumps([EXITED | {"cwd": f"{WT}/x", "pid": 7, "status": "idle"}]), ""),
                             (0, json.dumps([EXITED]), "")]})}.items():
            with self.subTest(name):
                code, out, _, fake = self.clean(sessions=[EXITED], **kw)
                self.assertEqual((code, out.strip()), (0, "removed x"))
                self.assertIn(["claude", "rm", "ex1"], fake.calls)
                self.assertFalse(fake.ran("git", "worktree", "remove"))

    def test_a_lock_is_reported_only_when_the_task_would_otherwise_go(self):
        code, out, _, _ = self.clean(entries=[task_entry("x") + ("",)], pr=[])
        self.assertEqual(out.strip(), "kept x: no merged pull request (none)")

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

    def factory(self, *args, env=None, cwd=None):
        return subprocess.run([sys.executable, str(BIN), *args], cwd=cwd or self.repo, env={**self.env, **(env or {})},
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
        self.assertEqual((row["phase"], doc["in_motion"]), ("motion", 1))

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

    def test_state_from_inside_a_worktree_shows_in_status(self):
        self.git("worktree", "add", "-q", "-b", "worktree-demo-task", ".claude/worktrees/demo-task")
        task_dir = self.repo / ".claude/worktrees/demo-task"
        (task_dir / "sub").mkdir()
        p = self.factory("state", "working", cwd=task_dir / "sub")
        self.assertEqual((p.returncode, p.stdout, p.stderr), (0, "demo-task: working\n", ""))
        p = self.factory("state", "handed-over", "https://x/y", cwd=task_dir)
        self.assertEqual((p.returncode, p.stderr), (0, ""))
        (rec,) = [json.loads(line) for line in (self.home / ".claude/factory/tasks.jsonl").read_text().splitlines()][1:]
        self.assertEqual((rec["repo"], rec["slug"], rec["state"]), (str(self.repo), "demo-task", "handed-over"))
        row = json.loads(self.factory("status", "--json").stdout)["rows"][0]
        self.assertEqual((row["state"], row["deliverable"], row["phase"]), ("handed-over", "https://x/y", "review"))
        p = self.factory("state", "working", cwd=self.repo)
        self.assertEqual(p.returncode, 2)

    def test_a_file_inside_the_worktree_is_refused(self):
        self.git("worktree", "add", "-q", "-b", "worktree-demo-task", ".claude/worktrees/demo-task")
        task_dir = self.repo / ".claude/worktrees/demo-task"
        (task_dir / "out.mp4").write_text("x")
        p = self.factory("state", "handed-over", str(task_dir / "out.mp4"), cwd=task_dir)
        self.assertEqual((p.returncode, p.stdout), (2, ""))
        self.assertIn("inside the worktree", p.stderr)
        outside = self.home / "out.mp4"
        outside.write_text("x")
        (self.home / "rel.mp4").write_text("x")
        rel = os.path.relpath(self.home / "rel.mp4", task_dir)
        for given, stored in ((str(outside), outside), ("~/out.mp4", outside), (rel, self.home / "rel.mp4")):
            p = self.factory("state", "handed-over", given, cwd=task_dir)
            self.assertEqual((p.returncode, p.stderr), (0, ""))
            last = json.loads((self.home / ".claude/factory/tasks.jsonl").read_text().splitlines()[-1])
            self.assertEqual(last["deliverable"], os.path.realpath(stored))

    def test_default_dispatcher_name_comes_from_the_main_checkout_inside_a_worktree(self):
        self.git("worktree", "add", "-q", "-b", "worktree-first", ".claude/worktrees/first")
        p = subprocess.run([sys.executable, str(BIN), "new", "second-task", "--brief", "x"],
                           cwd=self.repo / ".claude/worktrees/first", env=self.env, capture_output=True, text=True)
        self.assertEqual((p.returncode, p.stderr), (0, ""))
        self.assertIn("The dispatcher's session name is dispatcher-repo.", json.loads(self.argv_file.read_text())[-1])


if __name__ == "__main__":
    unittest.main()
