#!/usr/bin/env python3
"""Create isolated synthetic QA users on the Mac-local Airlock only."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / '.runtime/blind-director/local-source-package'
ORIGIN = 'http://localhost:42082'
OPERATOR = Path('/Users/user/AIfence/.runtime/qa-sol-20261127/operator.json')
REQUIRED = {'product-manager','market-watch','metrika-analyst'}

def assert_local(origin):
    if origin != ORIGIN:raise RuntimeError('Origin is not the explicitly allowed Mac-local Airlock')
    pids=subprocess.check_output(['lsof','-nP','-iTCP:42082','-sTCP:LISTEN','-t'],text=True).split()
    if len(pids)!=1:raise RuntimeError('Local listener identity is ambiguous')
    cmd=subprocess.check_output(['ps','-p',pids[0],'-o','args='],text=True)
    if 'colima-aifence/ssh.sock' not in cmd:raise RuntimeError('Listener is not the Mac Colima aifence forward')
    ports=json.loads(subprocess.check_output(['docker','--context','colima-aifence','inspect','aifence-qa-caddy-local-1','--format','{{json .NetworkSettings.Ports}}'],text=True))
    if {'HostIp':'127.0.0.1','HostPort':'42082'} not in (ports.get('80/tcp') or []):raise RuntimeError('Local container port identity mismatch')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Refusing redirect from local runtime')

OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def call(method, path, token, payload=None):
    if not path.startswith('/') or path.startswith('//'):
        raise ValueError('Invalid local path')
    headers = {'Authorization':'Bearer '+token}
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode(); headers['Content-Type'] = 'application/json'
    req = urllib.request.Request(ORIGIN+path, data=data, headers=headers, method=method)
    try:
        with OPENER.open(req, timeout=15) as res:
            body = res.read()
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f'Local API {method} {path}: HTTP {exc.code}') from None


def save(path, row):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(row, f, ensure_ascii=False); f.flush(); os.fsync(f.fileno())


def main():
    p=argparse.ArgumentParser();p.add_argument('--origin',required=True);p.add_argument('--cases',nargs='+',default=['case-01','case-06'],choices=[f'case-{i:02d}' for i in range(1,12)]);args=p.parse_args()
    assert_local(args.origin)
    info=OPERATOR.stat()
    if info.st_uid!=os.getuid() or info.st_mode & 0o077:raise RuntimeError('Insecure QA operator file')
    operator=json.loads(OPERATOR.read_text())
    admin=call('POST','/auth/login','',operator).get('accessToken')
    if not admin:raise RuntimeError('QA operator login failed')
    me = call('GET', '/api/v1/me', admin)
    if not (me.get('fullAdministrator') or me.get('full_administrator')):
        raise RuntimeError('Local administrator session required to create isolated users')
    rows = call('GET','/api/v1/agents',admin)['agents']
    apps = {x['slug']:x['id'] for x in rows if x.get('slug') in REQUIRED}
    if set(apps) != REQUIRED: raise RuntimeError('Three required local apps not found')
    users = OUT/'users-qa-42082'; users.mkdir(mode=0o700, exist_ok=True)
    for case in args.cases:
        plan = users/(case+'-plan.json'); credential = users/(case+'-credentials.json'); receipt = users/(case+'-receipt.json')
        if receipt.exists():
            print(case+' existing receipt preserved', flush=True)
            continue
        if plan.exists() and not credential.exists():
            raise RuntimeError('Creation outcome uncertain; reconcile before retry: '+case)
        if credential.exists():
            saved = json.loads(credential.read_text()); uid = saved['user_id']
            if saved.get('case') != case or saved.get('origin') != ORIGIN:
                raise RuntimeError('Saved local identity mismatch: '+case)
        else:
            email = 'blind-local-'+secrets.token_hex(12)+'@aifence.invalid'
            save(plan, {'case':case,'email':email,'origin':ORIGIN})
            created = call('POST','/api/v1/users/',admin,{'email':email,'displayName':'QA director','tenantRole':'user'})
            uid = created['user']['id']; temporary = created['tempPassword']
            save(credential, {'case':case,'email':email,'user_id':uid,'password':temporary,'origin':ORIGIN})
        for slug, aid in sorted(apps.items()):
            members = call('GET',f'/api/v1/agents/{aid}/members',admin).get('members',[])
            matches = [x for x in members if x.get('userId') == uid]
            if len(matches) > 1 or (matches and matches[0].get('role') != 'user'):
                raise RuntimeError('Unexpected existing local grant: '+case)
            if not matches:
                call('POST',f'/api/v1/agents/{aid}/members',admin,{'userId':uid,'role':'user'})
            confirmed = call('GET',f'/api/v1/agents/{aid}/members',admin).get('members',[])
            if len([x for x in confirmed if x.get('userId') == uid and x.get('role') == 'user']) != 1:
                raise RuntimeError('Local grant not confirmed: '+case)
        saved=json.loads(credential.read_text());final=users/(case+'-final-credentials.json')
        if not final.exists():save(final,{**saved,'password':secrets.token_urlsafe(32)+'Aa1!'})
        desired=json.loads(final.read_text())
        try:
            login=call('POST','/auth/login','',{'email':desired['email'],'password':desired['password']})
        except RuntimeError:
            login=call('POST','/auth/login','',{'email':saved['email'],'password':saved['password']})
            call('POST','/auth/change-password',login['accessToken'],{'currentPassword':saved['password'],'newPassword':desired['password']})
            login=call('POST','/auth/login','',{'email':desired['email'],'password':desired['password']})
        identity=call('GET','/api/v1/me',login['accessToken'])
        if identity.get('user',{}).get('id')!=uid:raise RuntimeError('Final QA login identity mismatch')
        save(receipt, {'case':case,'user_id':uid,'app_ids':apps,'origin':ORIGIN})
        print(case+' local QA user provisioned', flush=True)

if __name__ == '__main__':
    try: main()
    except Exception as exc:
        print(f'Provisioning stopped: {type(exc).__name__}'+(f': {exc}' if isinstance(exc, RuntimeError) else ''), file=__import__('sys').stderr)
        raise SystemExit(1)
