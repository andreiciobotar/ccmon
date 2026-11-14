"""The price file and the cost of one message: the one owner of prices."""
from __future__ import annotations

import os
import re
import tomllib

from .transcript import SessionError, _int, usage_from

DEFAULT_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "prices.toml")
RATES = ("input", "output", "cache_write_5m", "cache_write_1h", "cache_read")
_DATED = re.compile(r"-\d{8}$")


def _rates(table, where, path, extra=()):
    if not isinstance(table, dict):
        raise SessionError("prices file %s: %s must be a table" % (path, where))
    for k in RATES + extra:
        v = table.get(k)
        if isinstance(v, bool) or not isinstance(v, (int, float)) or v < 0:
            raise SessionError("prices file %s: %s needs %r as a non-negative number" % (path, where, k))


class Prices:
    def __init__(self, path, data):
        self.path = path
        for k in ("currency", "as_of", "source"):
            v = data.get(k)
            if not isinstance(v, str) or not v:
                raise SessionError("prices file %s: %r must be a non-empty string" % (path, k))
            setattr(self, k, v)
        models = data.get("models")
        if not isinstance(models, dict) or not models:
            raise SessionError("prices file %s: no [models.<id>] tables" % path)
        for m, t in models.items():
            _rates(t, "[models.%s]" % m, path)
            if "long_prompt" in t:
                _rates(t["long_prompt"], "[models.%s.long_prompt]" % m, path, extra=("over",))
        self.models = models

    def to_dict(self):
        return {"currency": self.currency, "as_of": self.as_of, "source": self.source, "path": self.path}

    def cost(self, model, raw_usage):
        """Cost of one message in the file's currency, or None when the model has no price."""
        t = self.models.get(model) or self.models.get(_DATED.sub("", model))
        if t is None:
            return None
        u = usage_from(raw_usage)
        long_prompt = t.get("long_prompt")
        if long_prompt and u.input + u.cache_write + u.cache_read > long_prompt["over"]:
            t = long_prompt
        tiers = raw_usage.get("cache_creation") if isinstance(raw_usage, dict) else None
        write_1h = min(_int(tiers.get("ephemeral_1h_input_tokens")) if isinstance(tiers, dict) else 0,
                       u.cache_write)
        return (u.input * t["input"] + u.output * t["output"] + u.cache_read * t["cache_read"]
                + (u.cache_write - write_1h) * t["cache_write_5m"] + write_1h * t["cache_write_1h"]) / 1e6


def load(path=None):
    path = os.path.expanduser(path) if path else DEFAULT_PATH
    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except OSError as e:
        raise SessionError("prices file %s: %s" % (path, e.strerror or e))
    except (tomllib.TOMLDecodeError, UnicodeDecodeError) as e:
        raise SessionError("prices file %s: invalid TOML (%s)" % (path, e))
    return Prices(path, data)
