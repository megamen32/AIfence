#!/usr/bin/env python3
"""Build and verify committed Airlock source bundles with the pinned native SDK."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
APPS = ("product-manager", "metrika-analyst", "market-watch", "web-scraper", "google-trends", "wordstat")
BUILD_INPUTS = (
    "scripts/deploy-apps",
    "scripts/personal-build-apps.py",
    "scripts/personal-source-bundle.go",
    "platform/personal-release.lock.json",
)

def run(*args, cwd=None, env=None):
    return subprocess.run(list(map(str, args)), cwd=cwd, env=env, check=True)

def capture(*args, cwd=None, env=None):
    return subprocess.check_output(list(map(str, args)), cwd=cwd, env=env, text=True).strip()

def committed_inputs(rev):
    inputs = {}
    for name in BUILD_INPUTS:
        data = subprocess.check_output(["git", "-C", str(ROOT), "show", rev + ":" + name])
        path = ROOT / name
        if path.is_symlink() or not path.is_file() or path.read_bytes() != data:
            raise ValueError(f"{name} must match selected commit {rev}; commit the build inputs first")
        inputs[name] = data
    return inputs

def locked_sources(cache, lock):
    cache.mkdir(mode=0o700, parents=True, exist_ok=True)
    repos = {}
    for name in ("agentsdk", "sol"):
        rev = lock[name]
        repo = cache / (name + "-source")
        present = repo.is_dir() and subprocess.run(
            ["git", "-C", repo, "cat-file", "-e", rev + "^{commit}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
        if not present:
            mirror = os.environ.get("AIFENCE_PERSONAL_GIT", "").rstrip("/")
            if not mirror:
                raise ValueError("Pinned " + name + " is absent; supply --cache or AIFENCE_PERSONAL_GIT for the authorized SSH mirror")
            if not repo.exists():
                run("git", "init", "-q", repo)
            run("git", "-C", repo, "-c", "core.sshCommand=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes",
                "fetch", "--depth=1", mirror + "/" + name + ".git", rev)
        if capture("git", "-C", repo, "rev-parse", rev + "^{commit}") != rev:
            raise ValueError("unexpected " + name + " revision")
        repos[name] = repo
    return repos

def project_dependency(repo, rev, destination):
    destination.mkdir(parents=True)
    with tempfile.TemporaryFile() as archive:
        subprocess.run(["git", "-C", repo, "archive", "--format=tar", rev], stdout=archive, check=True)
        archive.seek(0)
        with tarfile.open(fileobj=archive) as files:
            files.extractall(destination, filter="data")

def build_app(slug, rev, repos, lock, output, env, helper):
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
    parser.add_argument("--commit", default=os.environ.get("AIFENCE_DEPLOY_COMMIT", "HEAD"),
                        help="immutable source commit; an integration candidate may be built before main advances")
    parser.add_argument("--cache", type=Path,
                        default=Path(os.environ.get("AIFENCE_PERSONAL_CACHE", str(ROOT / ".runtime/personal-release"))))
    parser.add_argument("--app", choices=APPS, action="append")
    args = parser.parse_args()
    rev = capture("git", "-C", ROOT, "rev-parse", "--verify", "--end-of-options", args.commit + "^{commit}")
    inputs = committed_inputs(rev)
    lock = json.loads(inputs["platform/personal-release.lock.json"])
    if not {"release", "airlock", "agentsdk", "sol"}.issubset(lock):
        raise ValueError("incomplete personal release lock")
    if any(not re.fullmatch(r"[0-9a-f]{40}", lock[name]) for name in ("airlock", "agentsdk", "sol")):
        raise ValueError("personal release dependencies must be exact commit IDs")
    target = args.output.resolve()
    if target.exists():
        raise FileExistsError(target)
    target.parent.mkdir(parents=True, exist_ok=True)
    repos = locked_sources(args.cache.resolve(), lock)
    # The private SDK is the only one that publishes the read-only manifest bit
    # and JobContext.ReadAppTool. The airlock_private build tag selects the
    # matching sources; a plain public build uses the published SDK instead.
    goflags = os.environ.get("GOFLAGS", "").strip()
    if "-tags=airlock_private" not in goflags and "-tags airlock_private" not in goflags:
        goflags = (goflags + " -tags=airlock_private").strip()
    env = dict(os.environ, AIFENCE_DEPLOY_COMMIT=rev, GOWORK="off", GOFLAGS=goflags)
    with tempfile.TemporaryDirectory(prefix="personal-release-", dir=target.parent) as tmp:
        out = Path(tmp)
        helper = out / "source-bundle-helper.go"
        helper.write_bytes(inputs["scripts/personal-source-bundle.go"])
        receipts = [build_app(app, rev, repos, lock, out, env, helper) for app in args.app or APPS]
        helper.unlink()
        record = {"release": lock["release"], "source_commit": rev, "airlock": lock["airlock"],
                  "agentsdk": lock["agentsdk"], "sol": lock["sol"],
                  "build_inputs": {name: hashlib.sha256(data).hexdigest() for name, data in inputs.items()},
                  "packages": receipts}
        (out / "apps.json").write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n")
        os.rename(out, target)
    print(target / "apps.json")

if __name__ == "__main__":
    main()
