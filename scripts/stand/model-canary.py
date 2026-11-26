#!/usr/bin/env python3
"""Submit one synthetic request through the stand's Airlock system chat."""
import json
import os
from pathlib import Path
import time
import urllib.error
import urllib.request
import uuid

base = os.environ['AIFENCE_AIRLOCK_URL'].rstrip('/')

def call(method, path, payload=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, method=method, headers=headers,
                                 data=json.dumps(payload).encode() if payload is not None else None)
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        print(method, path.split('?')[0], 'HTTP', error.code)
        raise SystemExit(1)

credential = json.loads((Path(os.environ['AIFENCE_STAND_DATA_ROOT']) / 'operator.json').read_text())
token = call('POST', '/auth/login', credential)['accessToken']
conversation = call('POST', '/api/v1/system/conversations', {'title': 'Synthetic model canary'}, token)['conversation']
cid = conversation['id']
receipt = call('POST', f'/api/v1/conversations/{cid}/prompt', {
    'requestId': str(uuid.uuid4()), 'message': 'Reply with exactly OK.',
}, token)
print('Synthetic prompt accepted:', bool(receipt.get('runId')), flush=True)
deadline = time.monotonic() + 90
while time.monotonic() < deadline:
    snapshot = call('GET', f'/api/v1/conversations/{cid}/chat', token=token)
    messages = snapshot.get('messages', [])
    answers = [m.get('content', '') for m in messages if m.get('role') == 'assistant' and m.get('content')]
    if answers:
        print('Airlock assistant response:', answers[-1][:200], flush=True)
        break
    if not snapshot.get('conversation', {}).get('inFlightRunId'):
        print('No assistant response; run is no longer in flight.', flush=True)
        raise SystemExit(1)
    time.sleep(2)
else:
    print('No assistant response within 90 seconds.', flush=True)
    raise SystemExit(1)
