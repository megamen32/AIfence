#!/usr/bin/env python3
"""Apply two verified personal platform images to the existing M1 Airlock."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
from urllib.request import urlopen

SERVICES = ("airlock", "frontend")
PROJECT = "aifence"
BASE_URL = "http://localhost:42080"
RUNTIME = Path.home() / "AIfence/.runtime"

def sha(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()

def read_json(path):
    return json.loads(path.read_text())


def release_object(path):
    try:
        data = path.read_bytes()
        value = json.loads(data)
    except (OSError, ValueError) as error:
        raise ValueError("cannot read release record: " + str(path)) from error
    if not isinstance(value, dict):
        raise ValueError("release record must be an object: " + str(path))
    return data, value

def digest(value, length, label):
    if not isinstance(value, str) or not re.fullmatch("[a-f0-9]{" + str(length) + "}", value):
        raise ValueError("invalid " + label)
    return value

def platform_components(release):
    return {name: digest(release.get(field), 40, "release " + field)
            for name, field in (("airlock", "core"), ("agentsdk", "agentsdk"), ("sol", "sol"))}

def approved_release():
    # Policy comes only from the installed copy of the published channel.
    approved = RUNTIME / "personal-releases/current"
    _, channel = release_object(approved / "channel.json")
    approved_bytes, release = release_object(approved / "release.json")
    frozen = channel.get("frozen", False)
    if type(frozen) is not bool:
        raise ValueError("channel frozen must be a boolean")
    expected_hash = digest(channel.get("platform_manifest_sha256"), 64, "channel platform manifest hash")
    components = channel.get("components")
    if not isinstance(components, dict):
        raise ValueError("channel components must be an object")
    expected = {name: digest(components.get(name), 40, "channel " + name)
                for name in ("airlock", "agentsdk", "sol")}
    if "aifence" in components:
        digest(components["aifence"], 40, "channel aifence")
    if hashlib.sha256(approved_bytes).hexdigest() != expected_hash or platform_components(release) != expected:
        raise ValueError("approved platform manifest differs from the published channel")
    archives = release.get("archives")
    if not isinstance(archives, dict):
        raise ValueError("approved platform archives must be an object")
    expected_archives = {service: digest(archives.get(archive), 64, "release " + archive)
                         for service, archive in (("airlock", "backend.tar.gz"), ("frontend", "frontend.tar.gz"))}
    return frozen, expected_hash, expected, expected_archives

def verified_image_receipts(manifest, expected_archives):
    try:
        rows = read_json(manifest)
    except (OSError, ValueError) as error:
        raise ValueError("cannot read image receipt") from error
    if not isinstance(rows, list) or len(rows) != len(SERVICES):
        raise ValueError("approved release requires exactly two image receipts")
    image_archives = {}
    for row in rows:
        if not isinstance(row, dict) or row.get("service") not in SERVICES or row["service"] in image_archives:
            raise ValueError("invalid or duplicate image service")
        image_archives[row["service"]] = digest(row.get("sha256"), 64, "image archive hash")
    if image_archives != expected_archives:
        raise ValueError("image archives differ from the approved release")
    return {row["service"]: row for row in rows}

def verify_release_channel(manifest, revision):
    frozen, expected_hash, expected, archives = approved_release()
    if not frozen:
        return
    candidate_bytes, candidate = release_object(manifest.parent / "release.json")
    if platform_components(candidate) != expected or revision != expected["airlock"]:
        raise ValueError("frozen platform component revisions cannot change")
    if hashlib.sha256(candidate_bytes).hexdigest() != expected_hash:
        raise ValueError("candidate platform manifest differs from the frozen release")
    verified_image_receipts(manifest, archives)

def installed_release():
    _, _, expected, archives = approved_release()
    rows = verified_image_receipts(RUNTIME / "personal-overlay/manifest.json", archives)
    if any(row.get("revision") != expected["airlock"] for row in rows.values()):
        raise ValueError("installed images differ from the approved revision")
    return rows, expected["airlock"]

def save(path, value):
    tmp = path.with_name("." + path.name + ".new")
    with tmp.open("x") as stream:
        stream.write(value)
    os.replace(tmp, path)

def output(*args):
    return subprocess.check_output(list(map(str, args)), text=True).strip()

def run(*args, **kwargs):
    return subprocess.run(list(map(str, args)), check=True, **kwargs)

def inspect(service):
    return json.loads(output("docker", "inspect", PROJECT + "-" + service + "-1"))[0]

def sql(query):
    return output("docker", "exec", PROJECT + "-postgres-1", "psql", "-X", "-At",
                  "-U", "airlock", "-d", "airlock", "-v", "ON_ERROR_STOP=1", "-c", query)

def db_schema():
    return int(sql("select max(version_id) from goose_db_version where is_applied"))

def wait_idle():
    query = """select (select count(*) from system_runs where status in ('running','awaiting_confirmation'))
              + (select count(*) from runs where status='running' and started_at>now()-interval '1 hour')
              + (select count(*) from agent_job_attempts where status='running' and lease_expires_at>now())"""
    for _ in range(40):
        if sql(query) == "0":
            return
        time.sleep(2)
    raise RuntimeError("active runs or jobs still hold leases")

def networks(container):
    return container["NetworkSettings"]["Networks"]

def restore_networks(service, before):
    for name, prev in before.items():
        now = networks(inspect(service)).get(name)
        if not now:
            if json.loads(output("docker", "network", "inspect", name))[0]["Id"] != prev["NetworkID"]:
                raise RuntimeError("network was replaced: " + name)
            opts = []
            for alias in sorted(set(prev.get("Aliases") or [])):
                opts.extend(["--alias", alias])
            if prev["IPAddress"]:
                opts.extend(["--ip", prev["IPAddress"]])
            opts.extend(["--gw-priority", str(prev.get("GwPriority", 0))])
            result = subprocess.run(["docker", "network", "connect", *opts, name, PROJECT + "-" + service + "-1"],
                                    stderr=subprocess.PIPE)
            now = networks(inspect(service)).get(name)
            if result.returncode and not now:
                result.check_returncode()
        if now["NetworkID"] != prev["NetworkID"]:
            raise RuntimeError("network identity changed: " + name)

def wait_health(expected_image, schema):
    for _ in range(75):
        container = inspect("airlock")
        if container["Image"] != expected_image:
            raise RuntimeError("unexpected backend image")
        if container["State"].get("Health", {}).get("Status") == "healthy":
            try:
                with urlopen(BASE_URL + "/health", timeout=3) as response:
                    health = json.load(response)
                if health.get("status") == "ok" and health.get("db") and health.get("s3") and db_schema() == schema:
                    return health
            except OSError:
                pass
        time.sleep(2)
    raise RuntimeError("backend health or schema verification timed out")

def check_served(expected):
    for _ in range(24):
        try:
            with urlopen(BASE_URL + "/", timeout=3) as response:
                if hashlib.sha256(response.read()).hexdigest() == expected:
                    return
        except OSError:
            pass
        time.sleep(2)
    raise RuntimeError("served frontend hash mismatch")

def verify_images(rows, revision):
    if set(rows) != set(SERVICES):
        raise ValueError("exactly one backend and frontend are required")
    for service, row in rows.items():
        role = "backend" if service == "airlock" else "frontend"
        tag = "aifence-" + service + ":personal-" + revision[:12]
        if row["name"] != role or row["revision"] != revision or row["local_image"] != tag:
            raise ValueError("personal image role, tag or revision mismatch")
        img = json.loads(output("docker", "image", "inspect", tag))[0]
        labels = img["Config"]["Labels"] or {}
        if (img["Id"] != row["local_image_id"] or img["Architecture"] != "amd64"
                or labels.get("org.opencontainers.image.revision") != revision
                or labels.get("io.aifence.personal.source-image") != row["patched_id"]
                or labels.get("io.aifence.personal.archive-sha256") != row["sha256"]):
            raise ValueError("image identity, architecture or provenance mismatch")

def apply(manifest, revision, schema, root):
    verify_release_channel(manifest, revision)
    image_manifest_text = manifest.read_text()
    rows = {row["service"]: row for row in json.loads(image_manifest_text)}
    verify_images(rows, revision)
    local = RUNTIME / "compose.local.yml"
    pointer = RUNTIME / "personal-overlay/manifest.json"
    old_local, old_manifest = local.read_text(), pointer.read_text()
    old_rows = {row["service"]: row for row in json.loads(old_manifest)}
    before = {s: inspect(s) for s in SERVICES}
    initial_schema = db_schema()
    if all(before[s]["Image"] == rows[s]["local_image_id"] and
           old_rows[s]["local_image_id"] == rows[s]["local_image_id"] for s in SERVICES):
        wait_health(rows["airlock"]["local_image_id"], schema)
        check_served(rows["frontend"]["artifact_sha256"])
        print("Personal release is already active and healthy: " + revision)
        return
    if schema < initial_schema:
        raise ValueError("release schema is older than the live database")
    commands = {}
    old_tags = {s: old_rows[s]["local_image"] for s in SERVICES}
    new_tags = {s: rows[s]["local_image"] for s in SERVICES}
    next_local = old_local
    for s in SERVICES:
        c = before[s]
        if (c["Image"] != old_rows[s]["local_image_id"] or c["Config"]["Image"] != old_tags[s]
                or not c["State"]["Running"] or next_local.count(old_tags[s]) != 1):
            raise RuntimeError("live container, persistent pointer or image changed")
        next_local = next_local.replace(old_tags[s], new_tags[s])
        labels = c["Config"]["Labels"]
        if labels["com.docker.compose.project"] != PROJECT:
            raise ValueError("unexpected compose project")
        chain = labels["com.docker.compose.project.config_files"].split(",")
        commands[s] = ["docker", "compose", "--profile", "*", "-p", PROJECT,
                       "--project-directory", labels["com.docker.compose.project.working_dir"]]
        for f in chain:
            if not Path(f).is_file():
                raise ValueError("missing active Compose input")
            commands[s].extend(["-f", f])
    wait_health(old_rows["airlock"]["local_image_id"], initial_schema)
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    overlay = root / "compose.release.json"
    overlay.write_text(json.dumps({"services": {s: {"image": tag} for s, tag in new_tags.items()}}) + "\n")
    def compose_up(service, new):
        cmd = commands[service]
        extra = ["-f", str(overlay)] if new else []
        run(*cmd, *extra, "up", "-d", "--no-deps", "--force-recreate", "--no-build", "--pull", "never", service)
        restore_networks(service, networks(before[service]))
    for s in SERVICES:
        old = json.loads(output(*commands[s], "config", "--format", "json"))["services"][s]
        new = json.loads(output(*commands[s], "-f", overlay, "config", "--format", "json"))["services"][s]
        if old.pop("image") != old_tags[s] or new.pop("image") != new_tags[s] or old != new:
            raise ValueError("compose changes outside the two images")
    backup = root / "backup"
    backup.mkdir(mode=0o700)
    shutil.copy2(pointer, backup / "manifest.json")
    shutil.copy2(local, backup / "compose.local.yml")
    dump = backup / "airlock.dump"
    with dump.open("xb") as stream:
        run("docker", "exec", PROJECT + "-postgres-1", "pg_dump", "-Fc", "-U", "airlock", "airlock", stdout=stream)
    with dump.open("rb") as stream, (backup / "restore.list").open("xb") as listing:
        run("docker", "exec", "-i", PROJECT + "-postgres-1", "pg_restore", "-l", stdin=stream, stdout=listing)
    if dump.stat().st_size == 0 or (backup / "restore.list").stat().st_size == 0:
        raise RuntimeError("empty database backup or restore inventory")
    with (backup / "globals.sql").open("xb") as stream:
        run("docker", "exec", PROJECT + "-postgres-1", "pg_dumpall", "--globals-only",
            "--no-role-passwords", "-U", "airlock", stdout=stream)
    hashes = {f.name: sha(f) for f in sorted(backup.iterdir()) if f.is_file()}
    (backup / "sha256.json").write_text(json.dumps(hashes, indent=2) + "\n")
    wait_idle()
    if pointer.read_text() != old_manifest or local.read_text() != old_local or any(
            inspect(s)["Id"] != before[s]["Id"] for s in SERVICES):
        raise RuntimeError("deployment state changed during backup")
    verify_release_channel(manifest, revision)
    if manifest.read_text() != image_manifest_text:
        raise RuntimeError("image receipt changed during backup")
    changed = []
    try:
        for s in SERVICES:
            changed.append(s)
            compose_up(s, True)
            if inspect(s)["Image"] != rows[s]["local_image_id"]:
                raise RuntimeError("unexpected live image")
            if s == "airlock":
                health = wait_health(rows[s]["local_image_id"], schema)
            artifact = "/airlock" if s == "airlock" else "/srv/index.html"
            if output("docker", "exec", PROJECT + "-" + s + "-1", "sha256sum", artifact).split()[0] != rows[s]["artifact_sha256"]:
                raise RuntimeError("live artifact checksum mismatch")
        check_served(rows["frontend"]["artifact_sha256"])
        if pointer.read_text() != old_manifest or local.read_text() != old_local:
            raise RuntimeError("persistent release pointer changed during deployment")
        save(pointer, image_manifest_text)
        save(local, next_local)
        result = {"status": "healthy", "revision": revision, "schema": db_schema(), "health": health,
                  "images": {s: inspect(s)["Image"] for s in SERVICES}, "backup_sha256": hashes["airlock.dump"]}
        save(root / "rollout.json", json.dumps(result, indent=2) + "\n")
        print(json.dumps(result))
    except BaseException:
        if db_schema() == initial_schema:
            if local.read_text() == next_local:
                save(local, old_local)
            if pointer.read_text() == image_manifest_text:
                save(pointer, old_manifest)
            for s in reversed(changed):
                if inspect(s)["Image"] != old_rows[s]["local_image_id"]:
                    compose_up(s, False)
            wait_health(old_rows["airlock"]["local_image_id"], initial_schema)
            check_served(old_rows["frontend"]["artifact_sha256"])
        raise

def main():
    if sys.argv[1:] == ["--check-installed"]:
        rows, revision = installed_release()
        verify_images(rows, revision)
        return
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--schema", required=True, type=int)
    parser.add_argument("--run-dir", required=True, type=Path)
    args = parser.parse_args()
    if not re.fullmatch("[a-f0-9]{40}", args.revision):
        parser.error("--revision must be a full Git commit")
    verify_release_channel(args.manifest.resolve(), args.revision)
    os.umask(0o077)
    os.environ["DOCKER_HOST"] = "unix://" + str(Path.home() / ".colima/aifence/docker.sock")
    os.environ.pop("DOCKER_CONTEXT", None)
    with (RUNTIME / "personal-overlay/release.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        apply(args.manifest.resolve(), args.revision, args.schema, args.run_dir.resolve())

if __name__ == "__main__":
    main()
