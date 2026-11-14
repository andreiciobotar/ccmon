import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

from isshoni import metrics, transcript  # noqa: E402

PROJECTS = os.path.join(HERE, "fixtures", "projects")
AGENTS = os.path.join(HERE, "fixtures", "agents")
SESSION = os.path.join(PROJECTS, "-fixture-proj", "fixture-0001.jsonl")


def load(threshold=0.5):
    s = transcript.load_session(SESSION)
    metrics.analyze(s, AGENTS, threshold)
    return s


def by_desc(s, desc):
    return next(d for d in s.dispatches if d.description == desc)
