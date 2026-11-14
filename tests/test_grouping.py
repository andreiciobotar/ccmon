import json
import os
import unittest

from tests.support import AGENTS, HERE, PROJECTS
from tests.test_cli import run
from ccmon import transcript

GROUPING = os.path.join(HERE, "fixtures", "grouping")
BASE = ["--projects-dir", GROUPING, "--agents-dir", AGENTS]
TOTAL_KEYS = ("start", "end", "tokens", "by_model", "by_agent", "dispatch_count", "repeats",
              "review_loops", "recheck", "warn_count", "malformed")
ALPHA = "/r/alpha"


def sessions_json(*extra):
    rc, out, _ = run("sessions", "--json", *BASE, *extra)
    assert rc == 0
    return json.loads(out)


def by_prefix(d):
    return {s["id"]: s for s in d["sessions"]}


def add(into, other):
    for k, v in other.items():
        if isinstance(v, dict):
            add(into.setdefault(k, {}), v)
        else:
            into[k] = into.get(k, 0) + v


class AttributionTests(unittest.TestCase):
    def test_each_case(self):
        s = by_prefix(sessions_json())
        got = {i: (x["repo"], x["repo_path"], x["task"], x["worktrees"]) for i, x in s.items()}
        self.assertEqual(got, {
            "a1000001": ("alpha", ALPHA, None, []),                # no worktree-state, plain cwd
            "a1000002": ("alpha", ALPHA, "ci", ["ci"]),            # repeated record and a null record
            "a1000003": ("alpha", ALPHA, "ci", ["ci"]),            # cwd in the repo, worktree-state decides
            "a1000004": ("alpha", ALPHA, "y", ["x", "y"]),         # switched: last one wins, both listed
            "a1000005": ("alpha", ALPHA, "inwt", ["inwt"]),        # originalCwd is the worktree path
            "a1000006": ("alpha", ALPHA, None, []),                # only an agent-<hex> worktree
            "a1000007": ("alpha", ALPHA, "cwdonly", []),           # no worktree-state, cwd inside a worktree
            "b1000001": ("beta", "/r/beta", None, []),
            "c1000001": ("-no-cwd", None, None, []),               # no cwd: project directory name
        })

    def test_unit_rules(self):
        att = transcript.attribute
        self.assertEqual(att([], "/r/a/.claude/worktrees/agent-1f/sub"), ("/r/a", None, []))
        self.assertEqual(att([], "/r/a/"), ("/r/a", None, []))
        self.assertEqual(att([("/h/wt", "hooked", "/r/a")], None), ("/r/a", "hooked", ["hooked"]))
        self.assertEqual(att([("/r/a/.claude/worktrees/x", "x", "/r/a"), ("/r/a/.claude/worktrees/agent-9", "agent-9", "/r/a")],
                             "/r/a"), ("/r/a", "x", ["x"]))
        self.assertEqual(att([], None), (None, None, []))

    def test_project_keeps_its_meaning(self):
        s = by_prefix(sessions_json())
        self.assertEqual(s["a1000005"]["project"], "inwt")
        self.assertEqual(s["a1000005"]["cwd"], ALPHA + "/.claude/worktrees/inwt")


class RepoTotalsTests(unittest.TestCase):
    def setUp(self):
        self.d = sessions_json()
        self.s = by_prefix(self.d)

    def check_sum(self, group, ids):
        self.assertEqual(group["sessions"], ids)
        self.assertEqual(group["session_count"], len(ids))
        rows = [self.s[i] for i in ids]
        for k in ("by_model", "by_agent"):
            want = {}
            for r in rows:
                add(want, r[k])
            self.assertEqual(group[k], want, k)
        for k in ("dispatch_count", "repeats", "review_loops", "warn_count", "malformed"):
            self.assertEqual(group[k], sum(r[k] for r in rows), k)
        for k in ("total", "main", "subagents"):
            self.assertEqual(group["tokens"][k], sum(r["tokens"][k] for r in rows), k)
        self.assertEqual(group["start"], min(r["start"] for r in rows))
        self.assertEqual(group["end"], max(r["end"] for r in rows))
        rc = group["recheck"]
        self.assertEqual(rc["explorer_with_citations"], sum(r["recheck"]["explorer_with_citations"] for r in rows))
        self.assertEqual(rc["rechecked"], sum(r["recheck"]["rechecked"] for r in rows))
        self.assertEqual(rc["rate"], rc["rechecked"] / rc["explorer_with_citations"] if rc["explorer_with_citations"] else None)

    def test_sums_and_order(self):
        repos = self.d["repos"]
        self.assertEqual([(r["repo"], r["repo_path"]) for r in repos],
                         [("-no-cwd", None), ("alpha", ALPHA), ("beta", "/r/beta")])
        alpha = repos[1]
        self.check_sum(alpha, ["a1000001", "a1000002", "a1000003", "a1000004", "a1000005", "a1000006", "a1000007"])
        self.assertEqual([t["task"] for t in alpha["tasks"]], ["ci", "cwdonly", "inwt", "y"])
        ci = alpha["tasks"][0]
        self.check_sum(ci, ["a1000002", "a1000003"])
        self.assertEqual(set(ci["by_agent"]), {"main", "coder", "explorer", "reviewer"})
        self.assertEqual(set(ci["by_model"]), {"claude-fable-5-2", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-5-5"})
        self.assertGreater(alpha["tokens"]["total"], sum(t["tokens"]["total"] for t in alpha["tasks"]))  # taskless sessions count too
        for r in repos:
            self.check_sum(r, r["sessions"])

    def test_group_of_one_equals_session(self):
        y = next(t for t in self.d["repos"][1]["tasks"] if t["task"] == "y")
        s = self.s["a1000004"]
        self.assertEqual(y["sessions"], ["a1000004"])
        for k in TOTAL_KEYS:
            self.assertEqual(y[k], s[k], k)


class CliTests(unittest.TestCase):
    def test_repo_task_filters(self):
        d = sessions_json("--repo", "alpha", "--task", "ci")
        self.assertEqual([s["id"] for s in d["sessions"]], ["a1000002", "a1000003"])
        self.assertEqual([r["repo"] for r in d["repos"]], ["alpha"])
        self.assertEqual([s["id"] for s in sessions_json("--repo", "beta")["sessions"]], ["b1000001"])
        self.assertEqual(sessions_json("--task", "nope")["sessions"], [])
        rc, out, _ = run("agents", "--json", "--repo", "alpha", "--task", "ci", *BASE)
        self.assertEqual(rc, 0)
        a = json.loads(out)
        self.assertEqual(a["sessions"], 2)
        self.assertEqual([x["agent"] for x in a["agents"]], ["main", "coder", "explorer", "reviewer"])

    def test_grouped_table(self):
        rc, out, _ = run("sessions", *BASE)
        self.assertEqual(rc, 0)
        lines = out.splitlines()
        self.assertTrue(lines[0].startswith("repo / task / session"))
        first = [ln.split()[0] for ln in lines[1:-2]]
        self.assertEqual(first, ["-no-cwd", "c1000001", "alpha", "a1000001", "a1000006", "ci", "a1000002", "a1000003",
                                 "cwdonly", "a1000007", "inwt", "a1000005", "y", "a1000004", "beta", "b1000001"])
        indent = {ln.split()[0]: len(ln) - len(ln.lstrip()) for ln in lines[1:-2]}
        self.assertEqual((indent["alpha"], indent["a1000001"], indent["ci"], indent["a1000002"]), (0, 2, 2, 4))
        self.assertEqual(lines[-1], "3 repositories, 4 tasks, 9 sessions, 0 malformed lines")

    def test_session_text(self):
        rc, out, _ = run("session", "a1000004", *BASE)
        self.assertEqual(rc, 0)
        self.assertIn("repo      alpha (/r/alpha)", out)
        self.assertIn("task      y (also entered: x)", out)
        rc, out, _ = run("session", "c1000001", *BASE)
        self.assertIn("repo      -no-cwd (-no-cwd)", out)
        self.assertIn("task      -\n", out)

    def test_old_fixture_keeps_old_keys_and_values(self):
        rc, out, _ = run("sessions", "--json", "--projects-dir", PROJECTS, "--agents-dir", AGENTS)
        s = json.loads(out)["sessions"][0]
        self.assertEqual((s["project"], s["project_dir"], s["cwd"]), ("cwd", "-fixture-proj", "/fixture/cwd"))
        self.assertEqual(s["tokens"]["total"], s["by_agent"]["main"]["total"] + sum(
            v["total"] for k, v in s["by_agent"].items() if k != "main"))
        for k in ("id", "path", "start", "end", "tokens", "by_model", "by_agent", "dispatch_count", "repeats",
                  "review_loops", "review_loop_events", "recheck", "warn_count", "warnings", "malformed"):
            self.assertIn(k, s)
        self.assertEqual((s["repo"], s["repo_path"], s["task"], s["worktrees"]), ("cwd", "/fixture/cwd", None, []))


if __name__ == "__main__":
    unittest.main()
