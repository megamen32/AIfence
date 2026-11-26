#!/usr/bin/env python3
"""Verify one cached release archive and load it into the isolated daemon."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile

root = Path(__file__).resolve().parents[2]
lock = json.loads((root / 'platform/airlock.lock.json').read_text())
name = sys.argv[1]
entry = lock['release_images'][name]
cache = root / '.runtime/image-cache' / lock['revision'][:12]
receipt = json.loads((cache / f'{name}.verified.json').read_text())
assert receipt['config'] == entry['id'] and receipt['revision'] == lock['revision']
archive, = cache.glob(f'{name}-*.tar.gz.part')
h = hashlib.sha256()
with archive.open('rb') as source:
    for chunk in iter(lambda: source.read(1024 * 1024), b''):
        h.update(chunk)
assert h.hexdigest().startswith(archive.name.removeprefix(name + '-').split('.')[0])
with tarfile.open(archive, 'r:gz') as tar:
    raw = tar.extractfile('blobs/sha256/' + receipt['manifest'][7:]).read()
    assert 'sha256:' + hashlib.sha256(raw).hexdigest() == receipt['manifest']
    assert json.loads(raw)['config']['digest'] == entry['id']
    config = tar.extractfile('blobs/sha256/' + entry['id'][7:]).read()
    assert 'sha256:' + hashlib.sha256(config).hexdigest() == entry['id']
    obj = json.loads(config)
    assert obj['architecture'] == 'amd64'
    assert obj['config']['Labels']['org.opencontainers.image.revision'] == lock['revision']
print(f'{name}: archive verified; loading', flush=True)
cmd = ['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5', '-p', os.environ['AIFENCE_SSH_PORT'],
       os.environ['AIFENCE_SSH_TARGET'], 'sudo -n -u aifence-stand env DOCKER_HOST=unix:///run/aifence-stand/docker.sock docker load']
with archive.open('rb') as source:
    subprocess.run(cmd, stdin=source, check=True)
