import contextlib
import fcntl
import io
from pathlib import Path
import tempfile
import unittest

from watch_cases import CHECKPOINT, check_once


class WatchCasesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = self.root / "input"
        self.data.mkdir()
        self.state = self.root / "state"
        self.dataset = self.data / "Датасет_пример.xlsx"
        for name in (self.dataset.name,"Store_demo.xlsx","Транскрибации интервью.zip","Конкурентный_анализ_demo.pptx"):
            (self.data/name).write_bytes(b"example")
        self.number = 0
        self.pinned = "ev-" + "1"*64

    def sync(self, source, output):
        self.assertNotEqual(source,self.data)
        self.assertEqual((source / self.dataset.name).read_bytes(),self.dataset.read_bytes())
        self.number += 1
        return {"snapshot_id":"ev-"+str(self.number)*64,"evidence_snapshot_id":self.pinned}

    def test_inputs_once_then_silent(self):
        check_once(self.data,self.state,self.sync)
        previous=(self.state/CHECKPOINT).read_bytes()
        self.dataset.touch()
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertIsNone(check_once(self.data,self.state,self.sync))
        self.assertEqual(out.getvalue(),"")
        self.assertEqual(self.number,1)
        self.assertEqual((self.state/CHECKPOINT).read_bytes(),previous)
        self.assertEqual(list(self.state.glob("inputs-*")),[])

    def test_failure_retries_and_previous_card_stays_pinned(self):
        success=check_once(self.data,self.state,self.sync)
        previous=(self.state/CHECKPOINT).read_bytes()
        self.dataset.write_bytes(b"new file")
        def fail(source,output):
            raise RuntimeError("SDK temporarily unavailable")
        with self.assertRaises(RuntimeError):
            check_once(self.data,self.state,fail)
        self.assertEqual((self.state/CHECKPOINT).read_bytes(),previous)
        next_run=check_once(self.data,self.state,self.sync)
        self.assertTrue(next_run["previous_case_pinned"])
        self.assertNotEqual(success["inputs"],next_run["inputs"])
        self.assertEqual(self.number,2)

    def test_concurrent_workers_do_not_sync_twice(self):
        self.state.mkdir()
        with (self.state/"worker.lock").open("a") as lock:
            fcntl.flock(lock,fcntl.LOCK_EX)
            self.assertIsNone(check_once(self.data,self.state,self.sync))
        self.assertEqual(self.number,0)

    def test_replacement_during_sync_gets_a_new_pass(self):
        def replace(source,output):
            rec=self.sync(source,output)
            self.dataset.write_bytes(b"arrived during import")
            return rec
        with self.assertRaisesRegex(RuntimeError,"New inputs"):
            check_once(self.data,self.state,replace)
        self.assertTrue(check_once(self.data,self.state,self.sync)["previous_case_pinned"])


if __name__ == "__main__":
    unittest.main()
