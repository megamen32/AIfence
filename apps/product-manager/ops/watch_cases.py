#!/usr/bin/env python3
"""Watch the four product research inputs and sync changed content with local Airlock."""
import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import signal
import subprocess
import sys
import tempfile


APP = Path(__file__).resolve().parents[1]
ROOT = APP.parents[1]
STATE = ROOT / ".runtime" / "product-manager" / "watch"
MATERIALS = ROOT / "материалы" / "Материалы заказчика"
LABEL = "org.aifence.product-manager.watch"
PATTERNS = ("Датасет*.xlsx", "Store*.xlsx", "Транскрибации интервью.zip", "Конкурентный_анализ*.pptx")
CHECKPOINT = "checkpoint.json"
FORMAT_VERSION = 1


def inputs(folder):
    paths = []
    for pattern in PATTERNS:
        found = sorted(p for p in folder.glob(pattern) if p.is_file() and not p.name.startswith("~$"))
        if len(found) != 1:
            raise ValueError(f"Expected one input matching {pattern}, found {len(found)}")
        paths.extend(found)
    return paths


def hashes(paths):
    result = {}
    for path in paths:
        digest = hashlib.sha256()
        with path.open("rb") as f:
            for block in iter(lambda: f.read(1 << 20), b""):
                digest.update(block)
        result[path.name] = digest.hexdigest()
    return result


def write_atomic(path, data):
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as f:
        try:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
            f.close()
            os.replace(f.name, path)
        finally:
            Path(f.name).unlink(missing_ok=True)


def run_sync(materials, output):
    command = [sys.executable, str(APP / "sync_case.py"), "--materials", str(materials), "--output", str(output)]
    with subprocess.Popen(command, cwd=APP, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          text=True, start_new_session=True) as child:
        try:
            stdout, _ = child.communicate(timeout=420)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGKILL)
            child.communicate()
            raise RuntimeError("Local sync timed out; retrying the next launch")
        exit_code = child.returncode
    if exit_code:
        # The child may report customer inputs or CLI diagnostics. Keep launchd's log public-safe.
        raise RuntimeError(f"Local sync exited {exit_code}; run sync_case.py interactively for diagnostics")
    try:
        receipt = json.loads(stdout.rstrip().splitlines()[-1])
        if not all(re.fullmatch(r"ev-[0-9a-f]{64}", receipt[k]) for k in ("snapshot_id","evidence_snapshot_id")):
            raise ValueError("Invalid snapshot ID")
        if not re.fullmatch(r"orders-[0-9a-f]{16}", receipt["hypothesis_id"]):
            raise ValueError("Invalid local investigation ID")
        if int(receipt["observations"]) < 1 or int(receipt["version"]) < 1 or not receipt["status"]:
            raise ValueError("Invalid native receipt")
    except (IndexError, TypeError, KeyError, ValueError) as exc:
        raise RuntimeError("No confirmed import/investigation receipt from local Airlock") from exc
    return {k:receipt[k] for k in ("snapshot_id","observations","hypothesis_id","evidence_snapshot_id","version","status")}


def check_once(folder, state, sync=run_sync):
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (state/"worker.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return None
        checkpoint = state/CHECKPOINT
        previous = json.loads(checkpoint.read_bytes()) if checkpoint.exists() else {}
        paths = inputs(folder)
        signatures = hashes(paths)
        if previous.get("version") == FORMAT_VERSION and previous.get("inputs") == signatures:
            return None
        # A source may be replaced between the event and the import. Send one verified snapshot.
        # A second run after a change is safe because native snapshot/task IDs are idempotent.
        with tempfile.TemporaryDirectory(dir=state,prefix="inputs-") as temp:
            staging = Path(temp)
            for src in paths:
                shutil.copyfile(src, staging/src.name)
            if hashes(inputs(staging)) != signatures:
                raise RuntimeError("Input changed during copy; retrying on the next launch")
            receipt = sync(staging,state.parent/"evidence.json")
        result = dict(version=FORMAT_VERSION, synced_at=datetime.now(timezone.utc).isoformat(timespec="seconds"),
                      inputs=signatures, native_receipt=receipt,
                      previous_case_pinned=receipt["snapshot_id"]!=receipt["evidence_snapshot_id"])
        write_atomic(checkpoint, json.dumps(result,ensure_ascii=False,indent=2).encode("utf-8")+b"\n")
        if hashes(inputs(folder)) != signatures:
            raise RuntimeError("New inputs arrived while syncing; retrying the newer version")
        return result


def config():
    return {
        "Label": LABEL,
        "ProgramArguments": [sys.executable,"-B",str(Path(__file__).resolve()),"check"],
        "WorkingDirectory": str(APP),
        "RunAtLoad": True,
        "StartInterval": 10800,
        "WatchPaths": [str(MATERIALS),*(str(p) for p in inputs(MATERIALS))],
        "KeepAlive": {"SuccessfulExit":False},
        "ThrottleInterval": 300,
        "ProcessType": "Background",
        "Umask": 0o077,
        "StandardOutPath": str(STATE/"watch.log"),
        "StandardErrorPath": str(STATE/"errors.log"),
    }


def launch(*args, check=True):
    return subprocess.run(["/bin/launchctl",*args], capture_output=True,text=True,check=check)


def install():
    state=STATE
    state.mkdir(mode=0o700,parents=True,exist_ok=True)
    plist=state/(LABEL+".plist")
    wanted=plistlib.dumps(config())
    domain=f"gui/{os.getuid()}"
    target=f"{domain}/{LABEL}"
    current=launch("print",target,check=False)
    if not current.returncode:
        if f"path = {plist}\n" not in current.stdout:
            raise RuntimeError("Label is already registered to a different launch definition")
        if plist.exists() and plist.read_bytes()==wanted:
            print(f"Registered {target}")
            return
        launch("bootout",target)
    write_atomic(plist,wanted)
    launch("bootstrap",domain,str(plist))
    print(f"Registered {target}")


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=["install","check","status"])
    args=p.parse_args()
    os.umask(0o077)
    if args.action=="install":
        install()
    elif args.action=="check":
        rec=check_once(MATERIALS,STATE)
        if rec:
            print(json.dumps({"synced_at":rec["synced_at"],"snapshot_id":rec["native_receipt"]["snapshot_id"],
                              "previous_case_pinned":rec["previous_case_pinned"]}))
    else:
        path=STATE/CHECKPOINT
        if not path.exists():
            print("No successful sync recorded")
            return
        state=json.loads(path.read_bytes())
        print(json.dumps({k:v for k,v in state.items() if k!="inputs"},ensure_ascii=False))


if __name__=="__main__":
    try:
        main()
    except (OSError,ValueError,RuntimeError,subprocess.SubprocessError) as exc:
        print(f"{datetime.now(timezone.utc).isoformat(timespec='seconds')} {exc}",file=sys.stderr)
        raise SystemExit(1)
