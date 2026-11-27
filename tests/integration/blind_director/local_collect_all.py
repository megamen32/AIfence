#!/usr/bin/env python3
"""Collect every eligible watchdog case and import unchanged under its QA owner."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path

HERE=Path(__file__).resolve().parent
s=importlib.util.spec_from_file_location('direct',HERE/'local_direct.py');d=importlib.util.module_from_spec(s);s.loader.exec_module(d)


def main():
    p=argparse.ArgumentParser();p.add_argument('--origin',required=True);p.add_argument('--case',required=True);p.add_argument('--blind30',action='store_true');p.add_argument('--resume-scan',action='store_true');args=p.parse_args()
    if args.blind30:
        import blind30_score
        package=d.q.ROOT/'.runtime/blind-director/blind30-sol-v2';_, frozen=blind30_score.verify(package)
        d.q.OUT=package/'local-inputs';allowed={x['id'] for x in frozen['cases']}
    else:allowed=set(d.sources.CASES)
    if args.case not in allowed:raise RuntimeError('Unknown case')
    d.assert_local(args.origin);case=args.case
    user=json.loads((d.q.OUT/'users-qa-42082'/f'{case}-final-credentials.json').read_text())
    if user.get('origin')!=args.origin or user.get('case')!=case:raise RuntimeError('User origin mismatch')
    token=d.q.call('POST','/auth/login','',{'email':user['email'],'password':user['password']})['accessToken']
    if d.q.call('GET','/api/v1/me',token).get('user',{}).get('id')!=user['user_id']:raise RuntimeError('Owner mismatch')
    apps={x['slug']:x['id'] for x in d.q.call('GET','/api/v1/agents',token)['agents']}
    if set(apps)!=d.q.REQUIRED:raise RuntimeError('Unexpected application grants')
    out=d.q.OUT/case/'collect-all-9f'
    if args.resume_scan:
        if not out.is_dir() or list(out.glob('import-*-start.json')) or (out/'candidates.json').exists():raise RuntimeError('Cannot safely resume collection')
    else:
        out.mkdir(mode=0o700,exist_ok=False)
        d.write(out/'rule.json',{'rule':'all paginated controlled_decline candidates with an explicit control; no oracle selection','source':'scan_watchdog_series','limit':12,'owner_id':user['user_id']})
    dataset=None;all_items=[];offset=0;page=0;total=None
    while True:
        path=out/f'scan-{page:03d}.json'
        if args.resume_scan and path.exists():result=json.loads(path.read_text())
        else:
            result=d.invoke(token,apps['metrika-analyst'],'scan_watchdog_series',{'offset':offset,'limit':12});d.write(path,result)
        scan=result['output'];current=scan.get('dataset_id');count=scan.get('candidate_count')
        if not current or scan.get('status') not in ('ok','alert','partial','measurement_issue','campaign_effect','temporary_spike','quiet'):raise RuntimeError('No usable active dataset')
        if dataset is None:dataset=current;total=count
        if current!=dataset or count!=total:raise RuntimeError('Scan changed while paging')
        all_items.extend(scan.get('candidates') or []);next_offset=scan.get('next_offset')
        if next_offset is None:break
        if not isinstance(next_offset,int) or next_offset<=offset or page>=99:raise RuntimeError('Invalid scan pagination')
        offset=next_offset;page+=1
    ids=[x.get('candidate_id') for x in all_items]
    if any(not x for x in ids) or len(set(ids))!=len(ids) or len(all_items)!=total:raise RuntimeError('Scan is incomplete or has duplicate candidate IDs')
    eligible=[x for x in all_items if x.get('kind')=='controlled_decline' and x.get('control_product_key')]
    excluded=[{'candidate_id':x.get('candidate_id'),'kind':x.get('kind'),'reason':'not supported by collect_selected_watchdog_case'} for x in all_items if x not in eligible]
    d.write(out/'candidates.json',{'dataset_id':dataset,'total':total,'eligible':[x['candidate_id'] for x in eligible],'excluded':excluded})
    imports=[]
    for i,candidate in enumerate(eligible):
        args={'dataset_id':dataset,'candidate_id':candidate['candidate_id']}
        collected=d.invoke(token,apps['metrika-analyst'],'collect_selected_watchdog_case',args);d.write(out/f'case-{i:03d}.json',{'input':args,'receipt':collected})
        document=(collected['output'] or {}).get('document')
        if not isinstance(document,dict) or document.get('schema_version')!='aifence.case.v1':raise RuntimeError('Collected document is unavailable')
        checksum=hashlib.sha256(json.dumps(document,ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()).hexdigest()
        d.write(out/f'import-{i:03d}-start.json',{'candidate_id':candidate['candidate_id'],'document_sha256':checksum})
        receipt=d.invoke(token,apps['product-manager'],'import_evidence',{'document':document});d.write(out/f'import-{i:03d}-receipt.json',receipt)
        snapshot=(receipt['output'] or {}).get('snapshot_id')
        if not snapshot:raise RuntimeError('Import receipt has no snapshot ID')
        imports.append(snapshot)
    index=d.invoke(token,apps['product-manager'],'list_evidence',{});d.write(out/'pm-list.json',index)
    available={x.get('snapshot_id') for x in index['output'].get('snapshots',[])}
    if not set(imports)<=available:raise RuntimeError('Some imported documents are absent from owner index')
    review=d.invoke(token,apps['product-manager'],'review_sales_and_prices',{'summary':True,'director':True});d.write(out/'review.json',review)
    print(case,'scan candidates',len(all_items),'eligible',len(eligible),'imported',len(imports),'review recorded',flush=True)

if __name__=='__main__':
    try:main()
    except (RuntimeError,OSError,ValueError,KeyError) as exc:print(str(exc),file=__import__('sys').stderr);raise SystemExit(1)
