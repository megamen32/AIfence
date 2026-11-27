#!/usr/bin/env python3
"""Owner-scoped local imports and direct reads. This file never sends prompts."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import urllib.error
import urllib.request

HERE=Path(__file__).resolve().parent

def module(name,path):
    s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
q=module('qa_users',HERE/'local_qa_users.py')
sources=module('local_sources',HERE/'local_sources.py')


def assert_local(origin):
    q.assert_local(origin)

def write(path,value):
    q.save(path,value)


def invoke(token, app, name, payload):
    raw=q.call('POST',f'/api/v1/agents/{app}/deployments/production/tools/{name}/invoke',token,{'input':payload})
    decoded=json.loads(raw['resultJson'])
    if decoded.get('isError') or decoded.get('is_error'):raise RuntimeError('Tool returned error: '+name)
    value=decoded.get('output',decoded)
    if isinstance(value,str):value=json.loads(value)
    return {'transport':raw,'output':value}


def upload(token,url,data,content_type):
    req=urllib.request.Request(q.ORIGIN+url,data=data,headers={'Authorization':'Bearer '+token,'Content-Type':content_type,'Origin': ('http://metrika-analyst.localhost:42082' if url.endswith('/watchdog') else 'http://product-manager.localhost:42082')},method='POST')
    try:
        with q.OPENER.open(req,timeout=30) as resp:
            payload=resp.read(); return json.loads(payload) if payload else {}
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f'Local upload {url}: HTTP {exc.code}') from None


def main():
    p=argparse.ArgumentParser();p.add_argument('--origin',required=True);p.add_argument('--case',required=True);p.add_argument('--blind30',action='store_true');p.add_argument('--resume-rejected',action='store_true');p.add_argument('--skip-metrika',action='store_true');args=p.parse_args()
    if args.blind30:
        import blind30_score
        package=q.ROOT/'.runtime/blind-director/blind30-sol-v2';_, frozen=blind30_score.verify(package)
        q.OUT=package/'local-inputs';allowed={x['id'] for x in frozen['cases']}
    else:allowed=set(sources.CASES)
    if args.case not in allowed:raise RuntimeError('Unknown case')
    assert_local(args.origin)
    case=args.case; user=json.loads((q.OUT/'users-qa-42082'/f'{case}-final-credentials.json').read_text())
    if user['origin']!=args.origin or user['case']!=case:raise RuntimeError('User provenance mismatch')
    auth=q.call('POST','/auth/login','',{'email':user['email'],'password':user['password']});token=auth['accessToken']
    me=q.call('GET','/api/v1/me',token)
    if me.get('user',{}).get('id')!=user['user_id'] or me.get('fullAdministrator') or me.get('full_administrator'):
        raise RuntimeError('Local QA identity mismatch')
    agents=q.call('GET','/api/v1/agents',token)['agents'];apps={a['slug']:a['id'] for a in agents}
    if set(apps)!=q.REQUIRED:raise RuntimeError('Unexpected accessible apps')
    out=q.OUT/case/'receipts-qa-42082'
    if out.exists() and not args.resume_rejected:raise RuntimeError('Existing attempt requires reconciliation')
    out.mkdir(mode=0o700,exist_ok=args.resume_rejected)
    owner={'case':case,'origin':args.origin,'user_id':user['user_id'],'apps':apps,'user_role':'user'}
    if (out/'owner.json').exists():
        if json.loads((out/'owner.json').read_text()) != owner:raise RuntimeError('Existing owner receipt mismatch')
    else:write(out/'owner.json',owner)
    manifest=json.loads((q.OUT/'manifest.json').read_text())['cases'][case]
    for doc in manifest['documents']:
        data=(q.OUT/case/doc['file']).read_bytes()
        if hashlib.sha256(data).hexdigest()!=doc['sha256']:raise RuntimeError('Prepared input changed')
    pm=apps['product-manager']; met=apps['metrika-analyst']
    index=json.loads((out/'pm-before.json').read_text()) if (out/'pm-before.json').exists() else invoke(token,pm,'list_evidence',{})
    if not (out/'pm-before.json').exists():write(out/'pm-before.json',index)
    if index['output'].get('snapshots')!=[]:raise RuntimeError('Local QA PM cabinet is not empty')
    before=json.loads((out/'tasks-before.json').read_text()) if (out/'tasks-before.json').exists() else invoke(token,pm,'list_hypotheses',{})
    if not (out/'tasks-before.json').exists():write(out/'tasks-before.json',before)
    if before['output'].get('records')!=[]:raise RuntimeError('Local QA cabinet has cards')
    series_path=q.OUT/case/'metrika-series.json'
    if series_path.exists() and args.skip_metrika:
        write(out/'metrika-blocked.json',{'status':'not_attempted_after_case01_404','source_not_loaded':True})
    elif series_path.exists() and args.resume_rejected and (out/'metrika-upload-rejected-404.json').exists():
        if (out/'metrika-upload.json').exists():raise RuntimeError('Unexpected successful prior import')
        write(out/'metrika-blocked.json',{'status':'rejected_404','source_not_loaded':True})
    elif series_path.exists():
        adapter=sources.adapter();series=series_path.read_bytes();context=(q.OUT/case/'metrika-context.json').read_bytes();body,ctype=adapter.multipart(series,context)
        route=f'/api/v1/agents/{met}/deployments/production/http/watchdog'
        start={'series_sha256':hashlib.sha256(series).hexdigest(),'context_sha256':hashlib.sha256(context).hexdigest()}
        if (out/'metrika-upload-start.json').exists():
            if not args.resume_rejected or not (out/'metrika-upload-rejected.json').exists() or (out/'metrika-upload.json').exists() or json.loads((out/'metrika-upload-start.json').read_text())!=start:raise RuntimeError('Unreconciled Metrika import; do not retry')
        else:write(out/'metrika-upload-start.json',start)
        x=upload(token,route,body,ctype);write(out/'metrika-upload.json',x)
        if x.get('status')!='stored':raise RuntimeError('Metrika upload unconfirmed')
        x=upload(token,route,b'action=select_blind','application/x-www-form-urlencoded');write(out/'metrika-select.json',x)
        if x.get('status')!='selected':raise RuntimeError('Metrika selection unconfirmed')
        read=invoke(token,met,'discover_watchdog_series',{});write(out/'metrika-discover.json',read)
        if read['output'].get('status')!='ok' or not read['output'].get('dataset_id'):raise RuntimeError('Metrika active dataset unconfirmed')
    else:
        write(out/'metrika-absent.json',{'status':'absent_in_input','not_simulated':True})
    received=[]
    for i,doc in enumerate(manifest['documents']):
        data=(q.OUT/case/doc['file']).read_bytes();write(out/f'pm-{i:02d}-start.json',{'sha256':doc['sha256']})
        route=f'/api/v1/agents/{pm}/deployments/production/http/import'
        row=upload(token,route,data,'application/json');write(out/f'pm-{i:02d}-receipt.json',row)
        if not row.get('snapshot_id'):raise RuntimeError('PM import receipt missing')
        received.append(row['snapshot_id'])
    ix=invoke(token,pm,'list_evidence',{});write(out/'pm-index.json',ix)
    if {r.get('snapshot_id') for r in ix['output'].get('snapshots',[])}!=set(received):raise RuntimeError('PM index does not match import receipts')
    for i,(doc,snapshot) in enumerate(zip(manifest['documents'],received)):
        for j,spec in enumerate(doc['counts']):
            params={'snapshot_id':snapshot,'source':spec['source'],'metric_key':spec['metric'],'limit':5}
            read=invoke(token,pm,'read_evidence',params);write(out/f'pm-{i:02d}-read-{j:02d}.json',{'query':params,'receipt':read})
    discovery=invoke(token,pm,'list_evidence',{});write(out/'discovery-list.json',discovery)
    visible={r.get('snapshot_id') for r in discovery['output'].get('snapshots',[])}
    neutral_id=received[-1]
    for name,product in [('read_wordstat',sources.PRODUCT),('read_google_trends',sources.PRODUCT),('read_competitor_prices',sources.SKU)]:
        try:read=invoke(token,pm,name,{'snapshot_id':neutral_id,'product_key':product,'limit':20})
        except RuntimeError as e:read={'error':str(e)}
        write(out/(name+'.json'),read)
        try:
            found=invoke(token,pm,name,{'product_key':product,'limit':20})
            value=found.get('output') or {}; rows=value.get('observations') or []
            if value.get('snapshot_id') not in visible or not any(isinstance(r.get('value'),(int,float)) and not isinstance(r.get('value'),bool) and (r.get('source_ref') or {}).get('locator') for r in rows):
                found['discovery_validation']='failed'
            else:found['discovery_validation']='passed'
        except RuntimeError as e:found={'error':str(e),'discovery_validation':'failed'}
        write(out/(name+'-discover.json'),found)
    print(case+' local direct imports and readbacks recorded',flush=True)

if __name__=='__main__':
    try:main()
    except Exception as exc:
        print('Local direct load stopped: '+type(exc).__name__+((': '+str(exc)) if isinstance(exc,RuntimeError) else ''),file=__import__('sys').stderr)
        raise SystemExit(1)
