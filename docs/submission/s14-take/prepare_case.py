#!/usr/bin/env python3
"""Prepare a new ordinary local Airlock owner with a frozen synthetic input, without sending a prompt."""
import argparse
import datetime
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import secrets
import sys
import tempfile
from urllib.error import HTTPError
from urllib.request import Request, HTTPRedirectHandler, build_opener

ALLOWED_ORIGIN = 'http://localhost:42080'
APPS = {'product-manager', 'market-watch', 'metrika-analyst'}
INPUT_FILES = {'metrika.json', 'Продажи.xlsx', 'releases.json', 'backlog.json', 'manifest.json'}


def encoded(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def save(path, value):
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        handle.write(encoded(value))
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(handle.name, path)


def read(path):
    return json.loads(path.read_bytes())


def prepare(input_dir, qa_root):
    if input_dir.is_symlink() or {p.name for p in input_dir.iterdir()} != INPUT_FILES:
        raise ValueError('Expected exactly the five canonical synthetic source files')
    if any(not (input_dir / name).is_file() or (input_dir / name).is_symlink() for name in INPUT_FILES):
        raise ValueError('Inputs must be regular files')
    manifest = read(input_dir / 'manifest.json')
    if manifest.get('synthetic') is not True:
        raise ValueError('Only a synthetic case is supported')
    adapter_dir = qa_root / 'tests/integration/blind_director'
    sys.path.insert(0, str(adapter_dir))
    spec = importlib.util.spec_from_file_location('recording_input_adapter', adapter_dir / 'live_adapter.py')
    adapter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(adapter)
    # Only the label map is needed by the source converter. Derive it from the visible manifest.
    labels = {key: {'label': row['product_label']} for key, row in manifest['catalog'].items()}
    with tempfile.TemporaryDirectory() as temp:
        catalog = Path(temp) / 'catalog_labels.py'
        catalog.write_text('PRODUCTS = ' + repr(labels) + '\n', encoding='utf-8')
        series, context = adapter.prepare_inputs(input_dir, catalog)
    batch, registry = json.loads(series), json.loads(context)
    template = {k: v for k, v in batch.items() if k != 'observations'}
    chunks, rows, size = [], [], 0
    for row in batch['observations']:
        cost = len(encoded(row)) + 256
        if rows and size + cost > 650000:
            chunks.append({**template, 'observations': rows})
            rows, size = [], 0
        rows.append(row)
        size += cost
    if rows:
        chunks.append({**template, 'observations': rows})
    chunks.extend(registry['batches'].values())
    documents = [encoded({'schema_version': 'aifence.case.v1', 'as_of': batch['as_of'], 'batches': [b]}) for b in chunks]
    if not documents or any(len(doc) > 1024 * 1024 for doc in documents):
        raise ValueError('PM source bundle limits exceeded')
    proof = {
        'raw_inputs': {n: digest((input_dir / n).read_bytes()) for n in sorted(INPUT_FILES)},
        'series_sha256': digest(series), 'context_sha256': digest(context),
        'document_sha256': [digest(doc) for doc in documents], 'source_rows': len(batch['observations']),
    }
    return adapter.multipart(series, context), documents, proof


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Local stand redirected the request')


class Stand:
    def __init__(self, origin):
        if origin != ALLOWED_ORIGIN:
            raise ValueError('This recorder is scoped to the Mac-local contest stand')
        self.origin = origin
        self.opener = build_opener(NoRedirect())

    def request(self, method, path, token=None, payload=None, content_type='application/json', app=None):
        if not path.startswith('/') or path.startswith('//'):
            raise ValueError('Expected a local API path')
        if payload is not None and not isinstance(payload, bytes):
            payload = encoded(payload)
        headers = {'Content-Type': content_type, 'Accept': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if app:
            headers['Origin'] = self.origin.replace('localhost', app + '.localhost')
        try:
            with self.opener.open(Request(self.origin + path, data=payload, headers=headers, method=method), timeout=60) as response:
                raw = response.read()
        except HTTPError as error:
            raise RuntimeError(f'{method} {path} returned HTTP {error.code}') from None
        return json.loads(raw) if raw else {}

    def tool(self, token, app, name, params=None):
        result = self.request('POST', f'/api/v1/agents/{app}/deployments/production/tools/{name}/invoke', token, {'input': params or {}})
        decoded = json.loads(result['resultJson'])
        if decoded.get('isError') or decoded.get('is_error'):
            raise RuntimeError('Native tool failed: ' + name)
        output = decoded.get('output', decoded)
        return json.loads(output) if isinstance(output, str) else output


def provision(api, out, credentials):
    admin = read(credentials)['sessions'][api.origin]['accessToken']
    apps = {a['slug']: a['id'] for a in api.request('GET', '/api/v1/agents', admin)['agents'] if a['slug'] in APPS}
    if set(apps) != APPS:
        raise RuntimeError('Required native apps missing')
    access = out / 'access.json'
    if not access.exists():
        plan = out / 'new-owner.json'
        if plan.exists():
            raise RuntimeError('Uncertain previous owner creation. Reconcile the local receipt before retrying.')
        email = 'qa-director-' + secrets.token_hex(8) + '@aifence.invalid'
        save(plan, {'email': email})
        user = api.request('POST', '/api/v1/users/', admin, {'email': email, 'displayName': 'Директор цифровых каналов', 'tenantRole': 'user'})
        save(access, {'email': email, 'user_id': user['user']['id'], 'temp': user['tempPassword'], 'password': secrets.token_urlsafe(32) + 'aA8!', 'origin': api.origin})
    owner = read(access)
    if owner['origin'] != api.origin:
        raise RuntimeError('Saved identity belongs to a different host')
    login = lambda pw: api.request('POST', '/auth/login', payload={'email': owner['email'], 'password': pw})['accessToken']
    if 'temp' in owner:
        try:
            token = login(owner['password'])
        except RuntimeError:
            token = login(owner['temp'])
            api.request('POST', '/auth/change-password', token, {'currentPassword': owner['temp'], 'newPassword': owner['password']})
        del owner['temp']
        save(access, owner)
    for aid in apps.values():
        route = f'/api/v1/agents/{aid}/members'
        matches = [m for m in api.request('GET', route, admin)['members'] if m['userId'] == owner['user_id']]
        if not matches:
            api.request('POST', route, admin, {'userId': owner['user_id'], 'role': 'user'})
        elif len(matches) != 1 or matches[0]['role'] != 'user':
            raise RuntimeError('Unexpected membership for the new demo owner')
    token = login(owner['password'])
    me = api.request('GET', '/api/v1/me', token)
    owner_apps = {a['slug']: a['id'] for a in api.request('GET', '/api/v1/agents', token)['agents']}
    if owner_apps != apps or me['user']['id'] != owner['user_id'] or me.get('fullAdministrator') or me.get('full_administrator'):
        raise RuntimeError('Local ordinary owner identity verification failed')
    return owner['user_id'], token, apps


def ensure_no_conversations_or_cards(api, token, pm):
    chats = api.request('GET', '/api/v1/system/conversations', token)['conversations']
    app_chats = api.request('GET', f'/api/v1/agents/{pm}/conversations', token)['conversations']
    if chats or app_chats or api.tool(token, pm, 'list_hypotheses')['records']:
        raise RuntimeError('Owner already has a conversation or a card. Use a fresh output directory.')


def stage(args, upload, docs, proof):
    out = args.out.resolve()
    if out.exists() and not args.resume:
        raise ValueError('Choose a fresh --out directory or explicitly --resume an unfinished attempt')
    out.mkdir(mode=0o700, parents=True, exist_ok=args.resume)
    if out.stat().st_uid != os.getuid() or out.stat().st_mode & 0o077:
        raise ValueError('The output directory must be private to the current OS user')
    import fcntl
    with (out / 'prepare.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if (out / 'inputs.json').exists() and read(out / 'inputs.json') != proof:
            raise ValueError('The supplied inputs differ from this attempt')
        save(out / 'inputs.json', proof)
        api = Stand(args.origin)
        uid, token, apps = provision(api, out, args.operator_session)
        pm, met = apps['product-manager'], apps['metrika-analyst']
        ensure_no_conversations_or_cards(api, token, pm)
        known = {read(p)['snapshot_id'] for p in out.glob('pm-*.receipt.json')}
        visible = {x['snapshot_id'] for x in api.tool(token, pm, 'list_evidence')['snapshots']}
        if visible != known:
            raise RuntimeError('Existing snapshots differ from verified receipts. Reconcile before resuming.')
        watchdog = f'/api/v1/agents/{met}/deployments/production/http/watchdog'
        for phase, (body, ctype), expected in [('upload', upload, 'stored'), ('select', (b'action=select_blind', 'application/x-www-form-urlencoded'), 'selected')]:
            path = out / ('metrika-' + phase + '.json')
            if not path.exists():
                result = api.request('POST', watchdog, token, body, ctype, 'metrika-analyst')
                if result.get('status') != expected:
                    raise RuntimeError('Metrika failed to confirm ' + phase)
                save(path, result)
        discover = api.tool(token, met, 'discover_watchdog_series')
        scan = api.tool(token, met, 'scan_watchdog_series')
        if discover.get('status') != 'ok' or not discover.get('dataset_id') or discover['dataset_id'] != scan.get('dataset_id'):
            raise RuntimeError('The native active Metrika dataset could not be verified')
        ids = []
        for i, doc in enumerate(docs):
            path = out / f'pm-{i:02}.receipt.json'
            if not path.exists():
                result = api.request('POST', f'/api/v1/agents/{pm}/deployments/production/http/import', token, doc, app='product-manager')
                if not result.get('snapshot_id'):
                    raise RuntimeError('PM import missing a receipt')
                save(path, result)
            ids.append(read(path)['snapshot_id'])
        if {x['snapshot_id'] for x in api.tool(token, pm, 'list_evidence')['snapshots']} != set(ids):
            raise RuntimeError('Native PM index differs from the imported sources')
        first = json.loads(docs[0])['batches'][0]
        metric = first['observations'][0]['metric_key']
        sample = api.tool(token, pm, 'read_evidence', {'snapshot_id': ids[0], 'source': first['source'], 'metric_key': metric, 'limit': 4})
        if not any(math.isfinite(float(r['value'])) and r.get('unit') and r.get('period') and r.get('source_ref', {}).get('locator') for r in sample['rows']):
            raise RuntimeError('Native PM sample lacks a numeric source locator')
        ensure_no_conversations_or_cards(api, token, pm)
        receipt = {**proof, 'origin': args.origin, 'owner_id': uid, 'apps': apps, 'dataset_id': discover['dataset_id'], 'as_of': discover.get('as_of'), 'pm_snapshots': ids, 'pm_cards': 0, 'system_chats': 0, 'agent_chats': 0, 'sample_rows': len(sample['rows']), 'verified_at': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        save(out / 'receipt.json', receipt)
        print('READY', out / 'receipt.json')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', type=Path, required=True)
    parser.add_argument('--origin', default=ALLOWED_ORIGIN, choices=[ALLOWED_ORIGIN])
    parser.add_argument('--qa-root', type=Path, default=Path('/Users/user/AIfence'))
    parser.add_argument('--out', type=Path)
    parser.add_argument('--operator-session', type=Path, default=Path.home() / 'Library/Application Support/airlock/credentials.json')
    parser.add_argument('--check-only', action='store_true', help='Calculate source and derived hashes without network access or account creation')
    parser.add_argument('--resume', action='store_true', help='Resume only an unfinished isolated attempt with no existing conversations or cards')
    args = parser.parse_args()
    if not args.check_only and args.out is None:
        parser.error('--out is required when preparing a live owner')
    upload, docs, proof = prepare(args.inputs, args.qa_root)
    if args.check_only:
        print(json.dumps(proof, ensure_ascii=False, indent=2))
    else:
        stage(args, upload, docs, proof)


if __name__ == '__main__':
    main()
