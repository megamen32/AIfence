import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "scripts/deploy-apps"
CHECK = DEPLOY.read_text().split("<<'VERIFY_MAIN'\n", 1)[1].split("\nVERIFY_MAIN\n", 1)[0]
SERVER_POLICY = Path("/home/roomhacker/Airlock/personal/releases/current")
INPUTS = ("scripts/deploy-apps", "scripts/personal-build-apps.py",
          "scripts/personal-source-bundle.go", "scripts/personal-mac-rollout.py")


class LiveAppFreezeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="aifence-app-freeze-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.repo = self.base / "checkout"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "main")
        for name in INPUTS:
            target = self.repo / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
        self.lock = {"release": "personal-frozen", "airlock": "a" * 40,
                     "agentsdk": "b" * 40, "sol": "c" * 40}
        self.write(self.repo / "platform/personal-release.lock.json", self.lock)
        for name, text in {
            "go.mod": "module market-watch\n\ntool github.com/a-h/templ/cmd/templ\n",
            "go.sum": "", "main.go": "package main\n", "styles/app.css": "body {}\n",
        }.items():
            path = self.repo / "apps/market-watch" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        self.commit()
        self.home = self.base / "profile"
        self.approved = self.home / "AIfence/.runtime/personal-releases/current"
        self.release = {"core": self.lock["airlock"], "agentsdk": self.lock["agentsdk"],
                        "sol": self.lock["sol"],
                        "archives": {"backend.tar.gz": "1" * 64, "frontend.tar.gz": "2" * 64}}
        self.write(self.approved / "release.json", self.release)
        self.channel = {"frozen": True, "components": dict(self.lock, aifence=self.sha)}
        self.channel["components"].pop("release")
        self.channel["platform_manifest_sha256"] = hashlib.sha256((self.approved / "release.json").read_bytes()).hexdigest()
        self.write(self.approved / "channel.json", self.channel)
        self.receipt = self.base / "apps.json"
        self.write(self.receipt, dict(self.lock, source_commit=self.sha))
        self.reads = []

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], text=True).strip()

    def commit(self):
        self.git("add", "--", "scripts", "platform", "apps")
        self.git("-c", "user.name=Release Guard Test", "-c", "user.email=release-guard@example.invalid",
                 "commit", "-q", "-m", "Committed release inputs")
        self.sha = self.git("rev-parse", "HEAD")

    def write(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, sort_keys=True) + "\n")

    def check(self, profile="darwin", receipt=None):
        original = Path.read_bytes
        def read(path):
            self.reads.append(path)
            if path.parent == SERVER_POLICY:
                return original(self.approved / path.name)
            return original(path)
        with mock.patch.object(Path, "home", return_value=self.home), \
             mock.patch.object(Path, "read_bytes", read), \
             mock.patch.object(sys, "platform", profile), \
             mock.patch.object(sys, "argv", ["-", str(self.repo), self.sha, str(receipt or "")]):
            exec(compile(CHECK, str(DEPLOY), "exec"), {"__name__": "__main__"})

    def live_command(self, profile):
        # Run the actual shell entrypoint. Redirect only the two fixed policy
        # locations to fixtures inside its Python stdin checks.
        runner = self.base / ("python-" + profile)
        runner.write_text("#!" + sys.executable + "\n"
            + "import pathlib, sys\nfrom unittest import mock\n"
            + "assert sys.argv[1] == '-', 'unexpected build invocation'\n"
            + "sys.argv = sys.argv[1:]\n"
            + "home = pathlib.Path(" + repr(str(self.home)) + ")\n"
            + "approved = pathlib.Path(" + repr(str(self.approved)) + ")\n"
            + "server = pathlib.Path(" + repr(str(SERVER_POLICY)) + ")\n"
            + "original = pathlib.Path.read_bytes\n"
            + "def read(path):\n    return original(approved / path.name if path.parent == server else path)\n"
            + "with mock.patch.object(pathlib.Path, 'home', return_value=home), mock.patch.object(pathlib.Path, 'read_bytes', read), mock.patch.object(sys, 'platform', " + repr(profile) + "):\n"
            + "    exec(compile(sys.stdin.read(), '<deploy-check>', 'exec'), {'__name__': '__main__'})\n")
        runner.chmod(0o700)
        env = dict(os.environ, AIFENCE_PYTHON=str(runner), AIFENCE_DEPLOY_COMMIT=self.sha)
        return subprocess.run([str(self.repo / "scripts/deploy-apps"), "market-watch"],
                              env=env, capture_output=True, text=True, timeout=15)

    def test_same_frozen_tuple_allows_new_app_commit_on_both_hosts(self):
        (self.repo / "apps/market-watch/main.go").write_text("package main\n// newer app source\n")
        self.commit()
        self.assertNotEqual(self.sha, self.channel["components"]["aifence"])
        self.write(self.receipt, dict(self.lock, source_commit=self.sha))
        for profile, approved in (("darwin", self.approved), ("linux", SERVER_POLICY)):
            with self.subTest(profile=profile):
                self.check(profile, self.receipt)
                self.assertIn(approved / "channel.json", self.reads)
                self.assertIn(approved / "release.json", self.reads)

    def test_each_changed_pin_is_rejected_before_live_state_or_build(self):
        for name in ("airlock", "agentsdk", "sol"):
            self.write(self.repo / "platform/personal-release.lock.json", dict(self.lock, **{name: "e" * 40}))
            self.commit()
            for profile in ("darwin", "linux"):
                with self.subTest(pin=name, profile=profile):
                    result = self.live_command(profile)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("dependencies differ from the frozen release", result.stderr)
                    self.assertFalse((self.repo / ".git/aifence-deploy").exists())

    def test_worktree_helper_cannot_replace_committed_policy(self):
        self.write(self.repo / "platform/personal-release.lock.json", dict(self.lock, sol="e" * 40))
        self.commit()
        (self.repo / "scripts/personal-mac-rollout.py").write_text("raise SystemExit(0)\n")
        with self.assertRaisesRegex(SystemExit, "dependencies differ from the frozen release"):
            self.check()

    def test_missing_malformed_and_hash_mismatched_policy_fail_closed(self):
        channel = self.approved / "channel.json"
        for profile in ("darwin", "linux"):
            for content in (None, "{", json.dumps(dict(self.channel, platform_manifest_sha256="e" * 64))):
                with self.subTest(profile=profile, content=content):
                    if content is None:
                        channel.unlink(missing_ok=True)
                    else:
                        channel.write_text(content)
                    with self.assertRaises(ValueError):
                        self.check(profile)
        self.write(channel, self.channel)

    def test_receipt_source_and_all_platform_pins_match_the_committed_lock(self):
        for name in ("source_commit", "airlock", "agentsdk", "sol"):
            with self.subTest(field=name):
                receipt = dict(self.lock, source_commit=self.sha)
                receipt[name] = "e" * 40
                self.write(self.receipt, receipt)
                with self.assertRaisesRegex(SystemExit, "App bundle platform dependencies or source commit"):
                    self.check(receipt=self.receipt)

    def test_policy_is_read_again_after_the_build(self):
        self.check()
        release = dict(self.release, sol="e" * 40)
        self.write(self.approved / "release.json", release)
        channel = copy.deepcopy(self.channel)
        channel["components"]["sol"] = release["sol"]
        channel["platform_manifest_sha256"] = hashlib.sha256((self.approved / "release.json").read_bytes()).hexdigest()
        self.write(self.approved / "channel.json", channel)
        with self.assertRaisesRegex(SystemExit, "dependencies differ from the frozen release"):
            self.check(receipt=self.receipt)

    def test_unfrozen_valid_policy_allows_a_future_platform_recipe(self):
        self.write(self.approved / "channel.json", dict(self.channel, frozen=False))
        future = dict(self.lock, agentsdk="e" * 40)
        self.write(self.repo / "platform/personal-release.lock.json", future)
        self.commit()
        self.write(self.receipt, dict(future, source_commit=self.sha))
        self.check(receipt=self.receipt)

    def test_dry_run_stages_future_recipe_without_policy(self):
        self.write(self.repo / "platform/personal-release.lock.json", dict(self.lock, sol="e" * 40))
        self.commit()
        shutil.rmtree(self.approved)
        env = dict(os.environ, AIFENCE_PYTHON=sys.executable, AIFENCE_DEPLOY_COMMIT=self.sha)
        result = subprocess.run([str(self.repo / "scripts/deploy-apps"), "--dry-run", "market-watch"],
                                env=env, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Dry run: no build, login, upload, or deployment.", result.stdout)
        self.assertFalse((self.repo / ".git/aifence-deploy/state").exists())


if __name__ == "__main__":
    unittest.main()
