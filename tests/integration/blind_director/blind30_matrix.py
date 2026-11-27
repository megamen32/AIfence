#!/usr/bin/env python3
"""Summarize owner-scoped numeric direct-read receipts without oracle access."""
import json
import math
from pathlib import Path

import blind30_score

PACKAGE=Path(__file__).resolve().parents[3]/'.runtime/blind-director/blind30-sol-v2'


def main():
    _,manifest=blind30_score.verify(PACKAGE);cases={}
    for item in manifest['cases']:
        case=item['id'];folder=PACKAGE/'local-inputs'/case/'receipts-qa-42082';sources={}
        for path in folder.glob('pm-*-read-*.json'):
            data=json.loads(path.read_text());output=data['receipt']['output']
            for row in output.get('rows',[]):
                try:value=float(row.get('value'))
                except (ValueError,TypeError):continue
                ref=row.get('source_ref') or {};period=row.get('period') or {}
                if not math.isfinite(value) or not ref.get('locator') or not row.get('unit'):continue
                source=ref.get('source_id') or 'unknown'
                entry=sources.setdefault(source,{'rows_read':0,'units':set(),'dates':set(),'receipts':set()})
                entry['rows_read']+=1;entry['units'].add(row['unit']);entry['dates'].update(x for x in (period.get('from'),period.get('to')) if x);entry['receipts'].add(path.name)
        for entry in sources.values():
            for key in ('units','dates','receipts'):entry[key]=sorted(entry[key])
        owner=json.loads((folder/'owner.json').read_text())
        cases[case]={'owner_id':owner['user_id'],'scope':'owner-scoped PM reads','sources':sources}
    blind30_score.save(PACKAGE/'direct-matrix.json',{'cases':cases,'note':'Rows read, not input row totals; synthetic unless the receipt states otherwise'})
    print('Direct-read matrix:',len(cases),'cases')


if __name__=='__main__':main()
