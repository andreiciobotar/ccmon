import os
import tempfile
import unittest

from tests.support import PRICES
from isshoni import prices
from isshoni.transcript import SessionError

# Fixture rates: opus 5/10/15(5m)/20(1h)/0.5; haiku 1/2/3/4/0.1, over 100 tokens 10/20/30/40/1.
OPUS = "claude-opus-5-5"
HAIKU = "claude-haiku-5-5"


def usage(i=0, o=0, cw=0, cr=0, h1=None):
    u = {"input_tokens": i, "output_tokens": o, "cache_creation_input_tokens": cw, "cache_read_input_tokens": cr}
    if h1 is not None:
        u["cache_creation"] = {"ephemeral_1h_input_tokens": h1, "ephemeral_5m_input_tokens": cw - h1}
    return u


class CostTests(unittest.TestCase):
    def test_default_file_prices_the_corpus_models(self):
        p = prices.load()
        for m, per_m in (("claude-fable-5-2", 8), (OPUS, 4), ("claude-sonnet-5-5", 2), (HAIKU, 0.1)):
            self.assertAlmostEqual(p.cost(m, usage(i=100000)), per_m / 10, msg=m)  # haiku: at the long-prompt limit
        self.assertAlmostEqual(p.cost(HAIKU, usage(i=100001)), 0.5 * 100001 / 1e6)
        self.assertEqual((p.currency, p.as_of), ("USD", "2026-11-14"))

    def test_default_file_all_five_classes(self):
        # 1000 in, 2000 out, 3000 cache write (1000 of them 1h), 4000 cache read; hand-computed from the spec table
        p, u = prices.load(), usage(i=1000, o=2000, cw=3000, cr=4000, h1=1000)
        for m, want in (("claude-fable-5-2", 0.1248), (OPUS, 0.0628), ("claude-sonnet-5-5", 0.0314), (HAIKU, 0.00159)):
            self.assertAlmostEqual(p.cost(m, u), want, msg=m)
        long_u = usage(i=100000, o=1000, cw=2000, cr=1, h1=1000)  # prompt 102,001: all five long-prompt rates
        self.assertAlmostEqual(p.cost(HAIKU, long_u), 0.05412505)

    def test_plain(self):
        self.assertAlmostEqual(PRICES.cost(OPUS, usage(i=1000, o=100, cr=10)), (5000 + 1000 + 5) / 1e6)

    def test_cache_write_split_and_no_breakdown(self):
        self.assertAlmostEqual(PRICES.cost(OPUS, usage(cw=100, h1=40)), (60 * 15 + 40 * 20) / 1e6)
        self.assertAlmostEqual(PRICES.cost(OPUS, usage(cw=100)), 100 * 15 / 1e6)  # no breakdown: all 5m
        self.assertAlmostEqual(PRICES.cost(OPUS, usage(cw=100, h1=40) | {"cache_creation_input_tokens": 30}),
                               30 * 20 / 1e6)  # 1h never exceeds the total written

    def test_haiku_long_prompt_boundary(self):
        self.assertAlmostEqual(PRICES.cost(HAIKU, usage(i=60, cr=40)), (60 * 1 + 40 * 0.1) / 1e6)  # 100: standard
        self.assertAlmostEqual(PRICES.cost(HAIKU, usage(i=61, cr=40)), (61 * 10 + 40 * 1) / 1e6)  # 101: long

    def test_model_matching(self):
        self.assertEqual(PRICES.cost(OPUS + "-20260101", usage(i=1)), PRICES.cost(OPUS, usage(i=1)))
        for m in ("claude-opus-5", OPUS + "-x", OPUS + "-2026", "unknown", "claude-sonnet-5-5"):
            self.assertIsNone(PRICES.cost(m, usage(i=1)), m)


class LoadErrorTests(unittest.TestCase):
    GOOD = ('currency = "USD"\nas_of = "2026-01-01"\nsource = "s"\n[models.m]\n'
            'input = 1\noutput = 1\ncache_write_5m = 1\ncache_write_1h = 1\ncache_read = 1\n')

    def fails(self, text, *words):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "p.toml")
            with open(path, "w") as f:
                f.write(text)
            with self.assertRaises(SessionError) as cm:
                prices.load(path)
        self.assertIn(path, str(cm.exception))
        for w in words:
            self.assertIn(w, str(cm.exception))

    def test_good_file_loads(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "p.toml")
            with open(path, "w") as f:
                f.write(self.GOOD)
            self.assertEqual(prices.load(path).to_dict()["path"], path)

    def test_not_utf8(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "p.toml")
            with open(path, "wb") as f:
                f.write(b"currency = \xff\xfe")
            with self.assertRaises(SessionError) as cm:
                prices.load(path)
        self.assertIn(path, str(cm.exception))

    def test_missing_file(self):
        with self.assertRaises(SessionError) as cm:
            prices.load("/nonexistent/prices.toml")
        self.assertIn("/nonexistent/prices.toml", str(cm.exception))

    def test_invalid_toml(self):
        self.fails("currency = ", "invalid TOML")

    def test_header_fields(self):
        self.fails(self.GOOD.replace('currency = "USD"\n', ""), "currency")
        self.fails(self.GOOD.replace('as_of = "2026-01-01"', 'as_of = ""'), "as_of")

    def test_model_rates(self):
        self.fails(self.GOOD.replace("cache_read = 1\n", ""), "cache_read")
        self.fails(self.GOOD.replace("output = 1", "output = -1"), "output")
        self.fails(self.GOOD.replace("input = 1", 'input = "1"'), "input")

    def test_long_prompt(self):
        rates = self.GOOD.split("[models.m]\n")[1]
        self.fails(self.GOOD + "[models.m.long_prompt]\n" + rates, "long_prompt", "over")
        self.fails(self.GOOD + "[models.m.long_prompt]\nover = 5\n" + rates.replace("output = 1\n", ""),
                   "long_prompt", "output")


if __name__ == "__main__":
    unittest.main()
