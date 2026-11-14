"""Specs are working papers kept in ~/.claude/factory/specs/: nothing under docs/specs/ is tracked."""
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class NoSpecsTracked(unittest.TestCase):
    def test_nothing_under_docs_specs_is_tracked(self):
        try:
            p = subprocess.run(["git", "ls-files", "--", "docs/specs"], cwd=ROOT, capture_output=True, text=True)
        except OSError:
            self.skipTest("git is not installed")
        if p.returncode:
            self.skipTest("not a git checkout")
        self.assertEqual(p.stdout, "", "move these to ~/.claude/factory/specs/<repo directory name>/ and git rm them")


if __name__ == "__main__":
    unittest.main()
