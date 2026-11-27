#!/usr/bin/env python3
"""Read each completed owner's cards after the chat, without model calls."""
import json
from pathlib import Path

import blind30_score
import local_direct as d

PACKAGE=Path(__file__).resolve().parents[3]/'.runtime/blind-director/blind30-sol-v2'


def main():
    d.assert_local('http://localhost:42082')
    _,manifest=blind30_score.verify(PACKAGE)
    for item in manifest['cases']:
        case=item['id'];base=PACKAGE/'local-inputs';out=base/case/'sol-chat'/'tasks-after.json'
        if not (PACKAGE/'records'/f'{case}.json').is_file() or out.exists():continue
        user=json.loads((base/'users-qa-42082'/f'{case}-final-credentials.json').read_text())
        token=d.q.call('POST','/auth/login','',{'email':user['email'],'password':user['password']})['accessToken']
        if d.q.call('GET','/api/v1/me',token).get('user',{}).get('id')!=user['user_id']:raise RuntimeError('Owner mismatch')
        apps={x['slug']:x['id'] for x in d.q.call('GET','/api/v1/agents',token)['agents']}
        if set(apps)!=d.q.REQUIRED:raise RuntimeError('Unexpected app grants')
        receipt=d.invoke(token,apps['product-manager'],'list_hypotheses',{})
        d.write(out,receipt)
        records=(receipt.get('output') or {}).get('records')
        print(case,'cards after chat:',len(records) if isinstance(records,list) else 'unknown',flush=True)


if __name__=='__main__':main()
