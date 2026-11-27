#!/usr/bin/env python3
"""Stage changes since the verified immutable SQLite snapshot.

The first snapshot digest remains the migration identity. The delta is
separately sealed and compared by complete row identity, including failure
rows which disappeared after successful parsing. No crawler is started.
"""
import argparse
import concurrent.futures
from contextlib import closing
import datetime as dt
import json
import os
from pathlib import Path
import sqlite3

from migrate_native import SOURCES, batches, invoke_stage, read_db, sha_file, write_json
from migrate_native_archive import transfer, verify


def delta_records(db, source, kind):
    if kind == "responses":
        for row in db.execute("""SELECT n.* FROM http_responses n
            WHERE n.source=? AND NOT EXISTS(SELECT 1 FROM old.http_responses o WHERE o.id=n.id)
            ORDER BY n.id""", (source,)):
            yield dict(row)
        return
    # Compare values, not just timestamps: the legacy scheduler can change a
    # failure row or its evidence without changing the paired task's timestamp.
    changed = db.execute("""SELECT n.* FROM crawl_tasks n
        LEFT JOIN old.crawl_tasks o USING(source,url,kind,generation)
        LEFT JOIN failed_tasks f USING(source,url,kind,generation)
        LEFT JOIN old.failed_tasks g USING(source,url,kind,generation)
        WHERE n.source=? AND (
          o.url IS NULL OR n.state IS NOT o.state OR n.priority IS NOT o.priority
          OR n.updated_at IS NOT o.updated_at OR n.evidence_id IS NOT o.evidence_id
          OR n.lane IS NOT o.lane OR f.url IS NOT g.url
          OR f.error_type IS NOT g.error_type OR f.attempts IS NOT g.attempts
          OR f.next_attempt IS NOT g.next_attempt OR f.evidence_id IS NOT g.evidence_id
          OR f.state IS NOT g.state OR f.detail IS NOT g.detail OR f.updated_at IS NOT g.updated_at
        ) ORDER BY n.url""", (source,))
    failures = {r["url"]: dict(r) for r in db.execute("SELECT * FROM failed_tasks WHERE source=?", (source,))}
    for row in changed:
        r = {"task": dict(row)}
        if row["url"] in failures:
            r["failure"] = failures[row["url"]]
        yield r


def prepare(catalog, baseline, directory):
    base = json.loads((baseline/"manifest.json").read_text())
    if not (baseline/"verified-archive.json").exists() or sha_file(baseline/"crawl.sqlite") != base["snapshot"]:
        raise RuntimeError("verified original snapshot required")
    directory.mkdir(mode=0o700,parents=True,exist_ok=False)
    snap=directory/"crawl.sqlite"
    with closing(read_db(catalog/"crawl.sqlite")) as src, closing(sqlite3.connect(snap)) as dest:
        src.backup(dest,pages=2048,sleep=0.05)
        dest.commit();dest.execute("pragma journal_mode=delete").fetchone()
    snap.chmod(0o600)
    counts={}
    changed={}
    with closing(read_db(snap)) as db:
        if db.execute("pragma quick_check").fetchone()[0]!="ok":raise RuntimeError("delta integrity check")
        db.execute("ATTACH DATABASE ? AS old",(f"file:{baseline/'crawl.sqlite'}?mode=ro",))
        for source in SOURCES:
            oldgen=db.execute("select distinct generation from old.crawl_tasks where source=?",(source,)).fetchall()
            newgen=db.execute("select distinct generation from crawl_tasks where source=?",(source,)).fetchall()
            if [r[0] for r in oldgen]!=[r[0] for r in newgen] or len(newgen)!=1:
                raise RuntimeError(source+": generation changed; needs explicit generation reconciliation")
            tasks,urls=db.execute("select count(*),count(distinct url) from crawl_tasks where source=?",(source,)).fetchone()
            if tasks!=urls:raise RuntimeError("ambiguous URL identities")
            deleted=db.execute("""SELECT count(*) FROM old.crawl_tasks o LEFT JOIN crawl_tasks n USING(source,url,kind,generation) WHERE o.source=? AND n.url IS NULL""",(source,)).fetchone()[0]
            removed=db.execute("""SELECT count(*) FROM old.http_responses o LEFT JOIN http_responses n ON n.id=o.id WHERE o.source=? AND n.id IS NULL""",(source,)).fetchone()[0]
            if deleted or removed:raise RuntimeError("source deleted rows; do not silently shrink staged data")
            failures=db.execute("select count(*) from failed_tasks where source=?",(source,)).fetchone()[0]
            responses=db.execute("select count(*) from http_responses where source=?",(source,)).fetchone()[0]
            counts[source]=dict(tasks=tasks,failures=failures,responses=responses)
            changed[source]=dict(tasks=sum(1 for _ in delta_records(db,source,"tasks")),responses=sum(1 for _ in delta_records(db,source,"responses")))
    manifest={"created_at":dt.datetime.now(dt.timezone.utc).isoformat(),"snapshot":base["snapshot"],
              "delta_digest":sha_file(snap),"baseline":str(baseline),"counts":counts,"changed":changed,
              "collection_owner":"existing_launchagents"}
    write_json(directory/"manifest.json",manifest)
    print(json.dumps(manifest),flush=True)


def stage_one(directory, source, manifest, client, app):
    log=directory/(source+"-delta.jsonl")
    done={x["sha256"] for x in (json.loads(l) for l in log.read_text().splitlines())} if log.exists() else set()
    with closing(read_db(directory/"crawl.sqlite")) as db, log.open("a") as out:
        db.execute("ATTACH DATABASE ? AS old",(f"file:{Path(manifest['baseline'])/'crawl.sqlite'}?mode=ro",))
        for kind,n in manifest["changed"][source].items():
            total=0
            for digest,data,count in batches(source,manifest["snapshot"],kind,delta_records(db,source,kind)):
                if digest not in done:
                    ack=invoke_stage(client,app,data,digest,count)
                    if ack["source"]!=source or ack["snapshot"]!=manifest["snapshot"] or ack["kind"]!=kind:raise RuntimeError("wrong delta acknowledgement")
                    out.write(json.dumps(ack)+"\n");out.flush();os.fsync(out.fileno())
                total+=count
            if total!=n:raise RuntimeError("changed row count mismatch")
            print(source,kind,"delta rows",total,flush=True)


def stage(directory,client,app):
    manifest=json.loads((directory/"manifest.json").read_text())
    if sha_file(directory/"crawl.sqlite")!=manifest["delta_digest"]:raise RuntimeError("delta digest changed")
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        for f in concurrent.futures.as_completed([pool.submit(stage_one,directory,src,manifest,client,app) for src in SOURCES]):f.result()


if __name__=="__main__":
    os.umask(0o077)
    p=argparse.ArgumentParser()
    p.add_argument("action",choices=["prepare","stage","archive"])
    p.add_argument("--catalog",type=Path,required=True)
    p.add_argument("--baseline",type=Path,required=True)
    p.add_argument("--directory",type=Path,required=True)
    p.add_argument("--client",type=Path,required=True)
    p.add_argument("--app",type=Path,required=True)
    a=p.parse_args()
    if a.action=="prepare":prepare(a.catalog,a.baseline,a.directory)
    elif a.action=="stage":stage(a.directory,a.client,a.app)
    else:
        manifest=json.loads((a.directory/"manifest.json").read_text())
        if sha_file(a.directory/"crawl.sqlite")!=manifest["delta_digest"] or Path(manifest["baseline"])!=a.baseline:
            raise RuntimeError("delta digest or baseline mismatch")
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            futures=[pool.submit(transfer,src,manifest,a.directory,a.catalog,a.client,a.app,a.baseline) for src in SOURCES]
            for f in concurrent.futures.as_completed(futures):f.result()
        verify(a.directory,a.client,a.app)
