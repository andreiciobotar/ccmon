"""A plugin's agents (<plugin>:<name>) count as their role; see docs/specs/plugin-agent-names.md."""
import os
import tempfile
import unittest

from tests.support import AGENTS, HERE, by_desc
from ccmon import metrics, transcript

SESSION = os.path.join(HERE, "fixtures", "plugin", "-plug-proj", "plug-0001.jsonl")


def load(agents_dir=AGENTS):
    s = transcript.load_session(SESSION)
    metrics.analyze(s, agents_dir, 0.5)
    return s


class PluginAgentTests(unittest.TestCase):
    def test_role(self):
        self.assertEqual(metrics.role("factory:coder"), "coder")
        self.assertEqual(metrics.role("coder"), "coder")

    def test_full_name_is_kept(self):
        x = metrics.session_summary(load())
        self.assertEqual(sorted(x["by_agent"]), ["factory:coder", "factory:explorer", "factory:reviewer", "main"])

    def test_review_loop(self):
        s = load()
        self.assertEqual(s.review_loops, 1)
        e = s.review_loop_events[0]
        self.assertEqual((e["reviewer_agent_id"], e["coder_agent_id"], e["via"]), ("aPR", "aPC2", "dispatch"))
        self.assertIn("review-loop", by_desc(s, "Implement loader cache fixes").flags)

    def test_recheck(self):
        s = load()
        self.assertEqual((s.recheck_candidates, s.rechecked), (1, 1))
        self.assertIn("rechecked", by_desc(s, "Find config loader").flags)

    def test_repeat(self):
        s = load()
        d = by_desc(s, "Implement loader cache fixes")
        self.assertIn("repeat", d.flags)
        self.assertIs(d.repeat_of, by_desc(s, "Implement loader cache"))

    def test_model_check_with_agents_dir_uses_role(self):
        s = load()
        self.assertEqual(s.warnings, ["agent factory:reviewer expected model opus, used claude-sonnet-5-5"])
        self.assertIn("WARN", by_desc(s, "Review loader cache").flags)

    def test_model_check_skipped_without_agents_dir(self):
        with tempfile.TemporaryDirectory() as cwd:
            d = os.path.join(cwd, ".claude", "agents")
            os.makedirs(d)
            with open(os.path.join(d, "reviewer.md"), "w") as f:
                f.write("---\nmodel: opus\n---\n")
            self.assertEqual(metrics.expected_model("reviewer", cwd), "opus")
            self.assertIsNone(metrics.expected_model("factory:reviewer", cwd))
        self.assertEqual(load(agents_dir=None).warnings, [])


if __name__ == "__main__":
    unittest.main()
