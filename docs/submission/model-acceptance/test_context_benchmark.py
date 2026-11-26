import json
import unittest
from context_benchmark import prompt_for

class ContextBenchmarkTests(unittest.TestCase):
    def test_markers_unique_and_spread(self):
        text, values = prompt_for(10000, 123)
        self.assertEqual(set(values), {"ALPHA","BRAVO","CHARLIE"})
        self.assertEqual(len(set(values.values())), 3)
        for k, f in zip(("ALPHA","BRAVO","CHARLIE"),(0.05,0.50,0.95)):
            marker = "MARKER " + k + " = " + values[k]
            self.assertEqual(text.count(marker), 1)
            self.assertLess(abs(text.index(marker)/len(text)-f), 0.025)
    def test_seed_changes_archive(self):
        a, av = prompt_for(1000, 17)
        b, bv = prompt_for(1000, 18)
        self.assertNotEqual(a, b)
        self.assertNotEqual(av, bv)
    def test_allocation_mode_does_not_claim_recall(self):
        import io
        from contextlib import redirect_stdout
        from unittest.mock import patch
        import context_benchmark as cb
        fake = {"http_ok": True, "done": True, "prompt_tokens": 320,
                "marker_score": 3, "elapsed_seconds": 0.1,
                "memory": {"context_length": 65536}, "pass": False}
        args = ["context_benchmark.py", "--base-url", "http://127.0.0.1:43153",
                "--contexts", "65536", "--allocation-only", "--cpu-only"]
        with patch("sys.argv", args), patch.object(cb, "run_case", return_value=fake), \
             patch.object(cb, "read_json", return_value={"models": [{"name": "gemma4:latest", "context_length": 65536}]}), \
             redirect_stdout(io.StringIO()) as capture:
            self.assertEqual(cb.main(), 0)
        first = json.loads(capture.getvalue().splitlines()[0])
        self.assertTrue(first["allocation_pass"])
        self.assertFalse(first["full_context_recall_test"])
        self.assertEqual(first["short_prompt_tokens"], 320)

if __name__ == "__main__":
    unittest.main()
