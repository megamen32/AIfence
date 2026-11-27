#!/usr/bin/env python3
"""Record local Airlock model usage for only the frozen QA conversations."""
import json
from pathlib import Path
import subprocess
import uuid

import blind30_score
import local_qa_users

PACKAGE=Path(__file__).resolve().parents[3]/'.runtime/blind-director/blind30-sol-v2'


def main():
    local_qa_users.assert_local('http://localhost:42082')
    _,manifest=blind30_score.verify(PACKAGE)
    ids=[];run_ids=[]
    for item in manifest['cases']:
        path=PACKAGE/'records'/f"{item['id']}.json"
        if not path.exists():raise RuntimeError('All records must be frozen')
        record=json.loads(path.read_text())['record']
        ids.append(str(uuid.UUID(record['chat_id'])));run_ids.append(str(uuid.UUID(record['run_id'])))
    chats=','.join("'"+x+"'" for x in ids);runs=','.join("'"+x+"'" for x in run_ids)
    sql="select coalesce(json_agg(json_build_object('conversation_id',conversation_id,'system_run_id',system_run_id,'model',model,'errored',errored)), '[]'::json) from llm_usage where conversation_id::text in ("+chats+") or system_run_id::text in ("+runs+");"
    result=subprocess.run(['docker','--context','colima-aifence','exec','-i','aifence-qa-postgres-1',
                           'sh','-c','psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'],input=sql,text=True,capture_output=True,check=True)
    rows=json.loads(result.stdout)
    blind30_score.save(PACKAGE/'local-model-usage-v2.json',{'rows':rows,'scope':'frozen QA conversations and system runs only',
        'note':'Airlock usage record; upstream provider not independently attested'})
    covered={cid for cid,rid in zip(ids,run_ids) if any(r.get('model')=='codex/gpt-6.1-sol' and (r.get('conversation_id')==cid or r.get('system_run_id')==rid) for r in rows)}
    print('Sol model usage:',len(covered),'/',len(ids),'conversations')


if __name__=='__main__':main()
