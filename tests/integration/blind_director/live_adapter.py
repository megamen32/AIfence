#!/usr/bin/env python3
"""Single-account blind-run transport for the dedicated local stand."""
import argparse
import datetime as dt
import hashlib
from html.parser import HTMLParser
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT / 'apps/product-manager/research'))
from xlsx import Workbook
from prompt_guard import require_prompt
from token_client import get_token

ORIGIN = 'http://localhost:42081'

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Redirect refused; credentials remain on the stand')

OPENER = urllib.request.build_opener(NoRedirect)
ACCOUNTS = ROOT / '.runtime/blind-director/accounts.json'


def safe_url(url):
    u = urllib.parse.urlsplit(url)
    if u.scheme != 'http' or u.port != 42081 or not (u.hostname == 'localhost' or (u.hostname or '').endswith('.localhost')) or u.username or u.password:
        raise RuntimeError('Refusing a non-stand URL')
    return url


def app_connection(slug, route):
    parsed = urllib.parse.urlsplit(route)
    expected = slug + '.aifence.bezrabotnyi.com'
    if parsed.scheme == 'https' and parsed.hostname == expected and parsed.port is None and not parsed.username and not parsed.password:
        return ORIGIN, expected
    old = f'http://{slug}.localhost:42081'
    if route.rstrip('/') == old:
        return old, None
    raise RuntimeError('Unexpected application route; refusing credentials')


def request(method, url, token=None, payload=None, data=None, headers=None):
    headers = dict(headers or {})
    if token: headers['Authorization'] = 'Bearer ' + token
    if payload is not None:
        headers['Content-Type'] = 'application/json'
        data = json.dumps(payload, ensure_ascii=False).encode()
    req = urllib.request.Request(safe_url(url), data=data, method=method, headers=headers)
    try:
        with OPENER.open(req, timeout=45) as resp:
            content = resp.read()
            return json.loads(content) if content else {}
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f'{method} {urllib.parse.urlsplit(url).path}: HTTP {exc.code}; inspect stand server-side') from None


def login(case):
    if ACCOUNTS.stat().st_mode & 0o077: raise RuntimeError('Account manifest must be mode 600')
    row = json.loads(ACCOUNTS.read_text())[case]
    if row['origin'].rstrip('/') != ORIGIN or not row.get('active') or not {'product-manager','metrika-analyst','market-watch'}.issubset(set(row.get('members',[]))):
        raise RuntimeError('Account origin or active state mismatch')
    return get_token(case), row


def batches(source, asof, facts):
    return {'schema_version':'aifence.v1', 'source':source, 'as_of':asof, 'status':'ok', 'observations':facts, 'warnings':['Synthetic input.']}


def prepare_inputs(folder, generator_path):
    """Deterministic source conversion; no truth or answer files are read."""
    evidence = json.loads((folder/'metrika.json').read_text())
    asof = evidence['as_of']
    spec = importlib.util.spec_from_file_location('generator_contract', generator_path)
    generator = importlib.util.module_from_spec(spec); spec.loader.exec_module(generator)
    labels = {v['label']: k.lower() for k,v in generator.PRODUCTS.items()}
    book = Workbook(folder/'Продажи.xlsx')
    try:
        rows = book.rows('Продажи'); header = rows.pop(1)
        for n, raw in sorted(rows.items()):
            row = {header[k]: v for k,v in raw.items() if k in header}
            name = row.get('product_family'); product = labels.get(name)
            if not product or not isinstance(row.get('orders_index'), (float,int)):
                raise RuntimeError('Unsupported synthetic sales row')
            start = dt.date.fromisoformat(str(row['week_start']))
            evidence['observations'].append({'id':f'synthetic-sales-row-{n}', 'kind':'metric', 'product_key':product,
                'metric_key':'family_orders_idx', 'value':row['orders_index'], 'unit':'index',
                'period':{'from':start.isoformat(), 'to':(start+dt.timedelta(days=6)).isoformat()},
                'segment':str(row.get('device') or ''), 'status':'observed',
                'source_ref':{'source_id':'synthetic-sales','locator':f'Продажи!row {n}','revision':'synthetic-v1'},
                'details':{'synthetic':True,'origin':'synthetic_fixture','base_period':str(row.get('base_period') or '')}})
    finally: book.close()
    context = {'schema_version':'aifence.watch-context.v1','synthetic':True,'batches':{}}
    for filename, source, kind, metric in [('releases.json','release','release','release_log'),('backlog.json','backlog','task','backlog_item')]:
        facts=[]
        for i, row in enumerate(json.loads((folder/filename).read_text()),1):
            date = str(row.get('release_date') or row.get('created_date') or '')
            dt.date.fromisoformat(date)
            family = str(row.get('family') or '').lower()
            facts.append({'id':f'synthetic-{source}-{i}', 'kind':kind, 'product_key':family or 'all', 'metric_key':metric,
                'value':str(row.get('description') or row.get('title') or ''), 'unit':'text',
                'period':{'from':date,'to':date}, 'status':'observed',
                'source_ref':{'source_id':'synthetic-'+source,'locator':f'{filename}#/{i-1}','revision':'synthetic-v1'},
                'details':{'synthetic':True,'origin':'synthetic_fixture', **row}})
        context['batches'][source] = batches(source,asof,facts)
    # An absent break registry is unknown, not an empty confirmed registry.
    return json.dumps(evidence,ensure_ascii=False).encode(), json.dumps(context,ensure_ascii=False).encode()


def multipart(series, context):
    boundary='aifence-'+uuid.uuid4().hex
    body=bytearray()
    for name, content in [('series',series),('context',context)]:
        body.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; filename="{name}.json"\r\nContent-Type: application/json\r\n\r\n'.encode())
        body.extend(content); body.extend(b'\r\n')
    body.extend(f'--{boundary}--\r\n'.encode())
    return bytes(body), 'multipart/form-data; boundary='+boundary


def call_tool(token, app, name):
    result = request('POST', ORIGIN+f'/api/v1/agents/{app}/deployments/production/tools/{name}/invoke',token,payload={'input':{}})
    output = result.get('resultJson')
    if not isinstance(output,str): raise RuntimeError('Tool result missing resultJson')
    decoded = json.loads(output)
    if decoded.get('isError') or decoded.get('is_error'): raise RuntimeError('Tool returned an error')
    if 'output' in decoded: decoded = decoded['output']
    if isinstance(decoded,str): decoded=json.loads(decoded)
    return decoded


def tasks(records):
    rows=records.get('records')
    if not isinstance(rows,list) or len(rows)>=100: raise RuntimeError('PM record list incomplete or at limit')
    return sorted(str(x['task']['id']) for x in rows if isinstance(x.get('task'),dict) and x['task'].get('id'))


def save_private(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as out:
        json.dump(data, out, ensure_ascii=False, indent=2)
        out.write('\n')


def send_and_wait(token, cid, app, message, timeout, out, label, purpose):
    require_prompt(message, purpose)
    payload={'requestId':str(uuid.uuid4()), 'message':message,'workspace':{'appId':app,'path':'/'}}
    save_private(out/(label+'-request.json'), payload)
    receipt=request('POST',ORIGIN+f'/api/v1/system/conversations/{cid}/prompt',token,payload=payload)
    save_private(out/(label+'-receipt.json'), receipt)
    if not receipt.get('runId'): raise RuntimeError('Prompt admission unconfirmed; do not resubmit')
    deadline=time.monotonic()+timeout
    state = {}
    while time.monotonic()<deadline:
        state=request('GET',ORIGIN+f'/api/v1/conversations/{cid}/chat',token)
        run=request('GET',ORIGIN+f'/api/v1/runs/{receipt["runId"]}',token).get('run',{})
        status=str(run.get('status','')).lower()
        if status in ('completed','complete','done','success') and not state.get('conversation',{}).get('inFlightRunId'):
            return receipt,state,'completed'
        if status in ('failed','error','cancelled','canceled','denied','timeout'):
            return receipt,state,'failed'
        if state.get('conversation',{}).get('status') == 'awaiting_confirmation':
            return receipt,state,'unknown'
        time.sleep(2)
    return receipt,state,'timeout'


def final_reply(chat, run_id):
    """Only a final assistant message from this run can answer the director."""
    messages = chat.get('messages', [])
    for message in reversed(messages):
        role = message.get('role')
        if role not in ('assistant', 'user', 'tool'):
            continue
        if role != 'assistant' or message.get('runId', run_id) != run_id:
            return ''
        parts = message.get('parts') or []
        if isinstance(parts, str):
            parts = json.loads(parts)
        if any(part.get('type') == 'tool-call' for part in parts):
            return ''
        return (message.get('content') or '').strip()
    return ''


def confirmation_mode(text):
    """Act only on an explicit, unambiguous request directed to the user."""
    text = text.casefold().replace('ё', 'е')
    if not re.search(r'задач|бэклог', text):
        return 'none'
    direct = re.search(r'подтвердите|нажмите|откройте|выберите|разрешите|ответьте|напишите|скажите|(?:подтвердить|создать|поставить)\s+(?:эту\s+)?задачу\s*\?', text)
    if not direct:
        return 'none'
    ui = bool(re.search(r'карточк|панел|кнопк|интерфейс', text) and re.search(r'подтверд|нажм|откро|выбер', text))
    reply = bool(re.search(r'ответ|напиш|скаж', text) and re.search(r'\bда\b|соглас', text))
    if ui and reply: return 'ambiguous'
    if ui: return 'ui'
    if reply: return 'text'
    return 'ambiguous'


def proposal(records):
    choices = [x for x in records.get('records', []) if x.get('hypothesis', {}).get('status') == 'proposed' and not x.get('task')]
    if len(choices) != 1:
        raise RuntimeError('Confirmation request does not identify exactly one proposed card')
    row = choices[0]
    identifier, version = row.get('hypothesis', {}).get('id'), row.get('version')
    if not identifier or not isinstance(version, int) or version < 1:
        raise RuntimeError('Proposed card ID/version missing')
    return row


class ApprovalForm(HTMLParser):
    def __init__(self):
        super().__init__(); self.forms=[]; self.form=None; self.button=None

    def handle_starttag(self, tag, attrs):
        attrs=dict(attrs)
        if tag == 'form':
            self.form={'action':attrs.get('action'), 'method':attrs.get('method','get').lower(), 'inputs':{}, 'buttons':[]}
        elif self.form is not None and tag == 'input':
            if attrs.get('name') in self.form['inputs']:
                raise RuntimeError('Duplicate form input')
            self.form['inputs'][attrs.get('name')] = attrs.get('value','')
        elif self.form is not None and tag == 'button':
            self.button={'attrs':attrs, 'text':''}

    def handle_data(self, data):
        if self.button is not None: self.button['text'] += data

    def handle_endtag(self, tag):
        if tag == 'button' and self.form is not None and self.button is not None:
            self.form['buttons'].append(self.button); self.button=None
        elif tag == 'form' and self.form is not None:
            self.forms.append(self.form); self.form=None


def checked_approval_form(html, identifier, version):
    parser=ApprovalForm(); parser.feed(html)
    matches=[]
    for form in parser.forms:
        if form['method'] != 'post' or form['action'] not in ('./approve','/approve'):
            continue
        if form['inputs'].get('id') != identifier or form['inputs'].get('version') != str(version):
            continue
        buttons=[b for b in form['buttons'] if b['attrs'].get('name') == 'confirmed' and b['attrs'].get('value') == 'yes' and ' '.join(b['text'].split()) == 'Создать эту задачу']
        if len(buttons) == 1: matches.append(form)
    if len(matches) != 1:
        raise RuntimeError('Expected one rendered approval button with exact card ID and version')
    return matches[0]


def get_card_form(token, app_url, identifier, version, app_host=None):
    card_url=safe_url(app_url+'/?'+urllib.parse.urlencode({'id':identifier}))
    headers={'Authorization':'Bearer '+token}
    if app_host: headers['Host']=app_host
    req=urllib.request.Request(card_url, headers=headers)
    try:
        with OPENER.open(req,timeout=45) as resp:
            if resp.status != 200: raise RuntimeError('Card GET did not return 200')
            html=resp.read(2*1024*1024+1)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f'Card GET: HTTP {exc.code}') from None
    if len(html)>2*1024*1024: raise RuntimeError('Card page too large')
    return checked_approval_form(html.decode('utf-8'),identifier,version)


def select_prompt(args, manifest, questions):
    if args.purpose == 'price_acceptance':
        if not args.prompt_file or not args.smoke:
            raise RuntimeError('Price acceptance requires an owner prompt file and separate smoke mode')
        path = Path(args.prompt_file).resolve()
        if not path.is_relative_to((ROOT/'.runtime/blind-director').resolve()):
            raise RuntimeError('Prompt file must remain in private runtime')
        question = path.read_text(encoding='utf-8')
    else:
        if args.prompt_file:
            raise RuntimeError('Prompt override forbidden for fixed director questions')
        question = 'Как у нас дела с магазином?' if args.smoke else questions[manifest['question_assignment'][args.case]]
    require_prompt(question, args.purpose)
    return question


def main():
    p=argparse.ArgumentParser(); p.add_argument('--case',required=True,choices=[f'case-{i:02d}' for i in range(1,7)])
    p.add_argument('--run',required=True); p.add_argument('--timeout',type=int,default=300)
    p.add_argument('--live-ready',action='store_true',help='only after owner confirms B/C readiness')
    p.add_argument('--smoke',action='store_true',help='use independent smoke input and never score acceptance')
    p.add_argument('--owner-send',action='store_true',help='only the owner may run this live command')
    p.add_argument('--purpose',choices=('director','price_acceptance'),default='director')
    p.add_argument('--prompt-file',help='private file with the exact owner-supplied price acceptance prompt')
    args=p.parse_args()
    if not args.owner_send: raise SystemExit('Live user prompt sending is reserved to the owner')
    if not args.live_ready: raise SystemExit('Live blocked pending owner confirmation of B/C')
    run=Path(args.run).resolve()
    if run.parent != (ROOT/'.runtime/blind-director').resolve(): raise SystemExit('Invalid private run')
    manifest=json.loads((run/'run.json').read_text())
    folder = run/'smoke-input' if args.smoke else run/'inputs'/args.case
    if not folder.is_dir() or (args.smoke and folder.resolve() == (run/'inputs'/args.case).resolve()): raise SystemExit('Separate smoke input missing')
    if not args.smoke:
        for name, digest in manifest['input_sha256'][args.case].items():
            if hashlib.sha256((folder/name).read_bytes()).hexdigest()!=digest: raise SystemExit('Input hash changed')
    questions=json.loads((HERE/'questions.json').read_text())
    question=select_prompt(args, manifest, questions)
    token, account=login('smoke' if args.smoke else args.case)
    agents=request('GET',ORIGIN+'/api/v1/agents',token).get('agents',[])
    byslug={a.get('slug'):a for a in agents}
    if not all(x in byslug for x in ('product-manager','metrika-analyst')): raise RuntimeError('Required apps unavailable to user')
    pm,metrika=byslug['product-manager'],byslug['metrika-analyst']
    pm_id=pm['id']; app_url=ORIGIN+'/api/v1/agents/'+metrika['id']+'/deployments/production/http'
    app_headers={}
    closed_origin='http://metrika-analyst.localhost:42081'
    series,context=prepare_inputs(folder,run/'tools/generator.py')
    form,ctype=multipart(series,context)
    result=request('POST',app_url+'/watchdog',token,data=form,headers={**app_headers,'Content-Type':ctype,'Origin':closed_origin})
    if result.get('status')!='stored': raise RuntimeError('Snapshot upload not confirmed')
    result=request('POST',app_url+'/watchdog',token,data=b'action=select_blind',headers={**app_headers,'Content-Type':'application/x-www-form-urlencoded','Origin':closed_origin})
    if result.get('status')!='selected': raise RuntimeError('Active snapshot selection not confirmed')
    discovered=call_tool(token,metrika['id'],'discover_watchdog_series')
    discovery_status=discovered.get('status')
    if not discovered.get('dataset_id') or discovery_status not in ('ok','partial'):
        raise RuntimeError('Discovery or active dataset unconfirmed: '+str(discovery_status))
    if discovery_status == 'partial' and not args.smoke:
        raise RuntimeError('Discovery partial; refusing acceptance on truncated dataset')
    if discovery_status == 'partial':
        print('Smoke discovery partial: '+str(discovered.get('warnings',[])),flush=True)
    before=call_tool(token,pm_id,'list_hypotheses'); ids_before=tasks(before)
    if before.get('records'): raise RuntimeError('Account not empty; separate smoke account or explicit cleanup required')
    out=(run/'smoke-results'/(args.case+'-attempt-'+uuid.uuid4().hex[:12])) if args.smoke else (run/'live-results'/args.case)
    out.mkdir(parents=True,exist_ok=False,mode=0o700)
    if args.smoke: print('Smoke output:', out, flush=True)
    save_private(out/'backend-before.json', before)
    conversation=request('POST',ORIGIN+'/api/v1/system/conversations',token,payload={'title':'Synthetic test'})['conversation']; cid=conversation['id']
    save_private(out/'conversation.json', conversation)
    receipt,chat,first_status=send_and_wait(token,cid,pm_id,question,args.timeout,out,'first',args.purpose)
    save_private(out/'first-chat.json', chat)
    after=call_tool(token,pm_id,'list_hypotheses'); ids_first=tasks(after)
    save_private(out/'backend-after-first.json', after)
    response=final_reply(chat, receipt['runId'])
    if first_status == 'completed' and not response: first_status='failed'
    second_request=None; second_response=None; second=after; chat2=None; second_status=None
    mode=confirmation_mode(response) if first_status == 'completed' else 'none'
    approval={'mode':mode,'outcome':'not_requested','card_id':None,'card_version':None,
              'card_before':None,'card_after':None,'task_receipt':None}
    if mode == 'ui':
        selected=proposal(after)
        approval.update(card_id=selected['hypothesis']['id'],card_version=selected['version'],card_before=selected,
                        outcome='awaiting_browser_click')
        pm_url=ORIGIN+'/api/v1/agents/'+pm_id+'/deployments/production/http'; pm_host=None
        form=get_card_form(token, pm_url, approval['card_id'], approval['card_version'], pm_host)
        approval['rendered_form']={'method':form['method'],'action':form['action'],'id':approval['card_id'],'version':approval['card_version'],'button':'Создать эту задачу'}
        save_private(out/'confirmation-before.json',approval)
        # An authenticated browser must open this exact card and click its
        # rendered approval button. A raw POST is not browser-click evidence.
        raise RuntimeError('UI approval pending: real isolated-user browser click required')
    elif mode == 'text':
        approval['outcome']='failed_text_confirmation_unsupported'
    elif mode == 'ambiguous':
        approval['outcome']='ambiguous_request'
    record={'case':args.case,'question':question,'chat_id':cid,'first_response':response,'first_status':first_status,'second_request':second_request,
            'second_response':second_response,'task_ids_before':ids_before,'task_ids_after_first':ids_first,'task_ids_after_second':tasks(second),
            'approval':approval,'transport_evidence':{'run_id':receipt['runId'],'conversation_id':cid},
            'backend_evidence':{'before':before,'after_first':after,'after_second':second},
            'active_dataset_id':discovered['dataset_id'],'chat_snapshot':chat,'second_chat_snapshot':chat2,'second_status':second_status}
    path=out/'record.json'; save_private(path, record)
    if not args.smoke:
        subprocess.run([sys.executable,str(HERE/'harness.py'),'freeze','--run',str(run),'--case',args.case,'--record',str(path)],check=True)
    print('Recorded', 'smoke' if args.smoke else args.case, 'without publishing evidence')

if __name__=='__main__':
    try: main()
    except Exception as exc:
        print(f'Live adapter failed: {type(exc).__name__}' + (f': {exc}' if isinstance(exc, RuntimeError) else ''),file=sys.stderr)
        raise SystemExit(1)
