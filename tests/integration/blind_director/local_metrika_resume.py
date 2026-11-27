#!/usr/bin/env python3
"""Reconcile a rejected local watchdog import and save owner-scoped receipts."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('direct',HERE/'local_direct.py');d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)


def main():
    p=argparse.ArgumentParser();p.add_argument('--origin',required=True);p.add_argument('--case',required=True,choices=d.sources.CASES);args=p.parse_args()
    d.assert_local(args.origin)
    out=d.q.OUT/args.case/'receipts-qa-42082'; user=json.loads((d.q.OUT/'users-qa-42082'/f'{args.case}-final-credentials.json').read_text())
    if user.get('origin')!=args.origin:raise RuntimeError('User origin mismatch')
    token=d.q.call('POST','/auth/login','',{'email':user['email'],'password':user['password']})['accessToken']
    me=d.q.call('GET','/api/v1/me',token)
    if me.get('user',{}).get('id')!=user['user_id']:raise RuntimeError('User identity mismatch')
    apps={x['slug']:x['id'] for x in d.q.call('GET','/api/v1/agents',token)['agents']}
    if set(apps)!=d.q.REQUIRED:raise RuntimeError('Unexpected user grants')
    met=apps['metrika-analyst'];series=(d.q.OUT/args.case/'metrika-series.json').read_bytes();context=(d.q.OUT/args.case/'metrika-context.json').read_bytes()
    if (out/'metrika-upload.json').exists() or (out/'metrika-qa-retry-plan.json').exists():raise RuntimeError('Earlier upload needs reconciliation; do not retry')
    evidence={'case':args.case,'series_sha256':hashlib.sha256(series).hexdigest(),'context_sha256':hashlib.sha256(context).hexdigest()}
    d.write(out/'metrika-qa-retry-plan.json',evidence)
    route=f'/api/v1/agents/{met}/deployments/production/http/watchdog';payload,ctype=d.sources.adapter().multipart(series,context)
    uploaded=d.upload(token,route,payload,ctype);d.write(out/'metrika-qa-import.json',uploaded)
    if uploaded.get('status')!='stored':raise RuntimeError('Upload unconfirmed')
    selected=d.upload(token,route,b'action=select_blind','application/x-www-form-urlencoded');d.write(out/'metrika-qa-select.json',selected)
    if selected.get('status')!='selected':raise RuntimeError('Selection unconfirmed')
    discovery=d.invoke(token,met,'discover_watchdog_series',{});d.write(out/'metrika-qa-discover.json',discovery)
    found=discovery.get('output') or {};dataset=found.get('dataset_id')
    if found.get('status')!='ok' or not dataset:raise RuntimeError('Discovery unconfirmed')
    scan=d.invoke(token,met,'scan_watchdog_series',{});d.write(out/'metrika-qa-scan.json',scan)
    if (scan.get('output') or {}).get('dataset_id')!=dataset:raise RuntimeError('Scan selected a different dataset')
    print(args.case+' upload/select/discover and scan receipts saved')

if __name__=='__main__':
    try:main()
    except (RuntimeError,OSError,KeyError,ValueError) as exc:
        print(str(exc),file=__import__('sys').stderr);raise SystemExit(1)
