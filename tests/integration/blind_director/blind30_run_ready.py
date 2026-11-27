#!/usr/bin/env python3
"""Run prepared local cases once, with a shared two-chat limit."""
import concurrent.futures
import datetime as dt
import json
import subprocess
import sys
import time
from pathlib import Path

import blind30_score

ROOT = Path(__file__).resolve().parents[3]
PACKAGE = ROOT / '.runtime/blind-director/blind30-sol-v2'


def run(case):
    return subprocess.run([sys.executable, str(Path(__file__).with_name('local_chat.py')),
                           '--origin', 'http://localhost:42082', '--blind30', '--case', case,
                           '--matrix', 'source-matrix-qa-42082-v8.json'], cwd=ROOT).returncode


def main():
    _, manifest = blind30_score.verify(PACKAGE)
    pending = [x['id'] for x in manifest['cases'] if not (PACKAGE/'records'/f"{x['id']}.json").exists()]
    active = {}
    blocked = False
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        while pending or active:
            for case in pending[:]:
                folder = PACKAGE/'local-inputs'/case
                if len(active) >= 2 or blocked:break
                if (folder/'sol-chat').exists():
                    print(case, 'existing admission requires reconciliation', flush=True)
                    pending.remove(case)
                elif (folder/'collect-all-9f'/'review.json').is_file():
                    active[pool.submit(run, case)] = case
                    pending.remove(case)
            done = [future for future in active if future.done()]
            for future in done:
                case = active.pop(future)
                code=future.result()
                print(case, 'runner exit', code, flush=True)
                if code:blocked=True
            if blocked and not active:
                print('Admission or transport needs reconciliation; remaining cases not started:', ', '.join(pending), flush=True)
                break
            if not active and pending and dt.datetime.now().hour >= 13:
                print('Deadline reached; not ready:', ', '.join(pending), flush=True)
                break
            time.sleep(2)


if __name__ == '__main__':
    main()
