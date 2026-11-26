#!/usr/bin/env python3
"""Run on server-88: verify one cache archive and load it on server-100."""
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

name, expected = sys.argv[1:]
revision = 'be0f9ee03be31ee15dacd5f6ebcc0f28acea6468'
base = Path.home() / '.cache/aifence-release' / revision
meta = json.loads((base / f'{name}.json').read_text())
archive = base / f'{name}.tar.gz'
assert meta['id'] == expected and meta['revision'] == revision
assert archive.stat().st_size == meta['size']
h = hashlib.sha256()
with archive.open('rb') as f:
    for chunk in iter(lambda: f.read(1024 * 1024), b''):
        h.update(chunk)
assert h.hexdigest() == meta['sha256']
print(f'{name}: cache SHA-256 verified; loading', flush=True)
remote = ['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5',
          os.environ['AIFENCE_CACHE_TARGET'],
          'sudo -n -u aifence-stand env DOCKER_HOST=unix:///run/aifence-stand/docker.sock docker load']
with archive.open('rb') as f:
    subprocess.run(remote, stdin=f, check=True)
