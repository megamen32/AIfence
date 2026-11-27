import copy
import fcntl
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
from unittest import mock

import test_personal_mac_rollout as fixtures

ROOT, CORE, rollout = fixtures.ROOT, fixtures.CORE, fixtures.rollout

loader = importlib.machinery.SourceFileLoader("personal_up", str(ROOT / "scripts/up"))
spec = importlib.util.spec_from_loader(loader.name, loader)
up = importlib.util.module_from_spec(spec)
loader.exec_module(up)


class PersonalUpTests(unittest.TestCase):
    write = fixtures.FrozenReleaseTests.write
    snapshot = fixtures.FrozenReleaseTests.snapshot

    def setUp(self):
        fixtures.FrozenReleaseTests.setUp(self)
        self.pointer = self.runtime / "personal-overlay/manifest.json"
        self.write(self.pointer, self.rows)
        self.worktree = self.base / "another-worktree"
        self.worktree.mkdir()
        (self.runtime / "compose.local.yml").write_text("keep the installed selectors\n")
        for patch in (mock.patch.object(up, "ROOT", self.worktree),
                      mock.patch.object(up, "load_rollout", return_value=rollout),
                      mock.patch.object(sys, "argv", ["scripts/up"])):
            patch.start()
            self.addCleanup(patch.stop)

    def assert_blocked_before_start(self):
        before = self.snapshot()
        with mock.patch.object(up.subprocess, "call") as start, self.assertRaises(ValueError):
            up.main()
        start.assert_not_called()
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.runtime / "personal-overlay/release.lock").exists())

    def test_another_worktree_uses_canonical_runtime_and_holds_rollout_lock(self):
        other = self.worktree / ".runtime/personal-overlay"
        other.mkdir(parents=True)
        (other / "manifest.json").write_text("not an approved receipt")
        def start(command, *, env):
            self.assertEqual(env["AIFENCE_UP_RUNTIME"], str(self.runtime))
            self.assertEqual(env["AIFENCE_UP_PERSONAL"], "1")
            self.assertEqual(env["AIFENCE_UP_ROOT"], str(self.worktree))
            self.assertEqual(env["AIFENCE_UP_GUARD"], str(self.worktree / "scripts/personal-mac-rollout.py"))
            with (self.runtime / "personal-overlay/release.lock").open("a") as competing:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(competing, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return 0
        with mock.patch.object(up.subprocess, "call", side_effect=start) as call:
            self.assertEqual(up.main(), 0)
        call.assert_called_once()
        with (self.runtime / "personal-overlay/release.lock").open("a") as released:
            fcntl.flock(released, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.assertEqual((other / "manifest.json").read_text(), "not an approved receipt")

    def test_installed_personal_channel_without_overlay_refuses_bootstrap(self):
        self.pointer.unlink()
        self.assert_blocked_before_start()

    def test_personal_overlay_without_channel_fails_closed(self):
        (self.approved / "channel.json").unlink()
        self.assert_blocked_before_start()

    def test_malformed_channel_fails_before_any_startup_mutation(self):
        (self.approved / "channel.json").write_text("{")
        self.assert_blocked_before_start()

    def test_changed_installed_archive_refuses_startup(self):
        changed = copy.deepcopy(self.rows)
        changed[1]["sha256"] = "e" * 64
        self.write(self.pointer, changed)
        self.assert_blocked_before_start()

    def test_initial_bootstrap_without_personal_installation_is_preserved(self):
        shutil.rmtree(self.approved)
        shutil.rmtree(self.runtime / "personal-overlay")
        with mock.patch.object(up.subprocess, "call", return_value=0) as start:
            self.assertEqual(up.main(), 0)
        self.assertEqual(start.call_args.kwargs["env"]["AIFENCE_UP_RUNTIME"], str(self.worktree / ".runtime"))
        self.assertEqual(start.call_args.kwargs["env"]["AIFENCE_UP_PERSONAL"], "0")
        self.assertFalse((self.runtime / "personal-overlay").exists())

    def startup_fixture(self, *, bad_provenance=False):
        # Execute the real startup shell and guard, replacing only external
        # Docker/Git/Colima commands with fixture executables.
        scripts = self.worktree / "scripts"
        scripts.mkdir()
        guard = (ROOT / "scripts/personal-mac-rollout.py").read_text()
        guard = guard.replace('RUNTIME = Path.home() / "AIfence/.runtime"',
                              "RUNTIME = Path(" + repr(str(self.runtime)) + ")")
        (scripts / "personal-mac-rollout.py").write_text(guard)
        platform = self.worktree / "platform"
        platform.mkdir()
        self.write(platform / "airlock.lock.json", {"revision": CORE})
        hosts = ("market-watch.localhost", "baserow.localhost", "openai-byok-local.localhost",
                 "userio-dion-local.localhost", "product-manager.localhost", "metrika-analyst.localhost")
        template = (ROOT / "platform/market-iframe.caddy").read_text()
        template = "\n".join("\thost " + " ".join(hosts) if line.strip().startswith("host ")
                             else line for line in template.splitlines()) + "\n"
        (platform / "market-iframe.caddy").write_text(template)
        shutil.copy2(ROOT / "scripts/local-market-iframe", scripts / "local-market-iframe")
        # A stale installed checkout remains on four hosts. Its tracked files
        # must never replace the invoking checkout's template.
        installed_platform = self.runtime.parent / "platform"
        installed_platform.mkdir()
        self.write(installed_platform / "airlock.lock.json", {"revision": CORE})
        old_template = template.replace(" product-manager.localhost metrika-analyst.localhost", "")
        (installed_platform / "market-iframe.caddy").write_text(old_template)
        installed_scripts = self.runtime.parent / "scripts"
        installed_scripts.mkdir()
        shutil.copy2(ROOT / "scripts/local-market-iframe", installed_scripts / "local-market-iframe")
        source = self.runtime / "airlock"
        (source / ".git").mkdir(parents=True)
        (source / ".env").write_text("COMPOSE_PROJECT_NAME=aifence\nTLS_MODE=local\nDOMAIN=localhost\n")
        (source / "caddy").mkdir()
        (source / "caddy/routes.caddy").write_text(
            "frame-src 'self' {args[0]}://*.{$DOMAIN};\n" * 2
            + "frame-ancestors 'self' {args[0]}://{$DOMAIN} end;\n")
        (source / "caddy/Caddyfile.local").write_text("http://*.{$DOMAIN} {\n}\n")
        target = self.runtime / "caddy-market-iframe"
        target.mkdir()
        (target / "market-iframe.caddy").write_text(template)
        commands = self.base / "commands"
        commands.mkdir()
        for name in ("git", "colima"):
            executable = commands / name
            executable.write_text("#!/bin/sh\nexit 0\n")
            executable.chmod(0o700)
        images = {
            row["local_image"]: {
                "Id": row["local_image_id"], "Architecture": "amd64",
                "Config": {"Labels": {
                    "org.opencontainers.image.revision": CORE,
                    "io.aifence.personal.source-image": row["patched_id"],
                    "io.aifence.personal.archive-sha256": "e" * 64 if bad_provenance else row["sha256"],
                }},
            } for row in self.rows
        }
        docker = commands / "docker"
        docker.write_text("#!" + sys.executable + "\nimport json, sys\n"
                          + "args = sys.argv[1:]\nimages = " + repr(images) + "\n"
                          + "if args[:2] == ['image', 'inspect']:\n"
                          + "    if '--format' in args: print(" + repr(CORE) + ")\n"
                          + "    else: print(json.dumps([images.get(args[2], next(iter(images.values())))]))\n"
                          + "elif args not in (['info'], ['compose', 'version']): raise SystemExit(90)\n")
        docker.chmod(0o700)
        start = up.START.replace('export PATH="/opt/homebrew/bin:/usr/local/go/bin:$PATH"',
                                 'export PATH="' + str(commands) + ':/opt/homebrew/bin:/usr/local/go/bin:$PATH"')
        return start, template, old_template

    def run_startup(self, start):
        real_call = subprocess.call
        def run(command, *, env):
            env.pop("AIFENCE_CACHE_HOST", None)
            return real_call(command, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        with mock.patch.object(up, "START", start), mock.patch.object(up.subprocess, "call", side_effect=run):
            return up.main()

    def test_actual_provenance_failure_preserves_compose_before_rewrite(self):
        start, _, _ = self.startup_fixture(bad_provenance=True)
        before = (self.runtime / "compose.local.yml").read_bytes()
        self.assertNotEqual(self.run_startup(start), 0)
        self.assertEqual((self.runtime / "compose.local.yml").read_bytes(), before)

    def test_other_worktree_preserves_six_host_template_in_canonical_runtime(self):
        start, template, old_template = self.startup_fixture()
        self.assertEqual(self.run_startup(start), 90)  # fixture stops at Compose up
        self.assertEqual((self.runtime / "caddy-market-iframe/market-iframe.caddy").read_text(), template)
        self.assertIn("product-manager.localhost metrika-analyst.localhost", template)
        self.assertEqual((self.runtime.parent / "platform/market-iframe.caddy").read_text(), old_template)
        self.assertTrue((self.runtime / "caddy-market-iframe/routes.caddy").is_file())
        self.assertFalse((self.worktree / ".runtime").exists())


if __name__ == "__main__":
    unittest.main()
