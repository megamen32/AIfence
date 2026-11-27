#!/usr/bin/env python3
"""One-time offline metadata transfer to the existing Market Watch app.

This does not crawl, replace a worker, enable a store, read credentials, or
mark an HTML object imported. The official authenticated air CLI transports
each digest-idempotent batch to the app-owned Postgres schema.
"""
import argparse
import base64
import concurrent.futures
import datetime as dt
import gzip
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time

SOURCES = ("softline", "softmagazin", "softmap")


def write_json(path, value):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2))
    temp.chmod(0o600)
    os.replace(temp, path)


def sha_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for block in iter(lambda: file.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def read_db(path):
    conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True, timeout=30)
    conn.row_factory = sqlite3.Row
    return conn


def prepare(catalog, directory):
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    snapshot = directory / "crawl.sqlite"
    with read_db(catalog / "crawl.sqlite") as source, sqlite3.connect(snapshot) as target:
        source.backup(target, pages=2048, sleep=0.05)
        target.commit()
    snapshot.chmod(0o600)
    counts = {}
    with read_db(snapshot) as db:
        if db.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise RuntimeError("SQLite snapshot failed integrity check")
        for source in SOURCES:
            tasks, urls, generations = db.execute(
                "SELECT count(*),count(distinct url),count(distinct generation) FROM crawl_tasks WHERE source=?", (source,)).fetchone()
            failures = db.execute("SELECT count(*) FROM failed_tasks WHERE source=?", (source,)).fetchone()[0]
            orphan = db.execute("""SELECT count(*) FROM failed_tasks f LEFT JOIN crawl_tasks t USING(source,url,kind,generation)
                WHERE f.source=? AND t.url IS NULL""", (source,)).fetchone()[0]
            responses = db.execute("SELECT count(*) FROM http_responses WHERE source=?", (source,)).fetchone()[0]
            if tasks != urls or generations != 1 or orphan:
                raise RuntimeError(f"{source}: ambiguous task keys or orphan failures")
            counts[source] = dict(tasks=tasks, failures=failures, responses=responses)
        hashes = [row[0] for row in db.execute("SELECT DISTINCT sha256 FROM http_responses")]
    missing = [h for h in hashes if not (catalog/"raw-html"/h[:2]/(h+".html.gz")).is_file()]
    manifest = {"created_at": dt.datetime.now(dt.timezone.utc).isoformat(), "snapshot": sha_file(snapshot),
                "source_catalog": str(catalog), "counts": counts, "distinct_raw_hashes": len(hashes),
                "missing_local_raw": len(missing), "raw_migration": "not_uploaded",
                "collection_owner": "existing_launchagents"}
    write_json(directory / "manifest.json", manifest)
    print(json.dumps(manifest, ensure_ascii=False), flush=True)


def encode(source, snapshot, kind, rows):
    raw = json.dumps({"source": source, "snapshot": snapshot, kind: rows},
                     ensure_ascii=False, separators=(",", ":")).encode()
    digest = hashlib.sha256(raw).hexdigest()
    data = json.dumps({"sha256": digest, "gzip_b64": base64.b64encode(gzip.compress(raw, compresslevel=5, mtime=0)).decode()},
                      separators=(",", ":"))
    return digest, data


def records(db, source, kind):
    if kind == "responses":
        for row in db.execute("SELECT * FROM http_responses WHERE source=? ORDER BY id", (source,)):
            yield dict(row)
        return
    failures = {row["url"]: dict(row) for row in db.execute("SELECT * FROM failed_tasks WHERE source=?", (source,))}
    for row in db.execute("SELECT * FROM crawl_tasks WHERE source=? ORDER BY url", (source,)):
        result = {"task": dict(row)}
        if row["url"] in failures:
            result["failure"] = failures[row["url"]]
        yield result


def batches(source, snapshot, kind, rows):
    def split(chunk):
        digest, data = encode(source, snapshot, kind, chunk)
        if len(data.encode()) > 110000:
            if len(chunk) == 1:
                raise RuntimeError("single metadata record exceeds CLI input limit")
            middle = len(chunk)//2
            yield from split(chunk[:middle])
            yield from split(chunk[middle:])
        else:
            yield digest, data, len(chunk)
    chunk = []
    for row in rows:
        chunk.append(row)
        if len(chunk) == 2000:
            yield from split(chunk)
            chunk = []
    if chunk:
        yield from split(chunk)


def invoke_stage(air, app, data, digest, n):
    cmd = [str(air), "request", "POST", "/migration/stage",
           "--header", "Content-Type: application/json", "--data", data, "--timeout", "45s"]
    # Exact body digest makes repeating an unknown outcome idempotent.
    for attempt in range(2):
        try:
            proc = subprocess.run(cmd, cwd=app, text=True, capture_output=True, timeout=65)
        except subprocess.TimeoutExpired:
            if attempt == 0:
                continue
            raise
        if proc.returncode != 0:
            if attempt == 0 and ("timeout" in proc.stderr.lower() or "deadline" in proc.stderr.lower()):
                continue
            raise RuntimeError("Airlock stage failed: "+proc.stderr[-400:]+proc.stdout[-600:])
        ack = json.loads(proc.stdout)
        if ack.get("sha256") != digest or ack.get("rows") != n:
            raise RuntimeError("migration acknowledgement does not match batch")
        return ack
    raise RuntimeError("stage call did not finish")


def stage_source(directory, source, manifest, air, app):
    log = directory / (source+".jsonl")
    done = {}
    if log.exists():
        for line in log.read_text().splitlines():
            row = json.loads(line)
            done[row["sha256"]] = row
    imported = {"tasks": 0, "responses": 0}
    with read_db(directory/"crawl.sqlite") as db, log.open("a") as out:
        log.chmod(0o600)
        for kind in imported:
            for digest, payload, n in batches(source, manifest["snapshot"], kind, records(db, source, kind)):
                if digest not in done:
                    ack = invoke_stage(air, app, payload, digest, n)
                    if ack["source"] != source or ack["snapshot"] != manifest["snapshot"] or ack["kind"] != kind:
                        raise RuntimeError("wrong source/snapshot acknowledgement")
                    out.write(json.dumps(ack)+"\n")
                    out.flush()
                    os.fsync(out.fileno())
                imported[kind] += n
                if imported[kind] % 10000 < n or imported[kind] == manifest["counts"][source][kind]:
                    print(source, kind, imported[kind], "/", manifest["counts"][source][kind], flush=True)
            if imported[kind] != manifest["counts"][source][kind]:
                raise RuntimeError("snapshot count mismatch")
    return source, imported


def stage(directory, air, app):
    manifest = json.loads((directory/"manifest.json").read_text())
    if sha_file(directory/"crawl.sqlite") != manifest["snapshot"]:
        raise RuntimeError("immutable snapshot digest changed")
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        futures = [pool.submit(stage_source, directory, src, manifest, air, app) for src in SOURCES]
        for future in concurrent.futures.as_completed(futures):
            print("acknowledged", future.result(), flush=True)


def verify(directory, air, app):
    manifest = json.loads((directory/"manifest.json").read_text())
    p = subprocess.run([str(air), "tools", "invoke", "legacy_migration_status", "--input", "{}", "--json"],
                       cwd=app, capture_output=True, text=True, timeout=30, check=True)
    raw = json.loads(p.stdout)["data"]["result"]["output"]
    rows = json.loads(raw) if isinstance(raw, str) else raw
    by = {row["source"]: row for row in rows}
    for source, expected in manifest["counts"].items():
        got = by.get(source, {})
        if got.get("snapshot") != manifest["snapshot"]:
            raise RuntimeError(f"{source}: unexpected staged snapshot")
        for k, n in expected.items():
            if got.get(k) != n:
                raise RuntimeError(f"{source} {k}: expected {n}, got {got.get(k)}")
        if got["task_receipts"] != expected["tasks"] or got["response_receipts"] != expected["responses"]:
            raise RuntimeError(f"{source}: batch receipt count mismatch")
    write_json(directory/"verified-metadata.json", rows)
    print("POSTGRES METADATA VERIFIED", json.dumps(rows, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    os.umask(0o077)
    p = argparse.ArgumentParser()
    p.add_argument("action", choices=["prepare", "stage", "verify"])
    p.add_argument("--catalog", type=Path, required=True)
    p.add_argument("--directory", type=Path, required=True)
    p.add_argument("--air", type=Path, required=True)
    p.add_argument("--app", type=Path, required=True)
    args = p.parse_args()
    if args.action == "prepare":
        prepare(args.catalog, args.directory)
    elif args.action == "stage":
        stage(args.directory, args.air, args.app)
    else:
        verify(args.directory, args.air, args.app)
