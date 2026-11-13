import unittest


class Probe(unittest.TestCase):
    def test_fails(self):
        self.fail("CI probe")
