#!/usr/bin/env python3
"""Prepare fixed neutral control and hashed replay inputs for the unopened blind30 package."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path

ROOT=Path(__file__).resolve().parents[3]
PACKAGE=ROOT/'.runtime/blind-director/blind30-sol-v2'


def save(path,data):
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as f:json.dump(data,f,ensure_ascii=False,sort_keys=True,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())


def main():
    manifest=json.loads((PACKAGE/'manifest.json').read_text());out=PACKAGE/'offline';out.mkdir(mode=0o700,exist_ok=False)
    paths=[];pairs=set()
    for item in manifest['cases']:
        p=Path(item['run'])/'inputs'/item['id'][-7:]/'metrika.json';raw=p.read_bytes();digest=hashlib.sha256(raw).hexdigest()
        if digest!=item['input_hashes']['metrika.json']:raise RuntimeError('Input hash mismatch')
        doc=json.loads(raw);paths.append({'path':str(p),'sha256':digest,'as_of':doc['as_of']})
        for row in doc.get('observations',[]):
            key=row.get('product_key');period=row.get('period') or {};start=period.get('from');end=period.get('to')
            try:a=dt.date.fromisoformat(start);b=dt.date.fromisoformat(end)
            except (ValueError,TypeError):continue
            if key and a.weekday()==0 and b==a+dt.timedelta(days=6) and b.isoformat()<=doc['as_of'][:10]:pairs.add((key,start,end))
    rows=[{'id':f'neutral-trends/{product}/{start}','kind':'metric','product_key':product,'metric_key':'search_interest','value':50,'unit':'index','period':{'from':start,'to':end},'status':'observed','source_ref':{'source_id':'google_trends','locator':f'synthetic-neutral/trends/{product}/{start}','revision':'neutral-v1'},'details':{'synthetic':True,'origin':'synthetic_fixture','geo':'RU'}} for product,start,end in sorted(pairs)]
    save(out/'neutral-trends.json',{'schema_version':'aifence.v1','source':'research','as_of':'2027-01-04T00:00:00Z','status':'ok','observations':rows,'warnings':['synthetic neutral control']})
    sha=hashlib.sha256((out/'neutral-trends.json').read_bytes()).hexdigest()
    save(out/'inputs.json',{'inputs':paths,'multisource_without_metrika':[],'neutral_sha256':sha,'rule':'all-adjacent-full-weeks-v1','threshold_pct':20,'metrics':['visits','orders','revenue'],'control_origin':'synthetic_fixture'})
    print('Prepared',len(paths),'unopened blind inputs for frozen offline replay')

if __name__=='__main__':main()
