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


def invoke(argv, answers, settings=None):
    fake = FakeRun(answers)
    out, err = io.StringIO(), io.StringIO()
    with tempfile.TemporaryDirectory() as home, mock.patch.dict(os.environ, {"HOME": home}):
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
        self.assertEqual(self.load("not json"), want)
        self.assertEqual(self.load("[1]"), want)
        self.assertEqual(self.load("{}"), want)

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

    def test_launch_argv(self):
        ok = (0, "backgrounded · 96dc4869 · demo-task\n", "")
        with mock.patch.object(factory.time, "time", return_value=1794610677):
            code, out, err, fake = invoke(self.ARGS, repo_answers([], (), {("claude", "--bg"): ok}))
        self.assertEqual(code, 0, err)
        self.assertIn("96dc4869", out)
        self.assertIn("demo-task", out)
        plugin = str(BIN.resolve().parent.parent)
        lead = (BIN.resolve().parent.parent / "roles" / "lead.md").read_text()
        want = ["claude", "--bg", "--worktree", "demo-task", "-n", "demo-task", "--model", "fable",
                "--permission-mode", "auto", "--plugin-dir", plugin, "--append-system-prompt", lead,
                "Your task slug is demo-task. The dispatcher's session name is d1. "
                "Your base branch is origin/main. You started at 2026-11-13T22:57:57Z. "
                "Your time limit is 2 hours. Your brief follows.\n\ndo it"]
        self.assertEqual(fake.calls[-1], want)

    def test_launch_argv_uses_settings(self):
        ok = (0, "backgrounded · 1 · demo-task\n", "")
        settings = '{"lead_model": "opus", "lead_permission_mode": "plan"}'
        _, _, _, fake = invoke(self.ARGS, repo_answers([], (), {("claude", "--bg"): ok}), settings)
        argv = fake.calls[-1]
        self.assertEqual(argv[argv.index("--model") + 1], "opus")
        self.assertEqual(argv[argv.index("--permission-mode") + 1], "plan")

    def test_brief_file(self):
        with tempfile.NamedTemporaryFile("w", suffix=".md") as f:
            f.write("from file")
            f.flush()
            args = ["new", "demo-task", "--brief-file", f.name, "--dispatcher", "d1"]
            ok = (0, "backgrounded · 1 · demo-task\n", "")
            code, _, _, fake = invoke(args, repo_answers([], (), {("claude", "--bg"): ok}))
        self.assertEqual(code, 0)
        self.assertTrue(fake.calls[-1][-1].endswith("\n\nfrom file"))

    def test_precondition_errors_exit_2_and_start_nothing(self):
        bg = {("claude", "--bg"): (0, "backgrounded · 1 · demo-task\n", "")}
        cases = {
            "bad slug": (["new", "Bad", "--brief", "x", "--dispatcher", "d"], repo_answers([], (), bg), "invalid slug"),
            "agent prefix": (["new", "agent-abc", "--brief", "x", "--dispatcher", "d"], repo_answers([], (), bg), "agent-"),
            "empty brief": (["new", "demo-task", "--brief", "  ", "--dispatcher", "d"], repo_answers([], (), bg), "empty"),
            "worktree exists": (self.ARGS, repo_answers([task_entry("demo-task")], (), bg), "worktree already exists"),
            "branch exists": (self.ARGS, repo_answers([], (), bg, {("git", "rev-parse"): (0, "sha\n", "")}), "branch already exists"),
            "session exists": (self.ARGS, repo_answers([], [{"name": "demo-task", "cwd": "/elsewhere"}], bg), "session named"),
            "cap": (self.ARGS, repo_answers([task_entry(s) for s in "abcde"], (), bg), "cap of 5.*a, b, c, d, e"),
            "no repo": (self.ARGS, {("git",): (128, "", "not a repo")}, "not inside a git repository"),
            "agents unlistable": (self.ARGS, repo_answers([], (), bg, {("claude", "agents"): (1, "", "boom")}), "cannot list sessions"),
        }
        for name, (argv, answers, pattern) in cases.items():
            with self.subTest(name):
                code, _, err, fake = invoke(argv, answers)
                self.assertEqual(code, 2, err)
                self.assertRegex(err, pattern)
                self.assertEqual(len(err.strip().splitlines()), 1)
                self.assertFalse(fake.ran("claude", "--bg"))

    def test_base_creates_worktree_records_base_and_launches(self):
        ok = {("claude", "--bg"): (0, "backgrounded · 1 · demo-task\n", ""),
              ("git", "worktree", "add"): (0, "", ""), ("git", "config"): (0, "", "")}
        code, _, err, fake = invoke(self.ARGS + ["--base", "alpha"],
                                    repo_answers([task_entry("alpha")], (), ok))
        self.assertEqual(code, 0, err)
        add = ["git", "worktree", "add", "-b", "worktree-demo-task", f"{WT}/demo-task", "worktree-alpha"]
        cfg = ["git", "config", "branch.worktree-demo-task.factoryBase", "worktree-alpha"]
        self.assertLess(fake.calls.index(add), fake.calls.index(cfg))
        self.assertLess(fake.calls.index(cfg), len(fake.calls) - 1)
        launch = fake.calls[-1]
        self.assertEqual(launch[:6], ["claude", "--bg", "--worktree", "demo-task", "-n", "demo-task"])
        self.assertIn("Your base branch is worktree-alpha.", launch[-1])
        self.assertFalse(fake.ran("git", "worktree", "remove"))

    def test_unknown_base_refused_before_anything_is_created(self):
        code, _, err, fake = invoke(self.ARGS + ["--base", "ghost"], repo_answers([task_entry("alpha")]))
        self.assertEqual(code, 2)
        self.assertIn("ghost", err)
        self.assertFalse(fake.ran("git", "worktree", "add"))
        self.assertFalse(fake.ran("claude", "--bg"))

    def test_failed_launch_with_base_removes_worktree_and_branch(self):
        answers = repo_answers([task_entry("alpha")], (), {
            ("claude", "--bg"): (1, "", "boom"), ("git", "worktree"): (0, porcelain((ROOT, "main", False)), ""),
            ("git", "config"): (0, "", ""), ("git", "branch"): (0, "", "")})
        answers[("git", "worktree", "list")] = (0, porcelain((ROOT, "main", False), task_entry("alpha")), "")
        answers[("git", "worktree", "add")] = (0, "", "")
        answers[("git", "worktree", "remove")] = (0, "", "")
        code, _, err, fake = invoke(self.ARGS + ["--base", "alpha"], answers)
        self.assertEqual(code, 1)
        self.assertIn("boom", err)
        self.assertIn(["git", "worktree", "remove", "--force", f"{WT}/demo-task"], fake.calls)
        self.assertIn(["git", "branch", "-D", "worktree-demo-task"], fake.calls)

    def test_failed_launch_without_base_creates_nothing_to_undo(self):
        _, _, _, fake = invoke(self.ARGS, repo_answers([], (), {("claude", "--bg"): (1, "", "boom")}))
        self.assertFalse(fake.ran("git", "worktree", "remove"))

    def test_cap_from_settings(self):
        bg = {("claude", "--bg"): (0, "backgrounded · 1 · demo-task\n", "")}
        code, _, err, _ = invoke(self.ARGS, repo_answers([task_entry("a")], (), bg), '{"worktree_cap": 1}')
        self.assertEqual(code, 2)
        self.assertIn("cap of 1", err)

    def test_invalid_settings_exit_2(self):
        code, _, err, _ = invoke(self.ARGS, repo_answers([]), '{"worktree_cap": 0}')
        self.assertEqual((code, "worktree_cap" in err), (2, True))

    def test_claude_failure_passes_stderr(self):
        for result in ((1, "", "model not found\n"), (0, "no marker line\n", "")):
            with self.subTest(result=result):
                code, _, err, _ = invoke(self.ARGS, repo_answers([], (), {("claude", "--bg"): result}))
                self.assertEqual(code, 1)
                self.assertIn("did not start", err)
        _, _, err, _ = invoke(self.ARGS, repo_answers([], (), {("claude", "--bg"): (1, "", "model not found\n")}))
        self.assertIn("model not found", err)

    def test_no_remote_defaults_to_main(self):
        answers = repo_answers([], (), {("claude", "--bg"): (0, "backgrounded · 1 · demo-task\n", "")})
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
    def clean(self, *, dirty="", merged=True, pr=None, session=None, dry=False, extra=None):
        answers = repo_answers([task_entry("x")], [session] if session else [],
                               git_task_answers("x", time.time(), dirty=dirty, pr=pr))
        answers[("git", "merge-base")] = (0 if merged else 1, "", "")
        answers.update({("git", "worktree", "remove"): (0, "", ""), ("git", "branch"): (0, "", ""),
                        ("claude", "rm"): (0, "", "")})
        answers.update(extra or {})
        code, out, err, fake = invoke(["clean"] + (["--dry-run"] if dry else []), answers)
        return code, out, err, fake

    def test_keep_reasons(self):
        open_pr = [{"state": "OPEN", "url": "u", "headRefOid": "sha-x"}]
        merged_pr = [{"state": "MERGED", "url": "u", "headRefOid": "other"}]
        busy = {"id": "ab", "cwd": f"{WT}/x", "kind": "background", "status": "busy", "state": "working"}
        cases = {
            "uncommitted": (dict(dirty=" M a\n"), "uncommitted changes"),
            "unmerged open pr": (dict(merged=False, pr=open_pr), "no merged pull request \\(open\\)"),
            "unmerged no pr": (dict(merged=False), "no merged pull request \\(none\\)"),
            "unmerged gh missing": (dict(merged=False, pr="missing"), "pull request state is unknown"),
            "merged pr but newer commits": (dict(merged=False, pr=merged_pr), "newer than its merged pull request"),
            "busy session": (dict(session=busy), "session is busy"),
        }
        for name, (kw, pattern) in cases.items():
            with self.subTest(name):
                code, out, _, fake = self.clean(**kw)
                self.assertEqual(code, 0)
                self.assertRegex(out, f"^kept x: .*{pattern}")
                self.assertFalse(any(c[:2] in (["git", "branch"], ["claude", "rm"]) or c[:3] == ["git", "worktree", "remove"]
                                     for c in fake.calls))

    def test_kept_when_another_task_is_stacked_on_it(self):
        entries = [task_entry("x"), task_entry("y")]
        answers = repo_answers(entries, (), git_task_answers("x", time.time()),
                               {("git", "merge-base"): (0, "", ""),
                                ("git", "config", "--get", "branch.worktree-y.factoryBase"): (0, "worktree-x\n", "")})
        code, out, _, fake = invoke(["clean"], answers)
        self.assertIn("kept x: y is stacked on it", out)
        self.assertFalse(fake.ran("git", "worktree", "remove", f"{WT}/x"))

    def test_merged_means_into_the_default_branch_only(self):
        _, _, _, fake = self.clean()
        self.assertIn(["git", "merge-base", "--is-ancestor", "worktree-x", "origin/main"], fake.calls)

    def test_remove_without_session_uses_git(self):
        code, out, _, fake = self.clean()
        self.assertEqual((code, out.strip()), (0, "removed x"))
        self.assertIn(["git", "worktree", "remove", f"{WT}/x"], fake.calls)
        self.assertIn(["git", "branch", "-d", "worktree-x"], fake.calls)
        self.assertNotIn("--force", [a for c in fake.calls for a in c])
        self.assertFalse(fake.ran("claude", "rm"))

    def test_remove_with_session_uses_claude_rm(self):
        idle = {"id": "ab12", "cwd": f"{WT}/x", "kind": "background", "status": "idle", "state": "done"}
        code, out, _, fake = self.clean(session=idle)
        self.assertEqual((code, out.strip()), (0, "removed x"))
        self.assertIn(["claude", "rm", "ab12"], fake.calls)
        self.assertFalse(fake.ran("git", "worktree", "remove"))
        self.assertFalse(fake.ran("git", "branch"))

    def test_merged_pull_request_at_same_head_is_removed(self):
        pr = [{"state": "MERGED", "url": "u", "headRefOid": "sha-x"}]
        code, out, _, _ = self.clean(merged=False, pr=pr)
        self.assertEqual(out.strip(), "removed x")

    def test_dry_run_changes_nothing(self):
        code, out, _, fake = self.clean(dry=True)
        self.assertEqual(out.strip(), "would remove x")
        mutating = [c for c in fake.calls if c[:3] == ["git", "worktree", "remove"] or c[:2] in (["git", "branch"], ["claude", "rm"])]
        self.assertEqual(mutating, [])

    def test_failed_removal_exits_1_and_says_why(self):
        code, out, err, _ = self.clean(extra={("git", "worktree", "remove"): (1, "", "locked")})
        self.assertEqual(code, 1)
        self.assertIn("locked", err)
        self.assertNotIn("removed", out)


class EndToEndTests(unittest.TestCase):
    FAKE_CLAUDE = """#!/usr/bin/env python3
import json, os, sys
if sys.argv[1:2] == ["agents"]:
    print(os.environ.get("FAKE_AGENTS", "[]"))
elif sys.argv[1:2] == ["--bg"]:
    json.dump(sys.argv, open(os.environ["FAKE_ARGV"], "w"))
    print("backgrounded · 96dc4869 · " + sys.argv[sys.argv.index("-n") + 1])
else:
    sys.exit(3)
"""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        tmp = Path(self.tmp.name).resolve()
        self.repo, self.home, bindir = tmp / "repo", tmp / "home", tmp / "bin"
        for d in (self.repo, self.home, bindir):
            d.mkdir()
        claude = bindir / "claude"
        claude.write_text(self.FAKE_CLAUDE)
        claude.chmod(claude.stat().st_mode | stat.S_IXUSR)
        self.argv_file = tmp / "argv.json"
        self.env = {**os.environ, "HOME": str(self.home), "PATH": f"{bindir}{os.pathsep}{os.environ['PATH']}",
                    "FAKE_ARGV": str(self.argv_file), "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}
        self.git("init", "-q", "-b", "main")
        self.git("commit", "-q", "--allow-empty", "-m", "init")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env, check=True, capture_output=True, text=True).stdout

    def factory(self, *args, env=None):
        return subprocess.run([sys.executable, str(BIN), *args], cwd=self.repo, env={**self.env, **(env or {})},
                              capture_output=True, text=True)

    def test_new_status_clean(self):
        p = self.factory("new", "demo-task", "--brief", "build it", "--dispatcher", "d1")
        self.assertEqual((p.returncode, p.stderr), (0, ""))
        self.assertIn("96dc4869", p.stdout)
        argv = json.loads(self.argv_file.read_text())
        self.assertEqual(argv[1:6], ["--bg", "--worktree", "demo-task", "-n", "demo-task"])
        self.assertIn("build it", argv[-1])
        self.assertIn("d1", argv[-1])

        # the fake does not make worktrees; create the one claude would have made
        self.git("worktree", "add", "-q", "-b", "worktree-demo-task", ".claude/worktrees/demo-task")
        agents = json.dumps([{"id": "96dc4869", "cwd": str(self.repo / ".claude/worktrees/demo-task"),
                              "kind": "background", "name": "demo-task", "status": "idle", "state": "done",
                              "startedAt": int(time.time() * 1000)}])
        p = self.factory("status", "--json", env={"FAKE_AGENTS": agents})
        self.assertEqual(p.returncode, 0, p.stderr)
        doc = json.loads(p.stdout)
        (row,) = doc["rows"]
        self.assertEqual((row["slug"], row["session"], row["session_id"], row["ahead"], row["behind"]),
                         ("demo-task", "done", "96dc4869", 0, 0))
        self.assertIn(row["pr_state"], ("unknown", "none"))
        self.assertEqual((doc["open"], doc["leftover_agent_worktrees"], doc["outside_worktrees"]), (1, 0, 0))

        p = self.factory("new", "demo-task", "--brief", "again", "--dispatcher", "d1")
        self.assertEqual(p.returncode, 2)

        # nothing committed on the branch, so it is merged into main: clean removes it via claude rm
        p = self.factory("clean", "--dry-run", env={"FAKE_AGENTS": agents})
        self.assertEqual(p.stdout.strip(), "would remove demo-task")
        self.assertTrue((self.repo / ".claude/worktrees/demo-task").is_dir())


if __name__ == "__main__":
    unittest.main()
