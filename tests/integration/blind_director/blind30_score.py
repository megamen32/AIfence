#!/usr/bin/env python3
"""Create-only blind records, then fixed-parser scoring after all 30 are frozen."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
from blind_scoring import assess

ROOT=Path(__file__).resolve().parents[3]
HERE=Path(__file__).resolve().parent
PRIVATE=ROOT/'.runtime/blind-director'


def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def encoded(data):return (json.dumps(data,ensure_ascii=False,sort_keys=True,indent=2)+'\n').encode()
def save(path,data):
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'wb') as f:f.write(encoded(data));f.flush();os.fsync(f.fileno())


def verify(package):
    package=Path(package).resolve()
    if package.parent!=PRIVATE.resolve():raise RuntimeError('Expected package in private runtime')
    manifest=json.loads((package/'manifest.json').read_text())
    if len(manifest['cases'])!=30 or len({x['id'] for x in manifest['cases']})!=30:raise RuntimeError('Expected 30 unique cases')
    if hashlib.sha256(manifest['question'].encode()).hexdigest()!=manifest['question_sha256']:raise RuntimeError('Question changed')
    if sha(HERE/'blind_scoring.py')!=manifest['scoring_contract_sha256'] or sha(HERE/'harness.py')!=manifest['normalizer_sha256']:raise RuntimeError('Scoring contract changed')
    if sha(HERE/'blind30_prepare.py')!=manifest['preparer_sha256'] or sha(HERE/'blind30_score.py')!=manifest['scorer_wrapper_sha256'] or sha(HERE/'prompt_guard.py')!=manifest['prompt_guard_sha256']:raise RuntimeError('Package or prompt code changed')
    for run,digest in manifest['scorer_snapshots'].items():
        if sha(Path(run)/'tools/scorer.py')!=digest:raise RuntimeError('Pinned parser changed')
    for item in manifest['cases']:
        folder=Path(item['run'])/'inputs'/item['id'][-7:]
        if {x.name for x in folder.iterdir()}!=set(item['input_hashes']):raise RuntimeError('Input inventory changed')
        if any(sha(folder/name)!=value for name,value in item['input_hashes'].items()):raise RuntimeError('Input hash changed')
    return package,manifest


def freeze(package,case,record):
    package,manifest=verify(package);matches=[x for x in manifest['cases'] if x['id']==case]
    if len(matches)!=1:raise RuntimeError('Unknown case')
    raw=json.loads(Path(record).read_text())
    required={'case','question','chat_id','run_id','owner_id','status','response','duration_seconds','source_receipts'}
    if not isinstance(raw,dict) or not required<=raw.keys() or raw['case']!=case or raw['question']!=manifest['question'] or 'normalized' in raw:raise RuntimeError('Invalid raw record')
    if raw['status'] not in ('completed','failed','timeout','unknown') or not isinstance(raw['response'],str):raise RuntimeError('Invalid response state')
    if any(not isinstance(raw[k],str) or not raw[k] for k in ('chat_id','run_id','owner_id')):raise RuntimeError('Missing owner, chat or run identity')
    if not isinstance(raw['duration_seconds'],(int,float)) or raw['duration_seconds']<0 or not isinstance(raw['source_receipts'],dict):raise RuntimeError('Invalid duration or source receipts')
    directory=package/'records';directory.mkdir(mode=0o700,exist_ok=True)
    save(directory/(case+'.json'),{'sha256':hashlib.sha256(encoded(raw)).hexdigest(),'record':raw})


def score(package):
    package,manifest=verify(package);records=[]
    for item in manifest['cases']:
        path=package/'records'/(item['id']+'.json')
        if not path.is_file():raise RuntimeError('All 30 records must be frozen before scoring')
        envelope=json.loads(path.read_text());raw=envelope['record']
        if hashlib.sha256(encoded(raw)).hexdigest()!=envelope['sha256'] or raw['case']!=item['id'] or raw['question']!=manifest['question']:raise RuntimeError('Frozen record mismatch')
        records.append((item,raw))
    for key in ('chat_id','run_id','owner_id'):
        if len({r[key] for _,r in records})!=30:raise RuntimeError('All 30 '+key+' values must be unique')
    spec=importlib.util.spec_from_file_location('blind_harness',HERE/'harness.py');harness=importlib.util.module_from_spec(spec);spec.loader.exec_module(harness)
    cards={}
    for item,raw in records:
        run=Path(item['run']);case=item['id'][-7:]
        spec=importlib.util.spec_from_file_location('fixed_scorer',run/'tools/scorer.py');scorer=importlib.util.module_from_spec(spec);spec.loader.exec_module(scorer)
        rows=json.loads((run/'inputs'/case/'metrika.json').read_text()).get('observations',[]);ids={str(x.get('id','')) for x in rows}
        cards[item['id']]=harness.normalize_response(scorer,raw['response'],raw['status'],ids)
    # Normalisation is frozen before opening any expected answer.
    save(package/'normalized.json',cards)
    rows=[]
    for item,raw in records:
        run=Path(item['run']);case=item['id'][-7:];files=list((run/'vault'/case).glob('S*.json'))
        if len(files)!=1:raise RuntimeError('Expected one oracle after all freezes')
        oracle=json.loads(files[0].read_text());card=cards[item['id']]
        outcome=assess(oracle,{'status':raw['status'],'response':raw['response'],**card})
        rows.append({'case':item['id'],'scenario':oracle.get('scenario'),'outcome':outcome,'chat_id':raw['chat_id']})
    counts={name:sum(x['outcome']==name for x in rows) for name in ('found','missed','false_alarm','wrong_cause','correct_no_alert','correct_insufficient_data','unscorable')}
    save(package/'report.json',{'rows':rows,'counts':counts,'note':'Unscorable is not a pass; source and causality checks remain separate.'})
    print('Private 30-case report saved')


def main():
    p=argparse.ArgumentParser();sub=p.add_subparsers(dest='cmd',required=True)
    f=sub.add_parser('freeze');f.add_argument('--package',required=True);f.add_argument('--case',required=True);f.add_argument('--record',required=True)
    s=sub.add_parser('score');s.add_argument('--package',required=True);args=p.parse_args()
    if args.cmd=='freeze':freeze(args.package,args.case,args.record);print('Frozen',args.case)
    else:score(args.package)

if __name__=='__main__':
    try:main()
    except (RuntimeError,OSError,ValueError,KeyError) as exc:print(str(exc),file=sys.stderr);raise SystemExit(1)
