#!/usr/bin/env python3
"""Transfer verified source gzip objects through the native authenticated route.

Only the immutable migration snapshot is read. Each bundle is accepted after
the app verifies all uncompressed SHA-256 values, sizes and native storage
read-back. No collection is activated by this tool.
"""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
from urllib.parse import urlencode

from migrate_native import SOURCES, read_db, sha_file, write_json

BUNDLE_LIMIT = 6 << 20


def pack_groups(db, catalog, source):
    entries = []
    size = 0
    for digest, in db.execute(
        "SELECT DISTINCT sha256 FROM http_responses WHERE source=? ORDER BY sha256", (source,)
    ):
        path = catalog / "raw-html" / digest[:2] / (digest + ".html.gz")
        length = path.stat().st_size
        packed = 512 + ((length + 511) // 512) * 512
        if entries and (size + packed > BUNDLE_LIMIT or len(entries) >= 500):
            yield entries
            entries, size = [], 0
        if packed > BUNDLE_LIMIT:
            raise RuntimeError("single raw object exceeds migration bundle size")
        entries.append((digest, path, length))
        size += packed
    if entries:
        yield entries


def make_bundle(path, entries):
    with tarfile.open(path, "w", format=tarfile.USTAR_FORMAT) as tar:
        for digest, file, size in entries:
            info = tarfile.TarInfo(digest + ".gz")
            info.size = size
            info.mode = 0o600
            info.mtime = 0
            with file.open("rb") as src:
                tar.addfile(info, src)
    return sha_file(path)


def transfer(source, manifest, directory, catalog, client, app):
    log = directory / (source + "-raw.jsonl")
    done = {}
    if log.exists():
        for line in log.read_text().splitlines():
            row = json.loads(line)
            done[row["sha256"]] = row
    sent = 0
    with read_db(directory/"crawl.sqlite") as db, tempfile.TemporaryDirectory(dir=directory) as tmp, log.open("a") as out:
        log.chmod(0o600)
        path = Path(tmp)/"bundle.tar"
        for entries in pack_groups(db, catalog, source):
            digest = make_bundle(path, entries)
            if digest not in done:
                route = "/migration/archive?" + urlencode({"source": source, "snapshot": manifest["snapshot"]})
                cmd = [str(client), "request", "POST", route, "--header", "Content-Type: application/x-tar",
                       "--header", "X-Archive-SHA256: " + digest, "--data-file", str(path), "--timeout", "90s"]
                for attempt in range(2):
                    try:
                        p = subprocess.run(cmd, cwd=app, text=True, capture_output=True, timeout=110)
                    except subprocess.TimeoutExpired:
                        if attempt == 0:
                            continue
                        raise
                    if p.returncode:
                        if attempt == 0 and ("timeout" in p.stderr.lower() or "deadline" in p.stderr.lower()):
                            continue
                        raise RuntimeError("archive transfer failed: " + p.stderr[-500:] + p.stdout[-800:])
                    ack = json.loads(p.stdout)
                    if (ack["sha256"] != digest or ack["snapshot"] != manifest["snapshot"]
                        or ack["source"] != source or ack["objects"] != len(entries)
                        or ack["bytes"] != path.stat().st_size):
                        raise RuntimeError("wrong archive acknowledgement")
                    out.write(json.dumps(ack)+"\n")
                    out.flush()
                    os.fsync(out.fileno())
                    break
            sent += len(entries)
            if sent % 1000 < len(entries):
                print(source, "raw objects verified", sent, flush=True)
        expected = db.execute("SELECT count(distinct sha256) FROM http_responses WHERE source=?", (source,)).fetchone()[0]
        if sent != expected:
            raise RuntimeError("raw count mismatch")
    print(source, "raw objects complete", sent, flush=True)


def verify(directory, client, app):
    manifest = json.loads((directory/"manifest.json").read_text())
    p = subprocess.run([str(client), "tools", "invoke", "legacy_migration_status", "--input", "{}", "--json"],
                       cwd=app, capture_output=True, text=True, timeout=45, check=True)
    raw = json.loads(p.stdout)["data"]["result"]["output"]
    rows = json.loads(raw) if isinstance(raw, str) else raw
    by = {row["source"]: row for row in rows}
    for source, expected in manifest["counts"].items():
        got = by[source]
        if got["snapshot"] != manifest["snapshot"] or got["raw_pending"] != 0:
            raise RuntimeError(f"{source}: incomplete raw archive: {got}")
        for key, n in expected.items():
            if got[key] != n:
                raise RuntimeError(f"{source}: {key} mismatch")
    write_json(directory/"verified-archive.json", rows)
    print("ALL RAW ARCHIVES VERIFIED IN AIRLOCK STORAGE", json.dumps(rows, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    os.umask(0o077)
    p = argparse.ArgumentParser()
    p.add_argument("--catalog", type=Path, required=True)
    p.add_argument("--directory", type=Path, required=True)
    p.add_argument("--client", type=Path, required=True)
    p.add_argument("--app", type=Path, required=True)
    p.add_argument("--probe", action="store_true", help="upload exactly one bounded SoftMap bundle")
    args = p.parse_args()
    manifest = json.loads((args.directory/"manifest.json").read_text())
    if not (args.directory/"verified-metadata.json").is_file() or sha_file(args.directory/"crawl.sqlite") != manifest["snapshot"]:
        raise RuntimeError("verified immutable metadata required")
    if args.probe:
        source="softmap"
        with read_db(args.directory/"crawl.sqlite") as db, tempfile.TemporaryDirectory(dir=args.directory) as tmp:
            entries=next(pack_groups(db,args.catalog,source))
            path=Path(tmp)/"probe.tar"
            digest=make_bundle(path,entries)
            route="/migration/archive?"+urlencode({"source":source,"snapshot":manifest["snapshot"]})
            proc=subprocess.run([str(args.client),"request","POST",route,"--header","Content-Type: application/x-tar","--header","X-Archive-SHA256: "+digest,"--data-file",str(path),"--timeout","90s"],cwd=args.app,text=True,capture_output=True,timeout=110)
            if proc.returncode:
                raise RuntimeError(proc.stderr[-600:]+proc.stdout[-1000:])
            ack=json.loads(proc.stdout)
            assert ack["sha256"]==digest and ack["objects"]==len(entries)
            print("PROBE",json.dumps(ack),flush=True)
    else:
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            futures=[pool.submit(transfer,src,manifest,args.directory,args.catalog,args.client,args.app) for src in SOURCES]
            for f in concurrent.futures.as_completed(futures):
                f.result()
        verify(args.directory,args.client,args.app)
