#!/usr/bin/env python3
"""Build and verify the native Airlock source bundles for a personal release."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
APPS = ("product-manager", "metrika-analyst", "market-watch")

def run(*args, cwd=None, env=None):
    return subprocess.run(list(map(str, args)), cwd=cwd, env=env, check=True)

def capture(*args, cwd=None, env=None):
    return subprocess.check_output(list(map(str, args)), cwd=cwd, env=env, text=True).strip()

def locked_sources(cache, lock):
    cache.mkdir(mode=0o700, parents=True, exist_ok=True)
    repos = {}
    for name in ("agentsdk", "sol"):
        rev = lock[name]
        repo = cache / (name + "-source")
        if not repo.exists():
            run("git", "init", "-q", repo)
        if subprocess.run(["git", "-C", repo, "cat-file", "-e", rev + "^{commit}"],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            run("git", "-C", repo, "-c", "core.sshCommand=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes",
                "fetch", "--depth=1", lock["private_git"] + "/" + name + ".git", rev)
        if capture("git", "-C", repo, "rev-parse", rev + "^{commit}") != rev:
            raise ValueError("unexpected " + name + " revision")
        repos[name] = repo
    return repos

def project_dependency(repo, rev, destination):
    destination.mkdir(parents=True)
    with tempfile.TemporaryFile() as tar:
        subprocess.run(["git", "-C", repo, "archive", "--format=tar", rev], stdout=tar, check=True)
        tar.seek(0)
        with tarfile.open(fileobj=tar) as files:
            files.extractall(destination, filter="data")

def build_app(slug, rev, repos, lock, output, env):
    stage_result = subprocess.run([ROOT / "scripts/deploy-apps", "--dry-run", slug],
                                  text=True, capture_output=True, env=env)
    if stage_result.returncode:
        raise RuntimeError(stage_result.stderr)
    stages = [Path(line.removeprefix("Staging retained: ")) for line in stage_result.stderr.splitlines()
              if line.startswith("Staging retained: ")]
    if len(stages) != 1 or not stages[0].is_dir():
        raise ValueError("tracked staging did not yield one app directory")
    stage = stages[0]
    for name, repo in repos.items():
        path = Path(".personal-modules") / name
        project_dependency(repo, lock[name], stage / path)
        run("go", "mod", "edit", "-replace=github.com/airlockrun/" + name + "=./" + path.as_posix(),
            cwd=stage, env=env)
    package = output / (slug + ".tar.gz")
    helper = ROOT / "scripts/personal-source-bundle.go"
    state = capture("go", "run", helper, stage, package, cwd=stage, env=env)
    with tempfile.TemporaryDirectory(prefix=slug + "-verify-", dir=output) as tmp:
        tree = Path(tmp)
        with tarfile.open(package) as archive:
            archive.extractall(tree, filter="data")
        (tree / "views/static").mkdir(parents=True, exist_ok=True)
        run("go", "tool", "air", "build", cwd=tree, env=env)
    with package.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    return {"app": slug, "source_tree": state, "sha256": digest, "size": package.stat().st_size,
            "source_commit": rev, "file": package.name}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cache", type=Path, default=ROOT / ".runtime/personal-release")
    parser.add_argument("--app", choices=APPS, action="append")
    args = parser.parse_args()
    rev = capture("git", "-C", ROOT, "rev-parse", "HEAD")
    lock = json.loads((ROOT / "platform/personal-release.lock.json").read_text())
    if not {"release", "private_git", "agentsdk", "sol"}.issubset(lock):
        raise ValueError("incomplete personal release lock")
    target = args.output.resolve()
    if target.exists():
        raise FileExistsError(target)
    target.parent.mkdir(parents=True, exist_ok=True)
    repos = locked_sources(args.cache.resolve(), lock)
    env = dict(os.environ, AIFENCE_DEPLOY_COMMIT=rev, GOWORK="off")
    with tempfile.TemporaryDirectory(prefix="personal-release-", dir=target.parent) as tmp:
        out = Path(tmp)
        receipts = [build_app(app, rev, repos, lock, out, env) for app in args.app or APPS]
        record = {"release": lock["release"], "source_commit": rev, "agentsdk": lock["agentsdk"],
                  "sol": lock["sol"], "packages": receipts}
        (out / "apps.json").write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n")
        os.rename(out, target)
    print(target / "apps.json")

if __name__ == "__main__":
    main()
