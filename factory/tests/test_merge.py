import json
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from test_factory import ROOT, WT, FakeRun, factory, invoke, repo_answers, task_entry
from test_health import DISPATCHER, NOW, Harness, check, fired, iso, pic, row

SHA = "sha-1"
GREEN = [{"name": "test", "bucket": "pass"}]
SESSION = "dispatcher-repo"
COMMENTS = "repos/{owner}/{repo}/issues/%d/comments"
ACTIVITY = "repos/{owner}/{repo}/activity"


def pr(n, base="main", draft=False, fork=False, sha=SHA):
    return {"number": n, "url": f"https://x/pull/{n}", "isDraft": draft, "headRefName": f"worktree-t{n}",
            "headRefOid": sha, "baseRefName": base, "isCrossRepository": fork}


def raw(x):
    return x if isinstance(x, tuple) else (0, json.dumps(x), "")


def per(value, n):
    return value[n] if isinstance(value, dict) else value


def comment(body="lgtm", at=NOW - 60, login="andrei", n=1):
    return {"body": body, "created_at": iso(at), "html_url": f"https://x/pull/{n}#c{at}", "user": {"login": login}}


class World(Harness):
    """Open pull requests, one task per head branch, in a faked `gh`."""

    CRITERIA = ["lgtm-blocked", "base-merged"]

    def world(self, prs, *, checks=GREEN, mergeable="MERGEABLE", state="handed-over", tasks=None, local=SHA,
              extra=None):
        slugs = [f"t{p['number']}" for p in prs if not p["isCrossRepository"]] if tasks is None else tasks
        states = {s: (state[s] if isinstance(state, dict) else state) for s in slugs}
        self.seed("tasks", [{"repo": ROOT, "slug": s, "state": st, "ts": iso(NOW - 3600)} for s, st in states.items()])
        answers = {
            ("git", "status"): (0, "", ""), ("git", "rev-list"): (0, "0\t1\n", ""),
            ("git", "log"): (0, f"{NOW} {local}\n", ""),
            ("gh", "pr", "list", "--state", "open"): (0, json.dumps(prs), ""),
            ("gh", "pr", "edit"): (0, "", ""), ("gh", "pr", "ready"): (0, "", ""), ("gh", "pr", "merge"): (0, "", ""),
        }
        for s in slugs:
            n = int(s[1:])
            base = next(p["baseRefName"] for p in prs if p["number"] == n)
            answers[("gh", "pr", "list", "--head", f"worktree-{s}")] = (0, json.dumps([
                {"state": "OPEN", "url": f"https://x/pull/{n}", "headRefOid": SHA, "baseRefName": base}]), "")
        for p in prs:
            n = str(p["number"])
            answers[("gh", "pr", "checks", n)] = raw(per(checks, p["number"]))
            m = per(mergeable, p["number"])
            answers[("gh", "pr", "view", n)] = raw({"mergeable": m}) if isinstance(m, str) else (
                [raw({"mergeable": x}) for x in m] if isinstance(m, list) else m)
        answers.update(extra or {})
        finished = [{"id": f"id-{s}", "sessionId": f"sid-{s}", "cwd": f"{WT}/{s}", "kind": "background",
                     "status": "idle", "state": "done"} for s in slugs]
        return answers, [DISPATCHER | {"name": SESSION}, *finished], slugs

    def merge(self, *argv, prs, **kw):
        answers, sessions, slugs = self.world(prs, **{k: kw.pop(k) for k in list(kw) if k in (
            "checks", "mergeable", "state", "tasks", "local", "extra")})
        return self.cli("merge", *argv, answers=answers, sessions=sessions, entries=slugs)

    def gh_mutations(self):
        return [c[1:] for c in self.fake.calls if c[:3] in (["gh", "pr", "edit"], ["gh", "pr", "ready"],
                                                              ["gh", "pr", "merge"])]

    def pr_calls(self):
        return [c[1:] for c in self.fake.calls if c[:2] == ["gh", "pr"] and c[2] in
                ("checks", "view", "edit", "ready", "merge")]


class MergeCommandTests(World):
    def test_one_pull_request_merges_and_cleans(self):
        code, out, _ = self.merge("1", prs=[pr(1)])
        self.assertEqual(code, 0)
        self.assertEqual(self.pr_calls(), [
            ["pr", "checks", "1", "--json", "name,bucket"], ["pr", "view", "1", "--json", "mergeable"],
            ["pr", "merge", "1", "--merge", "--match-head-commit", SHA]])
        self.assertIn("#1 merged https://x/pull/1\n", out)
        self.assertIn("kept t1:", out)
        self.assertEqual(self.claude_runs(), [])

    def test_a_draft_is_marked_ready_first(self):
        code, _, _ = self.merge("1", prs=[pr(1, draft=True)])
        self.assertEqual(code, 0)
        self.assertEqual([c[:2] for c in self.pr_calls()][-2:], [["pr", "ready"], ["pr", "merge"]])

    def test_a_stack_named_in_reverse_merges_bottom_up_and_retargets(self):
        code, out, _ = self.merge("2", "1", prs=[pr(1), pr(2, base="worktree-t1")])
        self.assertEqual(code, 0)
        self.assertEqual(self.pr_calls(), [
            ["pr", "checks", "1", "--json", "name,bucket"], ["pr", "view", "1", "--json", "mergeable"],
            ["pr", "merge", "1", "--merge", "--match-head-commit", SHA],
            ["pr", "checks", "2", "--json", "name,bucket"], ["pr", "edit", "2", "--base", "main"],
            ["pr", "view", "2", "--json", "mergeable"], ["pr", "merge", "2", "--merge", "--match-head-commit", SHA]])
        self.assertLess(out.index("#1 merged"), out.index("#2 merged"))

    def test_a_base_merged_earlier_is_retargeted(self):
        merged = (0, json.dumps([{"state": "MERGED", "url": "u", "headRefOid": "z", "baseRefName": "main"}]), "")
        code, _, _ = self.merge("2", prs=[pr(2, base="worktree-t1")],
                                extra={("gh", "pr", "list", "--head", "worktree-t1"): merged})
        self.assertEqual(code, 0)
        self.assertEqual([c[:2] for c in self.pr_calls()], [["pr", "checks"], ["pr", "edit"], ["pr", "view"],
                                                            ["pr", "merge"]])

    def test_a_base_that_is_open_and_not_selected_is_a_stop(self):
        code, out, _ = self.merge("2", prs=[pr(1), pr(2, base="worktree-t1")])
        self.assertEqual(code, 1)
        self.assertIn("#2 stopped: its base worktree-t1 is not main", out)
        self.assertEqual(self.gh_mutations(), [])

    def test_a_base_merged_into_another_branch_is_a_stop(self):
        other = (0, json.dumps([{"state": "MERGED", "url": "u", "headRefOid": "z", "baseRefName": "dev"}]), "")
        code, out, _ = self.merge("2", prs=[pr(2, base="worktree-t1")],
                                  extra={("gh", "pr", "list", "--head", "worktree-t1"): other})
        self.assertEqual(code, 1)
        self.assertIn("#2 stopped: its base", out)

    def test_each_blocker(self):
        checks = lambda *buckets: [{"name": f"c{i}", "bucket": b} for i, b in enumerate(buckets)]
        cases = {
            "fork": (dict(prs=[pr(1, fork=True)]), "another repository"),
            "no task row": (dict(prs=[pr(1)], tasks=[]), "no task worktree has its head branch"),
            "motion": (dict(prs=[pr(1)], state="working"), "phase motion (declared working)"),
            "blocked": (dict(prs=[pr(1)], state="blocked"), "phase blocked (declared blocked)"),
            "local head differs": (dict(prs=[pr(1)], local="other"), "local commits that are not on the pull request"),
            "no checks": (dict(prs=[pr(1)], checks=[]), "no check passed"),
            "gh checks printed nothing": (dict(prs=[pr(1)], checks=(1, "", "no checks reported")),
                                          "no checks reported"),
            "failed": (dict(prs=[pr(1)], checks=checks("pass", "fail")), "failed or cancelled checks: c1"),
            "cancelled": (dict(prs=[pr(1)], checks=checks("pass", "cancel")), "failed or cancelled checks: c1"),
            "only skipped": (dict(prs=[pr(1)], checks=checks("skipping", "skipping")), "no check passed"),
            "pending": (dict(prs=[pr(1)], checks=checks("pass", "pending")), "checks are still running"),
            "pending, none passed": (dict(prs=[pr(1)], checks=checks("pending")), "checks are still running"),
            "pending and failed": (dict(prs=[pr(1)], checks=checks("fail", "pending")), "failed or cancelled"),
            "conflicting": (dict(prs=[pr(1)], mergeable="CONFLICTING"), "conflicts with its base"),
            "never computed": (dict(prs=[pr(1)], mergeable="UNKNOWN"), "not computed"),
            "view fails": (dict(prs=[pr(1)], mergeable=(1, "", "boom")), "boom"),
            "retarget fails": (dict(prs=[pr(2, base="worktree-t1")],
                                    extra={("gh", "pr", "list", "--head", "worktree-t1"): (0, json.dumps([
                                        {"state": "MERGED", "url": "u", "headRefOid": "z", "baseRefName": "main"}]), ""),
                                           ("gh", "pr", "edit"): (1, "", "no edit")}), "gh pr edit 2 --base main: no edit"),
            "ready fails": (dict(prs=[pr(1, draft=True)], extra={("gh", "pr", "ready"): (1, "", "no ready")}),
                            "gh pr ready 1: no ready"),
            "merge fails": (dict(prs=[pr(1)], extra={("gh", "pr", "merge"): (1, "", "head moved")}),
                            f"gh pr merge 1 --merge --match-head-commit {SHA}: head moved"),
        }
        for label, (kw, why) in cases.items():
            with self.subTest(label), mock.patch.object(factory.time, "sleep"):
                self.setUp()
                number = kw["prs"][0]["number"]
                code, out, _ = self.merge(str(number), **kw)
                self.assertEqual(code, 1)
                self.assertIn(f"#{number} stopped: ", out)
                self.assertIn(why, out)
                self.assertNotIn("merged https", out)
                self.assertNotIn("kept", out)
                if label not in ("retarget fails", "ready fails", "merge fails"):
                    self.assertEqual(self.gh_mutations(), [])

    def test_unknown_five_times_waits_between_asks(self):
        with mock.patch.object(factory.time, "sleep") as sleep:
            code, out, _ = self.merge("1", prs=[pr(1)], mergeable="UNKNOWN")
        self.assertEqual(code, 1)
        self.assertEqual(sum(c[:3] == ["gh", "pr", "view"] for c in self.fake.calls), 5)
        self.assertEqual(sleep.call_args_list, [mock.call(3)] * 4)
        self.assertIn("#1 stopped: GitHub has not computed", out)

    def test_unknown_then_mergeable_merges(self):
        with mock.patch.object(factory.time, "sleep") as sleep:
            code, out, _ = self.merge("1", prs=[pr(1)], mergeable=["UNKNOWN", "MERGEABLE"])
        self.assertEqual(code, 0)
        self.assertEqual(sleep.call_count, 1)
        self.assertIn("#1 merged", out)

    def test_a_stop_ends_the_run(self):
        prs = [pr(1), pr(2), pr(3)]
        code, out, _ = self.merge("1", "2", "3", prs=prs, checks={1: GREEN, 2: [{"name": "t", "bucket": "fail"}], 3: GREEN})
        self.assertEqual(code, 1)
        self.assertIn("#1 merged", out)
        self.assertIn("#2 stopped: failed or cancelled checks: t\n#3 not attempted\n", out)
        self.assertEqual([c[1] for c in self.gh_mutations()], ["merge"])
        self.assertEqual(self.gh_mutations()[0][2], "1")
        self.assertIn("kept t1:", out)

    def test_nothing_merged_runs_no_clean(self):
        code, out, _ = self.merge("1", "2", prs=[pr(1), pr(2)], checks=[])
        self.assertEqual(code, 1)
        self.assertIn("#2 not attempted", out)
        self.assertNotIn("kept", out)

    def test_all_skips_tasks_not_handed_over(self):
        code, out, _ = self.merge("--all", prs=[pr(1), pr(2), pr(3)], state={"t1": "handed-over", "t2": "working",
                                                                             "t3": "handed-over"}, tasks=["t1", "t2", "t3"])
        self.assertEqual(code, 0)
        self.assertIn("#2 skipped: task t2 is in phase motion (declared working)\n", out)
        self.assertEqual([c[1] for c in self.gh_mutations()], ["merge", "merge"])
        self.assertNotIn("stopped", out)

    def test_a_named_number_that_is_not_open_exits_2(self):
        code, _, err = self.merge("9", prs=[pr(1)])
        self.assertEqual(code, 2)
        self.assertIn("#9 is not an open pull request", err)
        self.assertEqual(self.gh_mutations(), [])

    def test_usage(self):
        for argv in ((), ("1", "--all")):
            with self.subTest(argv):
                self.assertEqual(self.merge(*argv, prs=[pr(1)])[0], 2)
                self.assertEqual(self.gh_mutations(), [])

    def test_sessions_unlistable_exits_1_with_nothing_done(self):
        code, _, err = self.merge("1", prs=[pr(1)], extra={("claude", "agents"): (1, "", "boom")})
        self.assertEqual(code, 1)
        self.assertIn("nothing done", err)
        self.assertEqual(self.pr_calls(), [])

    def test_gh_pr_list_failing_exits_1_with_nothing_done(self):
        code, _, err = self.merge("1", prs=[pr(1)], extra={("gh", "pr", "list", "--state", "open"): (1, "", "boom")})
        self.assertEqual(code, 1)
        self.assertIn("boom", err)
        self.assertEqual(self.pr_calls(), [])


class DryRunTests(World):
    def test_dry_run_changes_nothing_and_continues_past_a_stop(self):
        prs = [pr(1, draft=True), pr(2, base="worktree-t1"), pr(3), pr(4)]
        code, out, _ = self.merge("--dry-run", "--all", prs=prs,
                                  checks={1: GREEN, 2: GREEN, 3: [{"name": "t", "bucket": "fail"}], 4: GREEN})
        self.assertEqual(code, 1)
        self.assertEqual(out.splitlines(), [
            "#1 would merge (draft: would be marked ready)",
            "#2 would merge (retarget onto main first; mergeability is checked after that)",
            "#3 would stop: failed or cancelled checks: t",
            "#4 would merge"])
        self.assertEqual(self.gh_mutations(), [])
        self.assertFalse(any(c[:3] == ["gh", "pr", "view"] and c[3] == "2" for c in self.fake.calls))
        self.assertFalse(self.fake.ran("claude", "rm"))

    def test_dry_run_exits_0_when_all_would_merge(self):
        code, out, _ = self.merge("1", "--dry-run", prs=[pr(1)])
        self.assertEqual((code, out), (0, "#1 would merge\n"))


def approval_gh(pages, permissions, asked=None):
    def gh(kind, *args):
        if asked is not None:
            asked.append(args)
        if "--slurp" in args:
            return pages
        return {"permission": permissions[args[1].split("/")[4]]}
    return gh


class ApprovalTests(unittest.TestCase):
    def approval(self, *comments, permissions=None, asked=None):
        p = {"gh": approval_gh([list(comments)], permissions or {"andrei": "admin"}, asked)}
        return factory.newest_approval(p, pr(1), {})

    def test_body_variants(self):
        cases = {"lgtm": True, " LGTM\n": True, "Lgtm": True, "lgtm!": False, "lgtm, thanks": False,
                 "not lgtm": False, "": False}
        for body, approved in cases.items():
            with self.subTest(body=body):
                self.assertEqual(bool(self.approval(comment(body))), approved)

    def test_a_commenter_without_write_access_is_ignored(self):
        self.assertIsNone(self.approval(comment(login="octocat"), permissions={"octocat": "read"}))
        self.assertIsNone(self.approval(comment(login="octocat"), permissions={"octocat": "none"}))
        self.assertTrue(self.approval(comment(login="dev"), permissions={"dev": "write"}))

    def test_the_newest_approval_wins_over_pages_and_order(self):
        p = {"gh": approval_gh([[comment(at=NOW - 10)], [comment(at=NOW - 500), comment("hello", at=NOW)]],
                               {"andrei": "admin"})}
        self.assertEqual(factory.newest_approval(p, pr(1), {})["created_at"], iso(NOW - 10))

    def test_permission_is_asked_once_per_login(self):
        asked = []
        self.approval(comment(at=NOW - 3), comment(at=NOW - 2), comment(at=NOW - 1), asked=asked)
        self.assertEqual(sum("collaborators" in a[1] for a in asked), 1)
        self.assertIn(("api", "repos/{owner}/{repo}/collaborators/andrei/permission"), asked)

    def test_permission_is_not_asked_for_other_comments(self):
        asked = []
        self.approval(comment("looks good"), asked=asked)
        self.assertEqual(sum("collaborators" in a[1] for a in asked), 0)

    def test_an_unexpected_shape_is_not_evaluated(self):
        p = {"gh": lambda kind, *args: [{"body": "lgtm"}]}
        with self.assertRaises(factory.NotEvaluated):
            factory.newest_approval(p, pr(1), {})

    def stale(self, approved, activity):
        p = {"gh": lambda kind, *args: activity}
        return factory.stale_approval(p, pr(1), comment(at=approved))

    def test_the_approval_against_the_activity_timestamp(self):
        change = {"after": SHA, "timestamp": iso(NOW - 100)}
        self.assertIsNone(self.stale(NOW - 99, [change]))
        self.assertEqual(self.stale(NOW - 100, [change])[0], "stale")
        self.assertEqual(self.stale(NOW - 101, [change])[0], "stale")

    def test_the_activity_must_name_the_head(self):
        self.assertEqual(self.stale(NOW, [])[0], "head-time")
        self.assertEqual(self.stale(NOW, [{"after": "other", "timestamp": iso(NOW - 100)}])[0], "head-time")
        self.assertEqual(self.stale(NOW, [{"after": SHA}])[0], "head-time")


class LgtmTests(World):
    def lgtm_world(self, prs, comments, activity=None, **kw):
        answers, sessions, slugs = self.world(prs, **kw)
        for p in prs:
            n = p["number"]
            answers[("gh", "api", "--paginate", "--slurp", COMMENTS % n)] = raw([comments.get(n, [])])
            answers[("gh", "api", "-X", "GET", ACTIVITY, "-f", f"ref=refs/heads/worktree-t{n}")] = raw(
                (activity or {}).get(n, [{"after": SHA, "timestamp": iso(NOW - 3600)}]))
        answers[("gh", "api", "repos/{owner}/{repo}/collaborators/andrei/permission")] = raw({"permission": "admin"})
        answers.update(kw.get("extra") or {})
        return answers, sessions, slugs

    def tick_lgtm(self, prs, comments, command="watch", at=None, **kw):
        answers, sessions, slugs = self.lgtm_world(prs, comments, **kw)
        return self.cli(command, answers=answers, sessions=sessions, entries=slugs, at=at)

    def test_an_approved_green_pull_request_merges_without_a_model(self):
        code, out, _ = self.tick_lgtm([pr(1)], {1: [comment()]})
        self.assertEqual(code, 0)
        self.assertIn(f"#1 merged on lgtm https://x/pull/1#c{NOW - 60}\n", out)
        self.assertIn(["gh", "pr", "merge", "1", "--merge", "--match-head-commit", SHA], self.fake.calls)
        self.assertIn("kept t1:", out)
        self.assertEqual(self.claude_runs(), [])
        self.assertEqual(self.records(), [])

    def test_no_approval_does_nothing(self):
        code, out, _ = self.tick_lgtm([pr(1)], {1: [comment("lgtm, thanks"), comment(login="octocat")]},
                                      extra={("gh", "api", "repos/{owner}/{repo}/collaborators/octocat/permission"):
                                             raw({"permission": "read"})})
        self.assertEqual(code, 0)
        self.assertEqual(self.gh_mutations(), [])
        self.assertNotIn("blocked", out)
        self.assertEqual(self.records(), [])

    def test_a_stale_approval_fires_is_sent_and_not_merged(self):
        code, out, _ = self.tick_lgtm([pr(1)], {1: [comment(at=NOW - 7200)]})
        self.assertEqual(code, 0)
        self.assertIn("#1 blocked: its head changed after the lgtm", out)
        self.assertEqual(self.gh_mutations(), [])
        [record] = self.records()
        self.assertEqual((record["criterion"], record["escalate"], record["slug"], record["subject"]),
                         ("lgtm-blocked", "always", "t1", "#1/stale"))
        self.assertIn("https://x/pull/1", record["detail"])
        [argv] = self.claude_runs()
        [sent] = json.loads(argv[-1].partition("\n\n")[2])
        self.assertEqual((sent["id"], sent["subject"]), (record["id"], "#1/stale"))

    def test_pending_fires_nothing(self):
        code, out, _ = self.tick_lgtm([pr(1)], {1: [comment()]}, checks=[{"name": "t", "bucket": "pending"}])
        self.assertEqual(code, 0)
        self.assertIn("#1 blocked: checks are still running", out)
        self.assertEqual((self.records(), self.claude_runs(), self.gh_mutations()), ([], [], []))

    def test_unknown_mergeability_fires_nothing(self):
        with mock.patch.object(factory.time, "sleep"):
            self.tick_lgtm([pr(1)], {1: [comment()]}, mergeable="UNKNOWN")
        self.assertEqual((self.records(), self.gh_mutations()), ([], []))

    def test_a_blocker_of_another_kind_is_a_new_firing_and_the_old_one_clears(self):
        self.tick_lgtm([pr(1)], {1: [comment(at=NOW - 7200)]})
        [first] = self.records()
        self.tick_lgtm([pr(1)], {1: [comment(at=NOW - 7200)]}, activity={1: []}, at=NOW + 60)
        records = self.records()
        self.assertIn({"ts": iso(NOW + 60), "repo": ROOT, "id": first["id"], "cleared": True}, records)
        second = next(r for r in records if r.get("criterion") and r["id"] != first["id"])
        self.assertEqual(second["subject"], "#1/head-time")
        self.assertEqual(len(self.claude_runs()), 1)

    def test_the_same_blocker_is_not_sent_again(self):
        self.tick_lgtm([pr(1)], {1: [comment(at=NOW - 7200)]})
        self.seed("firings", [{"ts": iso(NOW), "repo": ROOT, "id": self.records()[0]["id"], "decision": "escalated",
                               "reason": "r", "by": "site-manager"}])
        self.tick_lgtm([pr(1)], {1: [comment(at=NOW - 7200)]}, at=NOW + 60)
        self.assertEqual(self.claude_runs(), [])
        self.assertEqual(len([r for r in self.records() if r.get("criterion")]), 1)

    def test_a_merge_clears_the_firing(self):
        self.seed("firings", [fired("aaa", criterion="lgtm-blocked", subject="#1/stale")])
        code, out, _ = self.tick_lgtm([pr(1)], {1: [comment()]})
        self.assertIn("#1 merged on lgtm", out)
        self.assertIn({"ts": iso(NOW), "repo": ROOT, "id": "aaa", "cleared": True}, self.records())
        self.assertEqual(self.claude_runs(), [])

    def test_one_blocked_pull_request_does_not_stop_another(self):
        code, out, _ = self.tick_lgtm([pr(1), pr(2)], {1: [comment(at=NOW - 7200)], 2: [comment(n=2)]})
        self.assertIn("#1 blocked: its head changed", out)
        self.assertIn("#2 merged on lgtm", out)
        self.assertEqual([c[2] for c in self.gh_mutations() if c[1] == "merge"], ["2"])

    def test_a_stack_merges_in_one_tick(self):
        code, out, _ = self.tick_lgtm([pr(2, base="worktree-t1"), pr(1)], {1: [comment()], 2: [comment(n=2)]})
        self.assertEqual([c[:2] for c in self.pr_calls() if c[1] != "view" and c[1] != "checks"],
                         [["pr", "merge"], ["pr", "edit"], ["pr", "merge"]])

    def test_a_failing_comments_call_is_not_evaluated_and_merges_nothing_further(self):
        code, out, _ = self.tick_lgtm([pr(1), pr(2)], {2: [comment(n=2)]},
                                      extra={("gh", "api", "--paginate", "--slurp", COMMENTS % 1): (1, "", "boom")})
        self.assertEqual(code, 0)
        self.assertIn("not evaluated: lgtm-blocked: gh api --paginate failed: boom", out)
        self.assertEqual((self.gh_mutations(), self.records()), ([], []))

    def test_failing_calls_leave_open_firings_alone(self):
        self.seed("firings", [fired("aaa", criterion="lgtm-blocked", subject="#1/stale")])
        calls = {
            "pull request list": {("gh", "pr", "list", "--state", "open"): (1, "", "boom")},
            "permission": {("gh", "api", "repos/{owner}/{repo}/collaborators/andrei/permission"): (1, "", "boom")},
            "activity": {("gh", "api", "-X", "GET", ACTIVITY, "-f", "ref=refs/heads/worktree-t1"): (1, "", "boom")},
        }
        for label, extra in calls.items():
            with self.subTest(label):
                _, out, _ = self.tick_lgtm([pr(1)], {1: [comment()]}, extra=extra)
                self.assertIn("not evaluated: lgtm-blocked", out)
                self.assertEqual(self.gh_mutations(), [])
                self.assertNotIn({"ts": iso(NOW), "repo": ROOT, "id": "aaa", "cleared": True}, self.records())

    def test_health_checks_without_changing_anything(self):
        prs, comments = [pr(1), pr(2)], {1: [comment()], 2: [comment(at=NOW - 7200, n=2)]}
        answers, sessions, slugs = self.lgtm_world(prs, comments)
        before = {p.name: p.read_bytes() for p in self.store.iterdir()}
        code, out, _ = self.cli("health", answers=answers, sessions=sessions, entries=slugs)
        self.assertEqual(code, 0)
        self.assertIn("lgtm-blocked  always  #2/stale  its head changed after the lgtm; a fresh lgtm is needed "
                      "https://x/pull/2", out)
        self.assertNotIn("#1/", out)
        self.assertEqual(self.gh_mutations(), [])
        self.assertFalse(self.fake.ran("claude", "rm"))
        self.assertEqual({p.name: p.read_bytes() for p in self.store.iterdir()}, before)


class BaseMergedTests(unittest.TestCase):
    REPO = SimpleNamespace(root=ROOT, default="main", remote="origin")
    MERGED = {("gh", "pr", "list", "--head", "worktree-base"): (0, json.dumps([
        {"state": "MERGED", "url": "https://x/pull/5", "headRefOid": "z", "baseRefName": "main"}]), "")}

    def found(self, phase="motion", base="worktree-base", answers=None, **kw):
        p = pic(repo=self.REPO, rows=[row("x", phase=phase, stacked_on=base, **kw)])
        with mock.patch.object(factory, "run", FakeRun(answers or self.MERGED)):
            return check("base-merged", p)

    def test_fires_for_motion_and_blocked(self):
        for phase in ("motion", "blocked"):
            with self.subTest(phase):
                self.assertEqual(self.found(phase), [(
                    "x", "x", "its base worktree-base merged (https://x/pull/5): tell its lead to rebase onto "
                    "origin/main and to open or retarget its pull request against main")])

    def test_does_not_fire_otherwise(self):
        open_base = {("gh", "pr", "list", "--head", "worktree-base"): (0, json.dumps([
            {"state": "OPEN", "url": "u", "headRefOid": "z", "baseRefName": "main"}]), "")}
        elsewhere = {("gh", "pr", "list", "--head", "worktree-base"): (0, json.dumps([
            {"state": "MERGED", "url": "u", "headRefOid": "z", "baseRefName": "dev"}]), "")}
        self.assertEqual(self.found("review"), [])
        self.assertEqual(self.found("settled"), [])
        self.assertEqual(self.found(answers=open_base), [])
        self.assertEqual(self.found(answers=elsewhere), [])
        self.assertEqual(self.found(base=None), [])
        self.assertEqual(self.found(pr_state="merged", pr_base="main"), [])
        self.assertEqual(self.found(pr_state="open", pr_base="main"), [])
        self.assertEqual(len(self.found(pr_state="open", pr_base="worktree-base")), 1)

    def test_an_unknown_base_state_is_not_evaluated(self):
        with self.assertRaises(factory.NotEvaluated):
            self.found(answers={("gh", "pr", "list"): (1, "", "boom")})


class SharedCodeTests(unittest.TestCase):
    def test_the_plugins_own_worktree_is_kept(self):
        with mock.patch.object(factory, "PLUGIN_ROOT", Path(f"{WT}/x/factory")):
            reason = factory.keep_reason(None, {"path": f"{WT}/x"}, [])
        self.assertEqual(reason, "this factory runs from it; leads started by it load their plugin from there")

    def test_another_worktree_is_judged_as_before(self):
        with mock.patch.object(factory, "PLUGIN_ROOT", Path(f"{WT}/xy/factory")):
            reason = factory.keep_reason(None, {"path": f"{WT}/x", "branch": None}, [])
        self.assertEqual(reason, "detached HEAD, no branch to judge")

    def test_collect_sets_stacked_on(self):
        for recorded, want in (("worktree-y\n", "worktree-y"), ("", None)):
            with self.subTest(recorded):
                answers = repo_answers([task_entry("x")], [], {
                    ("git", "status"): (0, "", ""), ("git", "rev-list"): (0, "0\t1\n", ""),
                    ("git", "log"): (0, f"{NOW} h\n", ""), ("gh",): (0, "[]", ""),
                    ("git", "config"): (0, recorded, "")})
                code, out, _, _ = invoke(["status", "--json"], answers)
                self.assertEqual(json.loads(out)["rows"][0]["stacked_on"], want)


if __name__ == "__main__":
    unittest.main()
