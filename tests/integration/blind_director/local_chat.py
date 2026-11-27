#!/usr/bin/env python3
"""Local QA chat transport, fail-closed until independently evidenced KT1."""
import argparse
import fcntl
import json
import math
import os
from pathlib import Path
import time
import uuid
import local_qa_users as qa
from prompt_guard import PRICE_ACCEPTANCE, require_prompt

ROUTE=Path('/Users/user/AIfence/.runtime/qa-sol-20261127/route.json')
CASES=[f'case-{i:02d}' for i in range(1,12)]


def numeric_read(path, visible):
    if not path.is_file():return False
    doc=json.loads(path.read_text());out=doc.get('output') or {}
    rows=out.get('observations') or []
    if out.get('snapshot_id') not in visible or out.get('status')!='available':return False
    return any(isinstance(r.get('value'),(int,float)) and not isinstance(r.get('value'),bool)
               and math.isfinite(r['value']) and r.get('unit') and r.get('period') and (r.get('source_ref') or {}).get('locator') for r in rows if isinstance(r,dict))


def kt1(matrix_path,offline_path):
    if matrix_path.parent.resolve()!=qa.OUT.resolve() or offline_path.parent.resolve()!=qa.OUT.resolve():raise RuntimeError('KT1 evidence must be in the private package')
    if not matrix_path.is_file() or not offline_path.is_file():raise RuntimeError('KT1: source matrix or measured offline score is missing')
    matrix=json.loads(matrix_path.read_text());offline=json.loads(offline_path.read_text())
    if offline.get('positive_denominator',0)<1 or offline.get('negative_denominator',0)<1 or not offline.get('frozen_evidence_sha256'):
        raise RuntimeError('KT1: no measurable offline hit/false-alarm denominators')
    for case in CASES:
        for source in ('metrika','sales','wordstat','google_trends','market','backlog'):
            item=matrix.get(case,{}).get(source,{})
            if item.get('input_rows',0)>0:
                if item.get('state')!='direct_PM_read' or not item.get('receipts'):
                    raise RuntimeError('KT1: missing direct source read for '+case+'/'+source)
            elif item.get('state')!='absent':
                raise RuntimeError('KT1: absent source is not explicitly recorded for '+case+'/'+source)
        for source in ('sales','wordstat','google_trends','market'):
            if matrix.get(case,{}).get(source,{}).get('input_rows',0)<=0:
                raise RuntimeError('KT1: required source absent for '+case+'/'+source)
        if matrix[case]['metrika'].get('input_rows',0)>0:
            if matrix[case]['metrika'].get('native_status')!='direct_native_discovery':
                raise RuntimeError('KT1: native Metrika not confirmed for '+case)
            location=qa.OUT/case/'receipts-qa-42082'
            native=json.loads((location/'metrika-qa-discover.json').read_text())['output']
            scan=json.loads((location/'metrika-qa-scan.json').read_text())['output']
            if native.get('dataset_id')!=scan.get('dataset_id') or not native.get('dataset_id'):
                raise RuntimeError('KT1: native dataset differs from discovery for '+case)
            candidates=scan.get('candidates') or []
            valid=any(any(isinstance(v,(int,float)) and not isinstance(v,bool) and math.isfinite(v) for v in c.values()) and any(isinstance(ref,dict) and ref.get('locator') for ref in c.get('source_refs',[])) for c in candidates)
            if not valid:raise RuntimeError('KT1: native numeric Metrika locator missing for '+case)
        receipt=qa.OUT/case/'receipts-qa-42082'
        listing=receipt/'discovery-list.json'
        if not listing.is_file():raise RuntimeError('KT1: owner snapshot discovery missing for '+case)
        visible={x.get('snapshot_id') for x in json.loads(listing.read_text()).get('output',{}).get('snapshots',[])}
        for name in ('read_wordstat','read_google_trends','read_competitor_prices'):
            if not numeric_read(receipt/(name+'-discover.json'),visible):raise RuntimeError('KT1: numeric specialist read missing for '+case+'/'+name)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--origin',required=True);parser.add_argument('--case',required=True,choices=CASES);parser.add_argument('--matrix',default='source-matrix-qa-42082-v6.json');parser.add_argument('--offline-score',default='sourcewatch-measured-score.json');args=parser.parse_args()
    qa.assert_local(args.origin)
    kt1(qa.OUT/args.matrix,qa.OUT/args.offline_score)
    route=json.loads(ROUTE.read_text())
    if route.get('origin')!=args.origin or route.get('route_verified') is not True or 'sol' not in str(route.get('model','')).lower() or not route.get('provider'):
        raise RuntimeError('QA Sol route not independently verified')
    user=json.loads((qa.OUT/'users-qa-42082'/f'{args.case}-final-credentials.json').read_text())
    token=qa.call('POST','/auth/login','',{'email':user['email'],'password':user['password']})['accessToken']
    me=qa.call('GET','/api/v1/me',token)
    if me.get('user',{}).get('id')!=user['user_id']:raise RuntimeError('QA identity mismatch')
    apps=qa.call('GET','/api/v1/agents',token)['agents']
    if {x['slug'] for x in apps}!=qa.REQUIRED:raise RuntimeError('Unexpected QA app grants')
    pm=next(x for x in apps if x['slug']=='product-manager')
    configured_model=pm.get('execModel');configured_provider=pm.get('execProviderId')
    if not configured_model or not configured_provider:
        info=qa.OPERATOR.stat()
        if info.st_uid!=os.getuid() or info.st_mode & 0o077:raise RuntimeError('Insecure QA operator file')
        operator=json.loads(qa.OPERATOR.read_text())
        admin=qa.call('POST','/auth/login','',operator)['accessToken']
        settings=qa.call('GET','/api/v1/settings',admin)['settings']
        configured_model=configured_model or settings.get('defaultExecModel')
        configured_provider=configured_provider or settings.get('defaultExecProviderId')
    if configured_model!=route['model'] or configured_provider!=route['provider']:
        raise RuntimeError('Effective QA PM route differs from verified Sol route; no fallback permitted')
    require_prompt(PRICE_ACCEPTANCE,'price_acceptance')
    if args.case not in ('case-01','case-06'):
        pilot=qa.OUT/'pilot-accepted.json'
        if not pilot.is_file() or json.loads(pilot.read_text()).get('accepted_cases') != ['case-01','case-06']:
            raise RuntimeError('Initial two-case pilot has not been accepted')
    slotdir=qa.OUT/'model-slots';slotdir.mkdir(mode=0o700,exist_ok=True)
    slot=None
    for n in range(2):
        fd=os.open(slotdir/str(n),os.O_CREAT|os.O_RDWR,0o600)
        try:
            fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);slot=fd;break
        except BlockingIOError:os.close(fd)
    if slot is None:raise RuntimeError('Two-request QA model budget is already in use')
    out=qa.OUT/args.case/'sol-chat';out.mkdir(mode=0o700,exist_ok=False)
    qa.save(out/'admission-plan.json',{'case':args.case,'origin':args.origin,'request_id':str(uuid.uuid4()),'model':route['model'],'provider':route['provider']})
    plan=json.loads((out/'admission-plan.json').read_text())
    conv=qa.call('POST','/api/v1/system/conversations',token,{'title':'Synthetic blind QA'})
    qa.save(out/'conversation.json',conv);cid=conv['conversation']['id']
    payload={'requestId':plan['request_id'],'message':PRICE_ACCEPTANCE,'workspace':{'appId':pm['id'],'path':'/'}}
    qa.save(out/'prompt-request.json',payload)
    # Admission is attempted once. If it fails, reconcile using request_id; never blindly resend.
    receipt=qa.call('POST',f'/api/v1/system/conversations/{cid}/prompt',token,payload)
    qa.save(out/'prompt-receipt.json',receipt);rid=receipt['runId'];deadline=time.monotonic()+300
    while time.monotonic()<deadline:
        run=qa.call('GET',f'/api/v1/runs/{rid}',token).get('run',{})
        status=str(run.get('status','')).lower()
        if status in ('complete','completed','success','error','failed','cancelled','canceled','timeout'):
            chat=qa.call('GET',f'/api/v1/conversations/{cid}/chat',token)
            qa.save(out/'terminal.json',{'run':run,'chat':chat,'case':args.case});print(args.case+' terminal evidence saved');return
        time.sleep(2)
    qa.save(out/'timeout.json',{'run_id':rid,'conversation_id':cid});raise RuntimeError('Run not terminal; do not retry or start another request until reconciled')

if __name__=='__main__':
    try:main()
    except (RuntimeError,OSError,ValueError,KeyError) as exc:
        print(str(exc),file=__import__('sys').stderr);raise SystemExit(1)
