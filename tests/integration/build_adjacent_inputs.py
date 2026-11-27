#!/usr/bin/env python3
"""Freeze input hashes and neutral synthetic trends; never open answer files."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
TARGET=ROOT/'.runtime/blind-director/local-source-package/adjacent-replay'
HIST=Path('/Users/user/AIfence/.runtime/blind-director')
CURRENT=ROOT/'.runtime/blind-director/run-2291b25d10ad531be967bf03'
MULTI=Path('/Users/user/AIfence/.runtime/kes-handoff/multisource-fixed-question-20261127/inputs')


def write(path,data):
    fd=os.open(path,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    with os.fdopen(fd,'w') as f:
        json.dump(data,f,ensure_ascii=False,sort_keys=True,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())


def main():
    TARGET.mkdir(mode=0o700,exist_ok=False)
    paths=sorted(HIST.glob('run-*/inputs/case-*/metrika.json'))+sorted((CURRENT/'inputs').glob('case-*/metrika.json'))
    inputs=[];pairs=set()
    for path in paths:
        raw=path.read_bytes();doc=json.loads(raw);inputs.append({'path':str(path),'sha256':hashlib.sha256(raw).hexdigest(),'as_of':doc['as_of']})
        for row in doc.get('observations',[]):
            key=row.get('product_key');period=row.get('period') or {};start=period.get('from');end=period.get('to')
            try:a=dt.date.fromisoformat(start);b=dt.date.fromisoformat(end)
            except (ValueError,TypeError):continue
            if key and a.weekday()==0 and b==a+dt.timedelta(days=6) and b.isoformat()<=doc['as_of'][:10]:pairs.add((key,start,end))
    neutral=[]
    for product,start,end in sorted(pairs):
        neutral.append({'id':f'neutral-trends/{product}/{start}','kind':'metric','product_key':product,'metric_key':'search_interest','value':50,'unit':'index','period':{'from':start,'to':end},'status':'observed','source_ref':{'source_id':'google_trends','locator':f'synthetic-neutral/trends/{product}/{start}','revision':'neutral-v1'},'details':{'synthetic':True,'origin':'synthetic_fixture','geo':'RU'}})
    absent=[{'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'status':'no_metrika_in_original'} for p in sorted(MULTI.glob('case-*/snapshot.json'))]
    neutral_doc={'schema_version':'aifence.v1','source':'research','as_of':'2027-01-04T00:00:00Z','status':'ok','observations':neutral,'warnings':['synthetic neutral control']}
    write(TARGET/'neutral-trends.json',neutral_doc)
    neutral_hash=hashlib.sha256((TARGET/'neutral-trends.json').read_bytes()).hexdigest()
    write(TARGET/'inputs.json',{'inputs':inputs,'multisource_without_metrika':absent,'neutral_sha256':neutral_hash,'rule':'all-adjacent-full-weeks-v1','threshold_pct':20,'metrics':['visits','orders','revenue'],'control_origin':'synthetic_fixture'})
    print('Frozen input manifests:',len(inputs),'without original Metrika:',len(absent),'neutral rows:',len(neutral))

if __name__=='__main__':main()
