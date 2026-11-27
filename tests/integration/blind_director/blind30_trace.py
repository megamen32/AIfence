#!/usr/bin/env python3
"""Pair actual tool calls and results in frozen local conversations."""
import json
import math
import re
from pathlib import Path

import blind30_score

PACKAGE = Path(__file__).resolve().parents[3]/'.runtime/blind-director/blind30-sol-v2'


def unpack(value):
    if isinstance(value, str):
        try:return unpack(json.loads(value))
        except (ValueError, RecursionError):return value
    if isinstance(value, list):return [unpack(v) for v in value]
    if isinstance(value, dict):return {k:unpack(v) for k,v in value.items()}
    return value


def numbers_and_locators(value):
    if isinstance(value, list):
        return [row for x in value for row in numbers_and_locators(x)]
    if not isinstance(value, dict):return []
    loc = value.get('source_ref') or {}
    n = value.get('value')
    if isinstance(n,str):
        try:n=float(n)
        except ValueError:n=None
    found = []
    if isinstance(loc, dict) and loc.get('locator') and isinstance(n, (int,float)) and not isinstance(n,bool) and math.isfinite(n):
        found.append({'source':loc.get('source_id'), 'locator':loc['locator'], 'value':n, 'unit':value.get('unit')})
    magnitude=value.get('величина_%')
    if isinstance(magnitude,(int,float)) and not isinstance(magnitude,bool) and math.isfinite(magnitude):
        for ref in value.get('доказательства') or []:
            if isinstance(ref,dict) and ref.get('локатор'):
                found.append({'source':ref.get('источник'),'locator':ref['локатор'],'value':magnitude,'unit':'percent'})
    for x in value.values():found.extend(numbers_and_locators(x))
    return found


def failed(value):
    if isinstance(value,list):return any(failed(x) for x in value)
    if not isinstance(value,dict):return False
    if value.get('isError') is True or value.get('is_error') is True or value.get('type') in ('error','error-text'):return True
    if str(value.get('status','')).lower() in ('error','failed'):return True
    return any(failed(x) for x in value.values())


def trace(terminal):
    calls, results = {}, {}
    for msg in terminal['chat']['messages']:
        try:parts = json.loads(msg.get('parts') or '[]')
        except (TypeError, ValueError):continue
        for part in parts if isinstance(parts,list) else []:
            callid=part.get('toolCallId')
            if not callid:continue
            if part.get('type')=='tool-call':calls[callid]=part
            elif part.get('type')=='tool-result':results[callid]=part
    rows=[]
    for callid, call in calls.items():
        result=results.get(callid); args=call.get('args') or {};code=args.get('code','')
        output=unpack(result.get('output')) if result else None
        observed=[] if failed(output) else numbers_and_locators(output)
        names=re.findall(r'apps\.([a-z_]+)\.tools\.([a-z_]+)',code)
        rows.append({'tool_call_id':callid,'operations':[f'{a}.{b}' for a,b in names],
                     'result_received':result is not None,'result_failed':failed(output),'observations':observed,
                     'raw_result':output if any(b=='submit_answer' for _,b in names) else None})
    final=[m.get('content','') for m in terminal['chat']['messages'] if m.get('role')=='assistant' and m.get('content')]
    return {'calls':rows,'final':final[-1] if final else ''}


def main():
    _, manifest=blind30_score.verify(PACKAGE)
    directory=PACKAGE/'traces-v4';directory.mkdir(mode=0o700,exist_ok=True)
    for item in manifest['cases']:
        case=item['id'];record=PACKAGE/'records'/f'{case}.json';out=directory/f'{case}.json'
        if not record.is_file() or out.exists():continue
        raw=json.loads(record.read_text())['record'];path=Path(raw['source_receipts']['terminal_path'])
        if blind30_score.sha(path)!=raw['source_receipts']['terminal_sha256']:raise RuntimeError('Terminal hash changed')
        blind30_score.save(out,trace(json.loads(path.read_text())))
    print('Frozen conversation traces:',len(list(directory.glob('*.json'))))


if __name__=='__main__':main()
