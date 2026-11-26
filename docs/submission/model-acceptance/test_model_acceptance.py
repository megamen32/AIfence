import json
import unittest
from model_acceptance import endpoint, evaluate, parse_stream, payload, safe_usage, tiny_png


class ModelAcceptanceTests(unittest.TestCase):
    def test_endpoint_rejects_credential(self):
        self.assertEqual(endpoint("https://example.test/v1/"), "https://example.test/v1/chat/completions")
        for u in ("https://name:secret@example.test/v1", "https://example.test/v1?key=x", "file:///tmp"):
            with self.assertRaises(ValueError):
                endpoint(u)

    def test_stream_http_success_not_enough(self):
        self.assertEqual(evaluate("stream", parse_stream([b"data: [DONE]\n"])), (False, "incomplete_or_wrong_stream"))
        d = parse_stream([b'data: {"choices":[{"delta":{"content":"O"}}]}\n',
                          b'data: {"choices":[{"delta":{"content":"K"},"finish_reason":"stop"}]}\n',
                          b'data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}\n',
                          b"data: [DONE]\n"])
        self.assertTrue(evaluate("stream", d)[0])
        self.assertEqual(d["usage"]["total_tokens"], 5)

    def test_tool_validation(self):
        def row(name, args):
            return {"choices": [{"message": {"tool_calls": [{"function": {"name": name, "arguments": args}}]}}]}
        self.assertTrue(evaluate("tool", row("record_status", '{"status":"ready"}'))[0])
        self.assertFalse(evaluate("tool", row("record_status", '{"status":"wrong"}'))[0])
        self.assertFalse(evaluate("tool", row("other", '{"status":"ready"}'))[0])
        self.assertFalse(evaluate("tool", row("record_status", "invalid-json"))[0])

    def test_synthetic_vision(self):
        self.assertTrue(tiny_png().startswith(b"\x89PNG\r\n\x1a\n"))
        self.assertIn("data:image/png;base64,", payload("vision", "test")["messages"][0]["content"][1]["image_url"]["url"])
        self.assertTrue(evaluate("vision", {"choices": [{"message": {"content": "RED"}}]})[0])
        self.assertFalse(evaluate("vision", {"choices": [{"message": {"content": "BLUE"}}]})[0])

    def test_evidence_keeps_missingness_and_human_gate(self):
        def row(value):
            return {"choices": [{"message": {"content": json.dumps(value)}}]}
        expected = {"cause_status": "unproven", "cart_event": "missing",
                    "next_action": "verify_historical_tracking_and_release", "backlog_action": "draft"}
        self.assertTrue(evaluate("evidence", row(expected))[0])
        for key, wrong in (("cause_status", "proven"), ("cart_event", "zero"), ("backlog_action", "send")):
            d = expected.copy()
            d[key] = wrong
            self.assertFalse(evaluate("evidence", row(d))[0])

    def test_output_budget_for_vision_and_reasoning(self):
        self.assertEqual(payload("text", "test")["max_tokens"], 96)
        self.assertEqual(payload("tool", "test")["max_tokens"], 256)
        self.assertEqual(payload("vision", "test")["max_tokens"], 512)
        self.assertEqual(payload("evidence", "test")["max_tokens"], 512)

    def test_missing_usage_is_unknown(self):
        self.assertIsNone(safe_usage(None))
        self.assertIsNone(safe_usage({"prompt_tokens": "100", "total_tokens": -1}))
        self.assertEqual(safe_usage({"prompt_tokens": 0}), {"prompt_tokens": 0})


if __name__ == "__main__":
    unittest.main()
