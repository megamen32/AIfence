#!/usr/bin/env python3
"""Summarise prepared synthetic inputs and independently recorded local reads."""
import argparse
import collections
import json
import math
import os
from pathlib import Path

ROOT=Path(__file__).resolve().parents[3]
OUT=ROOT/'.runtime/blind-director/local-source-package'
CASES=[f'case-{i:02d}' for i in range(1,12)]


def category(source,metric,ref=None):
    if source=='metrika' and (ref or {}).get('source_id')=='synthetic-sales':return 'sales'
    if source in ('metrika','sales'):return 'metrika' if source=='metrika' else 'sales'
    if metric=='search_queries':return 'wordstat'
    if metric=='search_interest':return 'google_trends'
    if source=='market':return 'market'
    if source=='backlog':return 'backlog'
    return None


def main():
    p=argparse.ArgumentParser();p.add_argument('--out',default='source-matrix-qa-42082.json');args=p.parse_args()
    if Path(args.out).name!=args.out:raise RuntimeError('Output must be a private runtime basename')
    manifest=json.loads((OUT/'manifest.json').read_text());result={}
    for case in CASES:
        groups={k:{'input_rows':0,'units':set(),'dates':set(),'scope':set(),'receipts':[],'direct_rows':0} for k in ('metrika','sales','wordstat','google_trends','market','backlog')}
        base=OUT/case; receipts=base/'receipts-qa-42082'
        for i,doc in enumerate(manifest['cases'][case]['documents']):
            value=json.loads((base/doc['file']).read_text())
            for batch in value['batches']:
                for r in batch.get('observations',[]):
                    key=category(batch.get('source'),r.get('metric_key'),r.get('source_ref'))
                    if not key:continue
                    entry=groups[key];entry['input_rows']+=1;entry['units'].add(str(r.get('unit','')))
                    entry['scope'].add(str(r.get('product_key','')))
                    period=r.get('period') or {};entry['dates'].update(x for x in (period.get('from'),period.get('to')) if x)
            if receipts.is_dir():
                for path in sorted(receipts.glob(f'pm-{i:02d}-read-*.json')):
                    data=json.loads(path.read_text());query=data['query'];key=category(query.get('source'),query.get('metric_key'))
                    if not key:continue
                    output=data['receipt']['output'];rows=output.get('rows',[])
                    by_source=collections.defaultdict(list)
                    for row in rows:
                        row_key=category(query.get('source'),query.get('metric_key'),row.get('source_ref'))
                        if row_key:by_source[row_key].append(row)
                    for row_key, selected in by_source.items():
                        entry=groups[row_key];entry['direct_rows']+=len(selected)
                        entry['receipts'].append({'path':str(path.resolve()),'query_matched_total':output.get('total_matched'),
                            'returned_for_source':len(selected),'has_numeric_value':any(str(r.get('value','')).replace('.','',1).replace('-','',1).isdigit() for r in selected)})
        result[case]={}
        for key,v in groups.items():
            dates=sorted(v['dates']); result[case][key]={'input_rows':v['input_rows'],'units':sorted(v['units']),'date_from':dates[0] if dates else None,
                'date_to':dates[-1] if dates else None,'scope':sorted(v['scope']),'direct_rows':v['direct_rows'],'receipts':v['receipts'],
                'state':'direct_PM_read' if v['receipts'] else ('prepared_not_read' if v['input_rows'] else 'absent')}
        metrika_receipt=receipts/'metrika-qa-discover.json'
        if not metrika_receipt.is_file():metrika_receipt=receipts/'metrika-discover.json'
        result[case]['metrika']['native_status']='direct_native_discovery' if metrika_receipt.is_file() else ('blocked_or_not_loaded' if result[case]['metrika']['input_rows'] else 'absent')
        scanfile=receipts/'metrika-qa-scan.json'
        if metrika_receipt.is_file() and scanfile.is_file():
            discover=json.loads(metrika_receipt.read_text())['output'];scan=json.loads(scanfile.read_text())['output']
            candidates=scan.get('candidates') or []
            valid=[r for r in candidates if any(isinstance(v,(int,float)) and not isinstance(v,bool) and math.isfinite(v) for v in r.values()) and any(isinstance(ref,dict) and ref.get('locator') for ref in r.get('source_refs',[]))]
            result[case]['metrika']['native_scan']={'receipt':str(scanfile.resolve()),'dataset_matches_discovery':bool(discover.get('dataset_id')) and discover.get('dataset_id')==scan.get('dataset_id'),'numeric_candidates_with_locator':len(valid)}
        else:result[case]['metrika']['native_scan']=None
        for key,name in (('wordstat','read_wordstat'),('google_trends','read_google_trends'),('market','read_competitor_prices')):
            path=receipts/(name+'-discover.json')
            if path.is_file():
                data=json.loads(path.read_text()); rows=(data.get('output') or {}).get('observations') or []
                index=receipts/'discovery-list.json'
                visible={x.get('snapshot_id') for x in (json.loads(index.read_text()).get('output',{}).get('snapshots',[]) if index.is_file() else [])}
                result[case][key]['specialist_read']={'receipt':str(path.resolve()),'rows':len(rows),'numeric_with_locator':sum(isinstance(r.get('value'),(int,float)) and not isinstance(r.get('value'),bool) and bool((r.get('source_ref') or {}).get('locator')) for r in rows),'snapshot_in_owner_list':(data.get('output') or {}).get('snapshot_id') in visible,'error':data.get('error')}
            else:result[case][key]['specialist_read']=None
    target=OUT/args.out;fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as f:json.dump(result,f,ensure_ascii=False,indent=2);f.write('\n')
    print('Local source matrix saved:',target)

if __name__=='__main__':main()
