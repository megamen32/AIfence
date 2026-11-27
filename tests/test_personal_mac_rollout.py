import copy
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("personal_mac_rollout", ROOT / "scripts/personal-mac-rollout.py")
rollout = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(rollout)

CORE, SDK, SOL = ("a" * 40, "b" * 40, "c" * 40)


class FrozenReleaseTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="aifence-frozen-release-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.runtime = self.base / "runtime"
        self.approved = self.runtime / "personal-releases/current"
        self.approved.mkdir(parents=True)
        (self.runtime / "personal-overlay").mkdir()
        self.candidate = self.base / "candidate"
        self.candidate.mkdir()
        self.manifest = self.candidate / "images.json"
        self.release = {
            "version": "personal-frozen",
            "core": CORE, "agentsdk": SDK, "sol": SOL,
            "archives": {"backend.tar.gz": "1" * 64, "frontend.tar.gz": "2" * 64},
        }
        self.rows = [
            {"service": service, "name": role, "revision": CORE,
             "local_image": "aifence-" + service + ":personal-" + CORE[:12],
             "local_image_id": "sha256:" + str(index + 3) * 64,
             "patched_id": "sha256:" + str(index + 5) * 64,
             "sha256": self.release["archives"][role + ".tar.gz"],
             "artifact_sha256": str(index + 7) * 64}
            for index, (service, role) in enumerate((("airlock", "backend"), ("frontend", "frontend")))
        ]
        self.write(self.manifest, self.rows)
        self.write(self.candidate / "release.json", self.release)
        self.write(self.approved / "release.json", self.release)
        self.channel = {
            "frozen": True,
            "platform_manifest_sha256": rollout.sha(self.approved / "release.json"),
            "components": {"airlock": CORE, "agentsdk": SDK, "sol": SOL, "aifence": "d" * 40},
        }
        self.write(self.approved / "channel.json", self.channel)
        self.runtime_patch = mock.patch.object(rollout, "RUNTIME", self.runtime)
        self.runtime_patch.start()
        self.addCleanup(self.runtime_patch.stop)
        network_patch = mock.patch.object(rollout, "urlopen", side_effect=AssertionError("unexpected network access"))
        network_patch.start()
        self.addCleanup(network_patch.stop)

    def write(self, path, value):
        path.write_text(json.dumps(value, sort_keys=True) + "\n")

    def snapshot(self):
        return {str(p.relative_to(self.base)): p.read_bytes() for p in self.base.rglob("*") if p.is_file()}

    def call_main(self):
        args = ["personal-mac-rollout.py", "--manifest", str(self.manifest), "--revision", CORE,
                "--schema", "35", "--run-dir", str(self.base / "rollout")]
        with mock.patch.object(sys, "argv", args), mock.patch.dict(os.environ), mock.patch.object(rollout.os, "umask"):
            rollout.main()

    def assert_blocked_before_side_effects(self):
        before = self.snapshot()
        forbidden = {name: mock.Mock(side_effect=AssertionError("Docker reached before release validation: " + name))
                     for name in ("verify_images", "output", "run", "inspect", "sql")}
        with mock.patch.multiple(rollout, **forbidden), self.assertRaises(ValueError):
            self.call_main()
        for operation in forbidden.values():
            operation.assert_not_called()
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.runtime / "personal-overlay/release.lock").exists())
        self.assertFalse((self.base / "rollout").exists())

    def test_exact_frozen_release_is_admitted(self):
        with mock.patch.object(rollout, "apply") as apply:
            self.call_main()
        apply.assert_called_once_with(self.manifest.resolve(), CORE, 35, (self.base / "rollout").resolve())

    def test_same_core_different_sdk_or_sol_is_rejected_before_docker_or_writes(self):
        for component in ("agentsdk", "sol"):
            with self.subTest(component=component):
                changed = dict(self.release, **{component: "e" * 40})
                self.write(self.candidate / "release.json", changed)
                self.assert_blocked_before_side_effects()

    def test_changed_manifest_bytes_are_rejected_even_with_same_component_commits(self):
        self.write(self.candidate / "release.json", dict(self.release, version="another-release"))
        self.assert_blocked_before_side_effects()

    def test_image_archives_must_match_approved_release(self):
        for index in (0, 1):
            with self.subTest(service=self.rows[index]["service"]):
                changed = copy.deepcopy(self.rows)
                changed[index]["sha256"] = "e" * 64
                self.write(self.manifest, changed)
                self.assert_blocked_before_side_effects()

    def test_duplicate_image_service_is_rejected(self):
        self.write(self.manifest, [self.rows[0], self.rows[0]])
        self.assert_blocked_before_side_effects()

    def test_candidate_channel_cannot_disable_canonical_freeze(self):
        self.write(self.candidate / "channel.json", dict(self.channel, frozen=False))
        self.write(self.candidate / "release.json", dict(self.release, sol="e" * 40))
        self.assert_blocked_before_side_effects()

    def test_missing_or_malformed_canonical_channel_fails_closed(self):
        path = self.approved / "channel.json"
        for content in (None, "{", "[]", '{"frozen":true}'):
            with self.subTest(content=content):
                if content is None:
                    path.unlink(missing_ok=True)
                else:
                    path.write_text(content)
                self.assert_blocked_before_side_effects()

    def test_approved_manifest_must_match_channel_hash_and_components(self):
        cases = [
            dict(self.channel, platform_manifest_sha256="e" * 64),
            dict(self.channel, components=dict(self.channel["components"], sol="e" * 40)),
            dict(self.channel, components=dict(self.channel["components"], agentsdk="latest")),
            dict(self.channel, frozen="false"),
        ]
        for channel in cases:
            with self.subTest(channel=channel):
                self.write(self.approved / "channel.json", channel)
                self.assert_blocked_before_side_effects()

    def test_malformed_approved_manifest_fails_closed_even_when_unfrozen(self):
        self.write(self.approved / "channel.json", dict(self.channel, frozen=False))
        for content in ("{", "[]"):
            with self.subTest(content=content):
                (self.approved / "release.json").write_text(content)
                self.assert_blocked_before_side_effects()

    def test_missing_approved_manifest_fails_closed_even_when_unfrozen(self):
        self.write(self.approved / "channel.json", dict(self.channel, frozen=False))
        (self.approved / "release.json").unlink()
        self.assert_blocked_before_side_effects()

    def test_unfrozen_or_legacy_channel_keeps_explicit_rollout(self):
        (self.candidate / "release.json").unlink()
        for legacy in (False, True):
            with self.subTest(legacy=legacy):
                channel = dict(self.channel, frozen=False)
                if legacy:
                    channel.pop("frozen")
                self.write(self.approved / "channel.json", channel)
                with mock.patch.object(rollout, "apply") as apply:
                    self.call_main()
                apply.assert_called_once()

    def test_policy_is_rechecked_after_backup_before_compose(self):
        pointer = self.runtime / "personal-overlay/manifest.json"
        local = self.runtime / "compose.local.yml"
        base_compose = self.base / "base.yml"
        base_compose.write_text("services: {}\n")
        old_rows = copy.deepcopy(self.rows)
        for row in old_rows:
            row["local_image"] += "-previous"
            row["local_image_id"] = row["local_image_id"] + "-previous"
        self.write(pointer, old_rows)
        local.write_text("\n".join(row["local_image"] for row in old_rows))
        before = {
            row["service"]: {
                "Id": "previous-" + row["service"], "Image": row["local_image_id"],
                "State": {"Running": True},
                "Config": {"Image": row["local_image"], "Labels": {
                    "com.docker.compose.project": "aifence",
                    "com.docker.compose.project.config_files": str(base_compose),
                    "com.docker.compose.project.working_dir": str(self.base),
                }},
                "NetworkSettings": {"Networks": {}},
            } for row in old_rows
        }
        def output(*args):
            self.assertIn("config", args)
            chosen = self.rows if any(str(arg).endswith("compose.release.json") for arg in args) else old_rows
            return json.dumps({"services": {row["service"]: {"image": row["local_image"], "restart": "always"}
                                             for row in chosen}})
        def run(*args, **kwargs):
            self.assertTrue(any(command in args for command in ("pg_dump", "pg_restore", "pg_dumpall")))
            kwargs["stdout"].write(b"database backup inventory\n")
        def change_policy():
            release = dict(self.release, sol="e" * 40)
            self.write(self.approved / "release.json", release)
            channel = dict(self.channel, platform_manifest_sha256=rollout.sha(self.approved / "release.json"),
                           components=dict(self.channel["components"], sol="e" * 40))
            self.write(self.approved / "channel.json", channel)
        destination = self.base / "late-policy-change"
        with mock.patch.object(rollout, "verify_images"), \
             mock.patch.object(rollout, "inspect", side_effect=lambda service: before[service]), \
             mock.patch.object(rollout, "db_schema", return_value=35), \
             mock.patch.object(rollout, "wait_health", return_value={"status": "ok"}), \
             mock.patch.object(rollout, "output", side_effect=output), \
             mock.patch.object(rollout, "run", side_effect=run) as operations, \
             mock.patch.object(rollout, "wait_idle", side_effect=change_policy), \
             self.assertRaises(ValueError):
            rollout.apply(self.manifest, CORE, 35, destination)
        self.assertTrue((destination / "backup/airlock.dump").is_file())
        self.assertEqual(len(operations.call_args_list), 3)
        self.assertFalse(any("up" in call.args for call in operations.call_args_list))
        self.assertEqual(json.loads(pointer.read_text()), old_rows)


if __name__ == "__main__":
    unittest.main()
