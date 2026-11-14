import contextlib
import io
import json
import os
import subprocess
import sys
import unittest
from datetime import datetime, timedelta, timezone

from tests.support import AGENTS, PROJECTS, ROOT
from isshoni import cli

BASE = ["--projects-dir", PROJECTS, "--agents-dir", AGENTS]


def run(*argv):
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        try:
            rc = cli.main(list(argv))
        except SystemExit as e:
            rc = e.code
    return rc, out.getvalue(), err.getvalue()


class CliTests(unittest.TestCase):
    def test_sessions_json(self):
        rc, out, _ = run("sessions", "--json", *BASE)
        self.assertEqual(rc, 0)
        d = json.loads(out)
        self.assertEqual(len(d["sessions"]), 1)
        s = d["sessions"][0]
        self.assertEqual((s["dispatch_count"], s["repeats"], s["review_loops"], s["warn_count"], s["malformed"]), (18, 1, 3, 1, 2))
        self.assertEqual(d["malformed"], 2)

    def test_session_json_full_data(self):
        rc, out, _ = run("session", "fixture", "--json", *BASE)
        self.assertEqual(rc, 0)
        d = json.loads(out)
        self.assertEqual(d["by_agent"]["main"]["total"], 300)
        self.assertEqual(set(d["by_model"]["claude-haiku-5-5"]), {"input", "output", "cache_write", "cache_read", "total"})
        self.assertEqual(len(d["dispatches"]), 18)
        e2 = next(x for x in d["dispatches"] if x["description"] == "Find config loader again")
        self.assertEqual(e2["flags"], ["repeat"])
        self.assertEqual(e2["repeat_of"], "toolu_E1")
        self.assertEqual(e2["status"], "running")
        self.assertEqual(len(d["warnings"]), 1)
        self.assertEqual(d["malformed"], 2)
        self.assertEqual(d["review_loops"], len(d["review_loop_events"]))
        self.assertEqual([e["via"] for e in d["review_loop_events"]], ["dispatch", "dispatch", "resume"])
        c1 = next(x for x in d["dispatches"] if x["description"] == "Apply fixes")
        self.assertEqual((c1["review_loops_dispatch"], c1["review_loops_resume"]), (1, 1))
        self.assertEqual(c1["flags"], ["review-loop", "review-loop(resume x1)"])

    def test_agents_json(self):
        rc, out, _ = run("agents", "--json", *BASE)
        self.assertEqual(rc, 0)
        d = json.loads(out)
        names = [a["agent"] for a in d["agents"]]
        self.assertEqual(names[0], "main")
        ex = next(a for a in d["agents"] if a["agent"] == "explorer")
        self.assertEqual(ex["dispatches"], 10)
        self.assertEqual(ex["usage"]["output"], 39)
        co = next(a for a in d["agents"] if a["agent"] == "coder")
        self.assertEqual(co["review_loops"], 3)  # 2 dispatch-triggered + 1 resume-triggered
        self.assertEqual(ex["warn_count"], 1)
        self.assertIn("claude-haiku-5-5", d["by_model"])

    def test_tables(self):
        rc, out, _ = run("session", "fixture", *BASE)
        self.assertEqual(rc, 0)
        self.assertIn("repeat,running", out)
        self.assertIn("WARN agent explorer expected model haiku", out)
        rc, out, _ = run("sessions", *BASE)
        self.assertEqual(rc, 0)
        self.assertIn("fixture-", out)
        rc, out, _ = run("agents", *BASE)
        self.assertEqual(rc, 0)
        self.assertIn("general-purpose", out)

    def test_usage_errors_exit_2(self):
        for argv in (("session", "nope", *BASE), ("sessions", "--since", "garbage", *BASE),
                     ("sessions", "--projects-dir", "/nonexistent-dir-xyz"), ("bogus",), ()):
            rc, _, _ = run(*argv)
            self.assertEqual(rc, 2, argv)

    def test_since_filters_old_sessions(self):
        rc, out, _ = run("sessions", "--json", "--since", "1h", *BASE)
        self.assertEqual(json.loads(out)["sessions"], [])
        rc, out, _ = run("sessions", "--json", "--since", "2020-01-01", *BASE)
        self.assertEqual(len(json.loads(out)["sessions"]), 1)

    def test_parse_since(self):
        now = datetime(2026, 1, 8, tzinfo=timezone.utc)
        self.assertEqual(cli.parse_since("7d", now), now - timedelta(days=7))
        self.assertEqual(cli.parse_since("2h", now), now - timedelta(hours=2))
        self.assertEqual(cli.parse_since("1w", now), now - timedelta(weeks=1))
        self.assertEqual(cli.parse_since("2026-01-02", now), datetime(2026, 1, 2, tzinfo=timezone.utc))

    def test_formatting(self):
        self.assertEqual(cli.human(1687683), "1.7M")
        self.assertEqual(cli.human(44082), "44.1k")
        self.assertEqual(cli.human(86), "86")
        self.assertEqual(cli.fmt_duration(269000), "4m29s")
        self.assertEqual(cli.fmt_duration(45000), "45s")

    def test_shim_json_is_clean(self):
        p = subprocess.run([sys.executable, os.path.join(ROOT, "bin", "isshoni"), "session", "fixture", "--json", *BASE],
                           capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        json.loads(p.stdout)


if __name__ == "__main__":
    unittest.main()
