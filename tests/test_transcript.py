import os
import tempfile
import unittest

from tests.support import PROJECTS, SESSION, by_desc, load
from ccmon import transcript
from ccmon.transcript import SessionError


class TranscriptTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.s = load()

    def test_token_dedupe_exact_totals(self):
        s = self.s
        main = s.main_usage_by_model["claude-fable-5-2"]
        # msg_m1 appears on two lines (and in a nested agent file) but counts once: (10,20,30,40) + 20 * (1,2,3,4)
        self.assertEqual(main.to_dict(), {"input": 30, "output": 60, "cache_write": 90, "cache_read": 120, "total": 300})
        e1 = by_desc(s, "Find config loader")
        # msg_a2 repeated on two lines counts once
        self.assertEqual(e1.usage.to_dict(), {"input": 11, "output": 21, "cache_write": 1, "cache_read": 220, "total": 253})

    def test_synthetic_skipped(self):
        e2 = by_desc(self.s, "Find config loader again")
        self.assertNotIn("<synthetic>", e2.usage_by_model)
        self.assertEqual(e2.usage.total, 20)

    def test_malformed_and_unknown_types(self):
        # bad JSON + assistant line without message; the unknown type and queue/attachment lines are silent
        self.assertEqual(self.s.malformed, 2)

    def test_meta_join(self):
        d = by_desc(self.s, "Find config loader")
        self.assertEqual(d.agent_id, "aE1")
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.duration_ms, 30000)

    def test_agentid_fallback_join(self):
        d = by_desc(self.s, "Map database migrations")  # meta has no toolUseId
        self.assertEqual(d.agent_id, "aE3")
        self.assertEqual(d.status, "completed")

    def test_running_uses_file_span(self):
        d = by_desc(self.s, "Find config loader again")  # no notification
        self.assertEqual(d.status, "running")
        self.assertEqual(d.duration_ms, 5000)  # 00:21 .. 00:26 (synthetic line still has a timestamp)

    def test_no_run(self):
        d = by_desc(self.s, "Ghost dispatch")
        self.assertEqual(d.status, "no-run")
        self.assertEqual(d.usage.total, 0)
        self.assertIsNone(d.agent_id)

    def test_orphan_with_broken_meta(self):
        orphans = [d for d in self.s.dispatches if d.status == "orphan"]
        self.assertEqual(len(orphans), 1)
        self.assertEqual(orphans[0].agent_id, "aORPH")
        self.assertEqual(orphans[0].usage.total, 4)

    def test_send_message_counts_resume(self):
        self.assertEqual(by_desc(self.s, "Apply fixes").resumes, 2)

    def test_results_and_reads(self):
        e1 = by_desc(self.s, "Find config loader")
        self.assertIn("src/config/loader.py:42", e1.results[0][1])
        self.assertEqual(self.s.reads[0][1], "/fixture/cwd/src/config/loader.py")
        self.assertEqual(self.s.cwd, "/fixture/cwd")

    def test_cross_file_dedupe(self):
        nested = by_desc(self.s, "Nested lookup")
        # the agent file repeats main's msg_m1 (10,20,30,40); only its own msg_a10 (1,1,1,1) counts
        self.assertEqual(nested.usage.to_dict(), {"input": 1, "output": 1, "cache_write": 1, "cache_read": 1, "total": 4})
        self.assertNotIn("claude-fable-5-2", nested.usage_by_model)

    def test_nested_dispatch_depth_and_link(self):
        d = by_desc(self.s, "Nested lookup")
        self.assertEqual(d.depth, 2)
        self.assertEqual(d.parent_agent_id, "aE1")
        self.assertEqual(d.agent_id, "aN1")
        self.assertEqual(by_desc(self.s, "Find config loader").depth, 1)

    def test_handback_agent_file_completed(self):
        d = by_desc(self.s, "Handback review")  # no notification at all
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.returned_at.strftime("%H:%M:%S"), "10:06:30")  # earlier than the main-file copy at 06:31
        self.assertEqual(d.duration_ms, 30000)  # returned_at minus dispatch
        self.assertEqual(len(d.results), 1)
        self.assertEqual(d.results[0][1], "## Verdict: CHANGES_NEEDED\n\n1. rename the helper")  # no harness prefix

    def test_handback_main_file_only(self):
        d = by_desc(self.s, "Entry point lookup")  # agent file ends in tool_use text, hand-back only in the main file
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.returned_at.strftime("%H:%M:%S"), "10:07:46")
        self.assertEqual(d.duration_ms, 31000)
        self.assertEqual(len(d.results), 1)
        self.assertIn("loader.py:12", d.results[0][1])

    def test_stub_notification_not_used_as_result(self):
        d = by_desc(self.s, "Another handback review")
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.duration_ms, 31000)  # from the notification
        self.assertEqual(d.returned_at.strftime("%H:%M:%S"), "10:08:30")  # hand-back is earlier than the stub at 08:40
        self.assertEqual(len(d.results), 1)
        self.assertFalse(any("STUBTEXT" in text for _, text in d.results))

    def test_notification_result_fallback_without_handback(self):
        # Only used when the agent has neither a hand-back nor an end_turn text.
        import tempfile, os, json
        with tempfile.TemporaryDirectory() as td:
            os.makedirs(os.path.join(td, "p", "s", "subagents"))
            main = os.path.join(td, "p", "s.jsonl")
            note = ("<task-notification><tool-use-id>t1</tool-use-id><status>completed</status>"
                    "<result>from notification</result><duration_ms>5</duration_ms></task-notification>")
            with open(main, "w") as f:
                f.write(json.dumps({"type": "assistant", "timestamp": "2026-01-01T00:00:00Z", "message": {"id": "a", "model": "m", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Agent", "input": {"description": "d", "prompt": "p"}}]}}) + "\n")
                f.write(json.dumps({"type": "user", "timestamp": "2026-01-01T00:01:00Z", "message": {"content": note}}) + "\n")
            base = os.path.join(td, "p", "s", "subagents", "agent-a1")
            with open(base + ".meta.json", "w") as f:
                f.write(json.dumps({"toolUseId": "t1"}))
            with open(base + ".jsonl", "w") as f:
                f.write(json.dumps({"type": "assistant", "timestamp": "2026-01-01T00:00:10Z", "message": {"id": "b", "model": "m", "content": [{"type": "text", "text": "wip"}], "stop_reason": "tool_use"}}) + "\n")
            s = transcript.load_session(main)
        d = s.dispatches[0]
        self.assertEqual((d.status, d.duration_ms), ("completed", 5))
        self.assertEqual([t for _, t in d.results], ["from notification"])

    def test_non_string_dedupe_keys(self):
        import json
        lines = [json.dumps({"type": "assistant", "uuid": [1], "requestId": {"a": 1}, "message": {"content": [{"type": "text", "text": "x"}], "usage": {"input_tokens": 1}}})]
        with tempfile.TemporaryDirectory() as td:
            os.makedirs(os.path.join(td, "p", "s", "subagents"))
            path = os.path.join(td, "p", "s.jsonl")
            for fp in (os.path.join(td, "p", "s", "subagents", "agent-a1.jsonl"), path):
                with open(fp, "w") as f:
                    f.write("\n".join(lines) + "\n")
            s = transcript.load_session(path)  # must not raise
        self.assertEqual(s.malformed, 0)

    def test_queued_command_notification_only(self):
        d = by_desc(self.s, "Queued only")  # only a queued_command attachment carries the notification
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.duration_ms, 12345)
        self.assertEqual(d.returned_at.strftime("%H:%M:%S"), "10:11:30")

    def test_other_attachment_types_ignored(self):
        d = by_desc(self.s, "Wrong attachment type")  # tag only inside a prompt_snapshot attachment
        self.assertEqual(d.status, "running")
        # the prompt_snapshot copy of E1's notification changes nothing either
        self.assertEqual(by_desc(self.s, "Find config loader").duration_ms, 30000)

    def test_user_line_notification_beats_queued_command(self):
        d = by_desc(self.s, "Both sources")
        self.assertEqual(d.status, "completed")
        self.assertEqual(d.duration_ms, 7000)  # user line, not the queued_command's 99999

    def test_notification_parse(self):
        n = transcript.parse_notification(
            "<task-notification>\n<tool-use-id>t1</tool-use-id>\n<status>completed</status>\n"
            "<result>multi\nline</result>\n<usage><duration_ms>269000</duration_ms></usage></task-notification>")
        self.assertEqual(n, {"tool_use_id": "t1", "status": "completed", "duration_ms": 269000, "result": "multi\nline"})
        self.assertIsNone(transcript.parse_notification("<task-notification>nothing</task-notification>"))

    def test_find_session(self):
        self.assertEqual(transcript.find_session(PROJECTS, "fixture"), SESSION)
        with self.assertRaises(SessionError):
            transcript.find_session(PROJECTS, "nope")

    def test_ambiguous_prefix(self):
        with tempfile.TemporaryDirectory() as td:
            os.makedirs(os.path.join(td, "p"))
            for n in ("abc1", "abc2"):
                open(os.path.join(td, "p", n + ".jsonl"), "w").close()
            with self.assertRaises(SessionError):
                transcript.find_session(td, "abc")
            self.assertTrue(transcript.find_session(td, "abc1").endswith("abc1.jsonl"))

    def test_tolerates_odd_shapes(self):
        lines = [
            '{"type":"assistant","message":{"id":"m","model":"x","content":"just a string","usage":null}}',
            '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t"}]},"toolUseResult":"err"}',
            '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t"}]},"toolUseResult":null}',
            '{"type":"assistant","message":{"id":"m2","model":"x","content":[1,null,{"type":"tool_use","name":"Agent","id":5,"input":"s"}],"usage":{"input_tokens":"a"}}}',
            '[1,2]', '', '{"no":"type"}',
        ]
        with tempfile.TemporaryDirectory() as td:
            os.makedirs(os.path.join(td, "p"))
            path = os.path.join(td, "p", "s1.jsonl")
            with open(path, "w") as f:
                f.write("\n".join(lines) + "\n")
            s = transcript.load_session(path)
        self.assertEqual(s.malformed, 2)
        self.assertEqual(s.dispatches, [])


if __name__ == "__main__":
    unittest.main()
