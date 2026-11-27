#!/usr/bin/env python3
"""Freeze 30 unique synthetic input packages without opening any oracle."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import prompt_guard

ROOT=Path(__file__).resolve().parents[3]
PRIVATE=ROOT/'.runtime/blind-director'


def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()


def save(path,data):
    fd=os.open(path,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    with os.fdopen(fd,'w') as f:json.dump(data,f,ensure_ascii=False,sort_keys=True,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())


def main():
    p=argparse.ArgumentParser();p.add_argument('--runs',nargs=5,required=True);p.add_argument('--output',required=True);args=p.parse_args()
    runs=[Path(x).resolve() for x in args.runs]
    if len(set(runs))!=5 or any(r.parent!=PRIVATE.resolve() or not r.name.startswith('run-') for r in runs):raise RuntimeError('Expected five distinct private runs')
    target=Path(args.output).resolve()
    if target.parent!=PRIVATE.resolve():raise RuntimeError('Package must be in the private runtime')
    items=[];seen=set()
    for batch,run in enumerate(runs,1):
        manifest=json.loads((run/'run.json').read_text());cases=manifest['cases']
        if sorted(cases)!=[f'case-{i:02d}' for i in range(1,7)]:raise RuntimeError('Unexpected case inventory')
        for case in sorted(cases):
            folder=run/'inputs'/case; expected=manifest['input_sha256'][case]
            if {x.name for x in folder.iterdir()}!=set(expected):raise RuntimeError('Input file inventory changed')
            hashes={name:sha(folder/name) for name in sorted(expected)}
            if hashes!=expected:raise RuntimeError('Input integrity failed')
            sample=hashes['metrika.json']
            if sample in seen:raise RuntimeError('Duplicate generated input')
            seen.add(sample);items.append({'id':f'batch-{batch:02d}-{case}','run':str(run),'input_hashes':hashes})
    target.mkdir(mode=0o700,exist_ok=False)
    question=prompt_guard.PRICE_ACCEPTANCE
    if prompt_guard.validate_prompt(question,'price_acceptance'):raise RuntimeError('Prompt validation failed')
    save(target/'manifest.json',{'schema':'blind30-v1','question_sha256':hashlib.sha256(question.encode()).hexdigest(),
        'question':question,'cases':items,'scoring_contract_sha256':sha(Path(__file__).with_name('blind_scoring.py')),
        'normalizer_sha256':sha(Path(__file__).with_name('harness.py')),
        'preparer_sha256':sha(Path(__file__)),'scorer_wrapper_sha256':sha(Path(__file__).with_name('blind30_score.py')),'prompt_guard_sha256':sha(Path(__file__).with_name('prompt_guard.py')),
        'scorer_snapshots':{str(run):sha(run/'tools/scorer.py') for run in runs}})
    print('Frozen',len(items),'unique cases; input manifest only, no oracle opened')

if __name__=='__main__':main()
