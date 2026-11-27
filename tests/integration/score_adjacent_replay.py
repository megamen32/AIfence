#!/usr/bin/env python3
"""Score frozen threshold timing only; never score causal diagnosis as a hit."""
import collections
import datetime as dt
import hashlib
import json
import os
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
BASE=ROOT/'.runtime/blind-director/local-source-package/adjacent-replay'


def main():
    freeze=json.loads((BASE/'freeze.json').read_text())
    for name,expected in freeze['sha256'].items():
        if hashlib.sha256((BASE/name).read_bytes()).hexdigest()!=expected:raise RuntimeError('Frozen evidence changed: '+name)
    manifest=json.loads((BASE/'inputs.json').read_text()); groups=collections.defaultdict(list)
    with (BASE/'windows-frozen.jsonl').open() as f:
        for line in f:
            row=json.loads(line);groups[row['input']].append(row)
    rows=[]
    for entry in manifest['inputs']:
        path=Path(entry['path']); windows=groups.get(str(path),[]);vault=path.parents[2]/'vault'/path.parent.name
        answers=list(vault.glob('S*.json'))
        if len(answers)!=1:
            rows.append({'input':str(path),'outcome':'missing_oracle'});continue
        oracle=json.loads(answers[0].read_text());scenario=oracle.get('scenario',answers[0].stem)
        family=oracle.get('family');week=oracle.get('week')
        submitted=[x for x in windows if x.get('status')=='submitted']
        if scenario=='S0':
            outcome='false_alarm' if submitted else ('quiet' if windows else 'ineligible')
            rows.append({'input':str(path),'scenario':scenario,'window_count':len(windows),'outside_signals':len(submitted),'outcome':outcome});continue
        if not isinstance(week,int) or not family:
            rows.append({'input':str(path),'scenario':scenario,'window_count':len(windows),'outcome':'ineligible_no_annotated_family_week'});continue
        source=json.loads(path.read_text());dates=[r['period']['from'] for r in source.get('observations',[]) if (r.get('period') or {}).get('from')]
        if not dates:rows.append({'input':str(path),'scenario':scenario,'outcome':'ineligible_no_dates'});continue
        origin=dt.date.fromisoformat(min(dates));when=(origin+dt.timedelta(weeks=week)).isoformat()
        eligible=[x for x in windows if x['current_from']==when and x['product'].casefold()==family.casefold()]
        matched=[x for x in eligible if x['status']=='submitted']
        outside=[x for x in submitted if x['current_from']!=when or x['product'].casefold()!=family.casefold()]
        rows.append({'input':str(path),'scenario':scenario,'family':family,'event_week':when,'eligible_windows':len(eligible),'matched_signals':len(matched),'outside_signals':len(outside),
                     'outcome':('event_window_signal' if matched else 'no_event_window_signal') if eligible else 'ineligible_no_comparable_window'})
    positives=[r for r in rows if r['outcome'] in ('event_window_signal','no_event_window_signal')]
    negatives=[r for r in rows if r.get('scenario')=='S0' and r['outcome'] in ('false_alarm','quiet')]
    report={'schema':'sourcewatch-adjacent-timing-v1','frozen_evidence_sha256':freeze['sha256']['windows-frozen.jsonl'],
            'positive_numerator':sum(r['outcome']=='event_window_signal' for r in positives),'positive_denominator':len(positives),
            'negative_numerator':sum(r['outcome']=='false_alarm' for r in negatives),'negative_denominator':len(negatives),
            'meaning':'Threshold trigger in annotated family/week, any supported metric/segment; not identification of semantic cause. S0 false alarm means any trigger anywhere in full observed history.',
            'excluded_no_original_metrika':len(manifest['multisource_without_metrika']),'rows':rows}
    dst=ROOT/'.runtime/blind-director/local-source-package/sourcewatch-measured-score.json';fd=os.open(dst,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    with os.fdopen(fd,'w') as f:json.dump(report,f,ensure_ascii=False,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
    print('Temporal signal overlap:',report['positive_numerator'],'/',report['positive_denominator'],'S0 any-alert:',report['negative_numerator'],'/',report['negative_denominator'],'no original Metrika excluded:',report['excluded_no_original_metrika'])

if __name__=='__main__':main()
