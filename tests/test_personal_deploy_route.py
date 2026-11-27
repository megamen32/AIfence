import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
# This executable probe exposes the native entrypoint contract, then records
# delegation without building or contacting a runtime. Native freeze behavior
# has its own tests in main.
NATIVE = r'''#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: <<'NATIVE_CONTRACT'
verify_main_release() {
frozen, _, approved, _ = policy["approved_release"](approved_path)
if frozen and pins != approved:
verify_main_release "$release_dir/packages/apps.json"
"$python_bin" "$ROOT/scripts/personal-build-apps.py" --commit "$commit" --app "$slug"
NATIVE_CONTRACT
probe_input=""
IFS= read -r probe_input || true
export PROBE_INPUT="$probe_input"
exec "$AIFENCE_PYTHON" - "$ROOT" "$@" <<'PROBE'
import json, os, pathlib, subprocess, sys
args = sys.argv[2:]
commit = os.environ["AIFENCE_DEPLOY_COMMIT"]
record = {"root": sys.argv[1], "commit": commit, "args": args,
          "url": os.environ.get("AIRLOCK_URL"), "sentinel": os.environ["ROUTE_SENTINEL"],
          "stdin": os.environ["PROBE_INPUT"]}
pathlib.Path(os.environ["PROBE_RESULT"]).write_text(json.dumps(record))
if any(arg not in ("--dry-run", "--create", "--auto", "market-watch",
                  "metrika-analyst", "product-manager") for arg in args):
    sys.exit(2)
main = subprocess.check_output(["git", "-C", sys.argv[1], "rev-parse", "main"], text=True).strip()
if "--dry-run" not in args and commit != main:
    sys.exit(64)
sys.exit(int(os.environ.get("PROBE_EXIT", "0")))
PROBE
'''


class StablePersonalDeployTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="aifence-personal-route-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name).resolve()
        self.root = self.base / "original root"
        self.main = self.base / "native main checkout"
        self.root.mkdir()
        self.git(self.root, "init", "-q", "-b", "sol-move")
        (self.root / "scripts").mkdir()
        shutil.copy2(ROOT / "scripts/deploy-apps", self.root / "scripts/deploy-apps")
        app = self.root / "apps/competitor-research"
        for name, content in {"go.mod": "module competitor-research\n\ntool github.com/a-h/templ/cmd/templ\n",
                              "go.sum": "", "main.go": "package main\n",
                              "styles/app.css": "body {}\n"}.items():
            path = app / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        self.commit(self.root)
        self.root_sha = self.git(self.root, "rev-parse", "HEAD")
        self.git(self.root, "worktree", "add", "-q", "-b", "main", str(self.main))
        self.native = self.main / "scripts/deploy-apps"
        self.native.write_text(NATIVE)
        self.native.chmod(0o755)
        self.commit(self.main)
        self.main_sha = self.git(self.main, "rev-parse", "HEAD")
        self.receipt = self.base / "probe.json"

    def git(self, root, *args):
        return subprocess.check_output(["git", "-C", str(root), *args],
                                       text=True, stderr=subprocess.PIPE).strip()

    def commit(self, root):
        self.git(root, "add", "--", ".")
        self.git(root, "-c", "user.name=Personal Route Test", "-c", "user.email=route@example.invalid",
                 "commit", "-q", "-m", "Committed entrypoint fixture")

    def run_deploy(self, *args, stdin="", **overrides):
        self.receipt.unlink(missing_ok=True)
        env = dict(os.environ)
        for name in ("AIRLOCK_URL", "AIFENCE_DEPLOY_COMMIT"):
            env.pop(name, None)
        env.update(AIFENCE_PYTHON=sys.executable, PROBE_RESULT=str(self.receipt),
                   ROUTE_SENTINEL="preserved", **overrides)
        return subprocess.run([str(self.root / "scripts/deploy-apps"), *args],
                              cwd=self.root, env=env, input=stdin, capture_output=True, text=True, timeout=10)

    def assert_delegated(self, result, args, commit, url=None, stdin=""):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.receipt.read_text()),
                         {"root": str(self.main), "commit": commit, "args": list(args),
                          "url": url, "sentinel": "preserved", "stdin": stdin})
        self.assertFalse((self.root / ".runtime").exists())

    def assert_blocked(self, result, message):
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(message, result.stderr)
        self.assertFalse(self.receipt.exists())
        self.assertFalse((self.root / ".runtime").exists())

    def test_default_routes_before_legacy_no_arg_recursion(self):
        self.assert_delegated(self.run_deploy(), (), self.main_sha)

    def test_delegation_preserves_the_callers_stdin(self):
        args = ("market-watch",)
        self.assert_delegated(self.run_deploy(*args, stdin="piped input\n"),
                              args, self.main_sha, stdin="piped input")

    def test_empty_url_and_normalized_personal_origins_preserve_arguments(self):
        args = ("--dry-run", "--create", "market-watch")
        for url in ("", "http://127.0.0.1:42080/", "http://[0:0:0:0:0:0:0:1]:42080",
                    "http://LOCALHOST.:42080", "https://AIRLOCK.BEZRABOTNYI.COM:443/",
                    "http://airlock.bezrabotnyi.com"):
            with self.subTest(url=url):
                self.assert_delegated(self.run_deploy(*args, AIRLOCK_URL=url), args, self.main_sha, url)

    def test_explicit_head_keeps_original_source_for_dry_run_and_live_rejects_it(self):
        args = ("--dry-run", "market-watch")
        self.assert_delegated(self.run_deploy(*args, AIFENCE_DEPLOY_COMMIT="HEAD"),
                              args, self.root_sha)
        result = self.run_deploy("market-watch", AIFENCE_DEPLOY_COMMIT="HEAD")
        self.assertEqual(result.returncode, 64)
        self.assertEqual(json.loads(self.receipt.read_text())["commit"], self.root_sha)
        self.assertFalse((self.root / ".runtime").exists())

    def test_explicit_empty_or_invalid_commit_is_not_replaced_by_main(self):
        for value in ("", "unknown-deployment-ref"):
            with self.subTest(value=value):
                self.assert_blocked(self.run_deploy("market-watch", AIFENCE_DEPLOY_COMMIT=value),
                                    "Cannot verify canonical main")

    def test_default_tracks_a_new_main_commit_without_changing_root(self):
        (self.main / "review.txt").write_text("next reviewed app commit\n")
        self.commit(self.main)
        current = self.git(self.main, "rev-parse", "HEAD")
        self.assertNotEqual(current, self.main_sha)
        self.assert_delegated(self.run_deploy("market-watch"), ("market-watch",), current)
        self.assertEqual(self.git(self.root, "rev-parse", "HEAD"), self.root_sha)

    def test_missing_main_worktree_fails_closed(self):
        self.git(self.main, "switch", "--detach", "-q")
        self.assert_blocked(self.run_deploy("market-watch"), "requires the installed main worktree")

    def test_dirty_missing_or_symlinked_canonical_entrypoint_fails_closed(self):
        for kind in ("dirty", "missing", "symlink"):
            with self.subTest(kind=kind):
                self.native.unlink(missing_ok=True)
                if kind == "dirty":
                    self.native.write_text(NATIVE + "\necho changed\n")
                elif kind == "symlink":
                    target = self.base / "linked-native"
                    target.write_text(NATIVE)
                    self.native.symlink_to(target)
                self.assert_blocked(self.run_deploy("market-watch"), "differs from its main commit")

    def test_old_main_without_native_guard_fails_closed(self):
        self.native.write_text("#!/usr/bin/env bash\nexit 0\n")
        self.commit(self.main)
        self.assert_blocked(self.run_deploy("market-watch"), "missing the native personal release guard")

    def test_server100_keeps_legacy_competitor_research_and_original_head(self):
        url = "http://100.99.88.100:42080"
        result = self.run_deploy("--dry-run", "competitor-research", AIRLOCK_URL=url)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Staged competitor-research from committed source " + self.root_sha, result.stdout)
        self.assertFalse(self.receipt.exists())
        self.assertTrue((self.root / ".runtime/deploy-apps/stages").is_dir())

    def test_personal_unknown_slug_and_canonical_failure_never_fall_back(self):
        result = self.run_deploy("competitor-research")
        self.assertEqual(result.returncode, 2)
        self.assertTrue(self.receipt.exists())
        self.assertFalse((self.root / ".runtime").exists())
        result = self.run_deploy("market-watch", PROBE_EXIT="23")
        self.assertEqual(result.returncode, 23)
        self.assertFalse((self.root / ".runtime").exists())

    def test_self_delegation_fails_without_recursion(self):
        self.git(self.main, "switch", "--detach", "-q")
        self.git(self.root, "branch", "-D", "main")
        self.git(self.root, "branch", "-m", "main")
        self.assert_blocked(self.run_deploy("market-watch"), "Refusing recursive personal deployment")

    def test_personal_origin_with_credentials_path_or_query_is_not_a_stand_fallback(self):
        for url in ("https://user@airlock.bezrabotnyi.com", "https://airlock.bezrabotnyi.com/app",
                    "http://localhost:42080?target=stand"):
            with self.subTest(url=url):
                self.assert_blocked(self.run_deploy("market-watch", AIRLOCK_URL=url),
                                    "AIRLOCK_URL must be an HTTP(S) origin")


if __name__ == "__main__":
    unittest.main()
