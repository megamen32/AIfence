#!/usr/bin/env python3
"""Activate a new isolated stand and register its initial model provider."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.error
import urllib.request

assert os.getuid() != 0
assert os.environ.get('DOCKER_HOST') == 'unix:///run/aifence-stand/docker.sock'
base = os.environ['AIFENCE_AIRLOCK_URL'].rstrip('/')
model = os.environ.get('AIFENCE_EXEC_MODEL', 'gemma4:aifence-64k')
build_model = os.environ.get('AIFENCE_BUILD_MODEL', 'gemma4:latest')
operator = Path(os.environ['AIFENCE_STAND_DATA_ROOT']) / 'operator.json'

def request(method, path, body=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    raw = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=raw, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            print(method, path, response.status, flush=True)
            return json.load(response)
    except urllib.error.HTTPError as error:
        print(method, path, 'HTTP', error.code, flush=True)
        raise SystemExit(1)

status = request('GET', '/auth/status')
if status['activated']:
    raise SystemExit('Stand is already activated; refusing to change its provider or settings.')
if not status['activated']:
    if operator.exists():
        raise SystemExit('Operator file exists before activation; inspect state.')
    credential = {'email': 'stand-admin@aifence.invalid', 'password': secrets.token_urlsafe(32) + 'Aa1!'}
    fd = os.open(operator, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(credential, f)
    code = subprocess.check_output(['docker', 'exec', 'aifence-stand-airlock-1',
                                    'cat', '/var/lib/airlock/activation_code.txt'], text=True).strip()
    result = request('POST', '/auth/activate', {**credential, 'displayName': 'Stand Admin', 'activationCode': code})
token = result['accessToken']
providers = request('GET', '/api/v1/providers', token=token).get('providers', [])
matches = [p for p in providers if p.get('slug') == 'server44-gemma']
if len(matches) > 1:
    raise SystemExit('Duplicate stand provider; inspect state.')
if matches:
    provider = matches[0]
else:
    provider = request('POST', '/api/v1/providers', {
        'providerId': 'openai-compatible', 'slug': 'server44-gemma',
        'displayName': 'Server-44 Gemma (synthetic canary)',
        'baseUrl': os.environ['AIFENCE_OLLAMA_URL'],
    }, token)['provider']
provider_id = provider['id']
discovery = request('POST', f'/api/v1/providers/{provider_id}/discover-models', {}, token)
found = {x.get('modelId') for x in discovery.get('candidates', [])}
if model not in found or build_model not in found:
    raise SystemExit('Configured models were not both discovered; settings unchanged.')
models = [{'modelId': name, 'displayName': name, 'toolCall': True,
           'contextLimit': 65536 if name == model else 8192, 'outputLimit': 1024}
          for name in dict.fromkeys((model, build_model))]
request('PUT', f'/api/v1/providers/{provider_id}/models', {'models': models}, token)
settings = request('GET', '/api/v1/settings', token=token)['settings']
settings.update({'defaultExecModel': model, 'defaultExecProviderId': provider_id,
                 'defaultBuildModel': build_model, 'defaultBuildProviderId': provider_id})
request('PUT', '/api/v1/settings', {'settings': settings}, token)
print('Synthetic-only provider configured; no customer content sent.')
