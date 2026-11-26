import contextlib
import fcntl
import io
from pathlib import Path
import subprocess
from unittest import mock
import tempfile
import unittest

from watch_cases import CHECKPOINT, LABEL, check_once, install, login_link


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

    def test_login_link_is_idempotent_and_preserves_foreign_entry(self):
        self.state.mkdir()
        definition = self.state / (LABEL + ".plist")
        definition.write_bytes(b"fixture")
        login_dir = self.root / "Library" / "LaunchAgents"
        link = login_link(definition, login_dir)
        self.assertEqual(link.resolve(), definition.resolve())
        self.assertEqual(login_link(definition, login_dir), link)
        link.unlink()
        link.write_bytes(b"other-owner")
        with self.assertRaisesRegex(RuntimeError, "Another launch definition"):
            login_link(definition, login_dir)
        self.assertEqual(link.read_bytes(), b"other-owner")
        link.unlink()
        link.symlink_to(self.root / "other-missing.plist")
        with self.assertRaisesRegex(RuntimeError, "Another launch definition"):
            login_link(definition, login_dir)
        self.assertEqual(link.readlink(), self.root / "other-missing.plist")

    def test_existing_manual_service_gains_login_link_without_restart(self):
        import plistlib
        self.state.mkdir()
        definition = self.state / (LABEL + ".plist")
        settings = {"Label": LABEL}
        definition.write_bytes(plistlib.dumps(settings))
        service = subprocess.CompletedProcess([],0,f"path = {definition}\nstate = running\n","")
        with mock.patch("watch_cases.STATE",self.state), mock.patch("watch_cases.Path.home",return_value=self.root), \
                mock.patch("watch_cases.config",return_value=settings), mock.patch("watch_cases.launch",return_value=service) as launch:
            with contextlib.redirect_stdout(io.StringIO()):
                install()
            self.assertEqual(launch.call_count,1)
            self.assertEqual(launch.call_args.args[0], "print")
        self.assertEqual((self.root / "Library" / "LaunchAgents" / definition.name).resolve(),definition.resolve())

    def test_active_import_cannot_be_unloaded_during_config_update(self):
        self.state.mkdir()
        definition=self.state / (LABEL + ".plist")
        definition.write_bytes(b"old")
        service=subprocess.CompletedProcess([],0,f"path = {definition}\nstate = running\n","")
        with (self.state / "worker.lock").open("a") as lock:
            fcntl.flock(lock,fcntl.LOCK_EX)
            with mock.patch("watch_cases.STATE",self.state), mock.patch("watch_cases.Path.home",return_value=self.root), \
                    mock.patch("watch_cases.config",return_value={"Label":LABEL}), mock.patch("watch_cases.launch",return_value=service) as launch:
                with self.assertRaisesRegex(RuntimeError,"sync is running"):
                    install()
                self.assertEqual(launch.call_count,1)
        self.assertEqual(definition.read_bytes(),b"old")


if __name__ == "__main__":
    unittest.main()
