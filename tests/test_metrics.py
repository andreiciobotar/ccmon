import unittest

from tests.support import AGENTS, by_desc, load
from ccmon import metrics


class MetricsTests(unittest.TestCase):
    def test_repeat_flagged_at_default_threshold(self):
        s = load(0.5)
        d = by_desc(s, "Find config loader again")
        self.assertIn("repeat", d.flags)
        self.assertEqual(d.repeat_of.description, "Find config loader")
        self.assertGreaterEqual(d.repeat_score, 0.5)
        self.assertNotIn("repeat", by_desc(s, "Map database migrations").flags)
        self.assertNotIn("repeat", by_desc(s, "Second pass").flags)

    def test_repeat_threshold_both_sides(self):
        score = by_desc(load(0.5), "Find config loader again").repeat_score
        self.assertIn("repeat", by_desc(load(score), "Find config loader again").flags)  # equal passes
        self.assertNotIn("repeat", by_desc(load(score + 0.01), "Find config loader again").flags)

    def test_jaccard(self):
        self.assertEqual(metrics.jaccard({"a", "b"}, {"b", "c"}), 1 / 3)
        self.assertEqual(metrics.jaccard(set(), set()), 0.0)

    def test_review_loop(self):
        s = load()
        # R1 verdict -> "Apply fixes"; hand-back verdict -> "Second fix round";
        # hand-back verdict -> SendMessage to aC1 (consumed once); "Third fix round" has nothing left to consume
        self.assertEqual(s.review_loops, 3)
        self.assertIn("review-loop", by_desc(s, "Apply fixes").flags)
        self.assertIn("review-loop", by_desc(s, "Second fix round").flags)
        self.assertNotIn("review-loop", by_desc(s, "Third fix round").flags)
        self.assertNotIn("review-loop", by_desc(s, "Second pass").flags)

    def test_review_loop_events_and_counts(self):
        s = load()
        ev = s.review_loop_events
        self.assertEqual(len(ev), s.review_loops)
        self.assertEqual([e["via"] for e in ev], ["dispatch", "dispatch", "resume"])
        self.assertEqual([e["at"] for e in ev], sorted(e["at"] for e in ev))
        self.assertEqual((ev[0]["reviewer_tool_use_id"], ev[0]["coder_tool_use_id"]), ("toolu_R1", "toolu_C1"))
        self.assertEqual((ev[2]["reviewer_agent_id"], ev[2]["coder_agent_id"]), ("aHR2", "aC1"))
        c1 = by_desc(s, "Apply fixes")
        self.assertEqual((c1.review_loops_dispatch, c1.review_loops_resume), (1, 1))
        c2 = by_desc(s, "Second fix round")
        self.assertEqual((c2.review_loops_dispatch, c2.review_loops_resume), (1, 0))
        self.assertEqual(c2.flags.count("review-loop"), 1)
        self.assertNotIn("review-loop(resume x1)", c2.flags)
        self.assertIn("review-loop(resume x1)", c1.flags)
        self.assertEqual(by_desc(s, "Third fix round").flags, [])

    def test_handback_verdict_drives_loop(self):
        s = load()
        self.assertTrue(by_desc(s, "Handback review").results)
        self.assertIn("review-loop", by_desc(s, "Second fix round").flags)

    def test_send_message_consumes_verdict(self):
        s = load()
        s.sends = []  # without the SendMessage the second hand-back verdict is consumed by "Third fix round"
        metrics.analyze(s, AGENTS, 0.5)
        self.assertEqual(s.review_loops, 3)
        self.assertIn("review-loop", by_desc(s, "Third fix round").flags)

    def test_verdict_parsing(self):
        f = metrics._is_changes_needed
        self.assertTrue(f("intro\n## Verdict\n\nCHANGES_NEEDED\n"))
        self.assertTrue(f("## Verdict: CHANGES_NEEDED\n\nbody"))
        self.assertFalse(f("## Verdict: APPROVE\nCHANGES_NEEDED is mentioned later"))
        self.assertFalse(f("## Verdict\nAPPROVE\n\nno CHANGES_NEEDED left"))
        self.assertTrue(f("no heading, but CHANGES_NEEDED here"))
        self.assertFalse(f("all good"))

    def test_recheck(self):
        s = load()
        self.assertIn("rechecked", by_desc(s, "Find config loader").flags)
        self.assertIn("rechecked", by_desc(s, "Helper lookup").flags)  # hand-back explorer, later Read
        self.assertEqual((s.recheck_candidates, s.rechecked), (3, 2))
        self.assertAlmostEqual(metrics.totals([s])["recheck"]["rate"], 2 / 3)

    def test_recheck_path_boundary(self):
        s = load()
        # cited loader.py:12; the only later Read is /x/myloader.py, which must not match
        self.assertNotIn("rechecked", by_desc(s, "Entry point lookup").flags)

    def test_recheck_requires_read_after_return(self):
        s = load()
        s.reads = [(ts.replace(second=ts.second - 30) if ts else ts, p) for ts, p in s.reads]  # before 00:40
        metrics.analyze(s, AGENTS, 0.5)
        self.assertEqual(s.rechecked, 0)

    def test_model_warn(self):
        s = load()
        d = by_desc(s, "Map database migrations")
        self.assertIn("WARN", d.flags)
        self.assertEqual(len(s.warnings), 1)
        self.assertIn("explorer", s.warnings[0])
        self.assertIn("haiku", s.warnings[0])
        self.assertIn("claude-sonnet-5-5", s.warnings[0])
        self.assertEqual(by_desc(s, "Apply fixes").warnings, [])

    def test_model_matches(self):
        self.assertTrue(metrics.model_matches("haiku", "claude-haiku-5-5"))
        self.assertFalse(metrics.model_matches("haiku", "claude-opus-5-5"))
        self.assertTrue(metrics.model_matches("inherit", "anything"))
        self.assertTrue(metrics.model_matches("claude-opus-5-5[1m]", "claude-opus-5-5"))
        self.assertFalse(metrics.model_matches("claude-opus-5-5", "claude-opus-5-4"))

    def test_expected_model_missing_definition(self):
        self.assertIsNone(metrics.expected_model("general-purpose", "/nonexistent", AGENTS))
        self.assertEqual(metrics.expected_model("coder", "/nonexistent", AGENTS), "sonnet")

    def test_percentile(self):
        self.assertEqual(metrics.percentile([1, 2, 3, 4], 50), 2.5)
        self.assertEqual(metrics.percentile([10], 90), 10.0)
        self.assertAlmostEqual(metrics.percentile([0, 10], 90), 9.0)
        self.assertIsNone(metrics.percentile([], 50))

    def test_summary_shares(self):
        x = metrics.session_summary(load())
        self.assertEqual(x["tokens"]["total"], 670)
        self.assertEqual(x["tokens"]["main"], 300)
        self.assertEqual(x["dispatch_count"], 18)


if __name__ == "__main__":
    unittest.main()
