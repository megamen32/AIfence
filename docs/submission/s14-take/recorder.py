#!/usr/bin/env python3
"""Record a real local Airlock session. Requires Playwright 1.60+ and FFmpeg."""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
from datetime import datetime, timezone
from contextlib import nullcontext
from pathlib import Path
from urllib.parse import parse_qs, urlsplit, urljoin
from html import unescape

from playwright.sync_api import sync_playwright


PROMPT = 'слушайте, может нам цену на Касперского снизить? продажи вроде упали, конкуренты наверное дешевле'
DEFAULT_WATCH = 'watch-3df8a337b60f0d84c3d02e5b381ae1ca'
UUID = re.compile(r'/chat/([0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12})(?:[/?#]|$)', re.I)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def json_write(path, value):
    temporary = path.with_suffix(path.suffix + '.writing')
    temporary.write_text(json.dumps(value, indent=2, ensure_ascii=False), encoding='utf-8')
    os.replace(temporary, path)


def get_tools(args):
    chrome = args.chrome or os.environ.get('AIFENCE_CHROME')
    if not chrome and sys.platform == 'darwin':
        matches = sorted((Path.home() / 'Library/Caches/ms-playwright').glob(
            'chromium-*/chrome-mac-arm64/*/Contents/MacOS/Google Chrome for Testing'))
        chrome = str(matches[-1]) if matches else None
    candidates = [args.ffmpeg, os.environ.get('AIFENCE_FFMPEG'), shutil.which('ffmpeg')]
    try:
        import imageio_ffmpeg
        candidates.append(imageio_ffmpeg.get_ffmpeg_exe())
    except ImportError:
        pass
    if sys.platform == 'darwin':
        candidates += [str(p) for p in (Path.home() / '.cache/uv').glob('archive-v0/*/lib/python*/site-packages/imageio_ffmpeg/binaries/ffmpeg-macos-aarch64*')]
    ffmpeg = None
    for exe in dict.fromkeys(x for x in candidates if x):
        try:
            ok = subprocess.run([exe, '-version'], capture_output=True, timeout=5)
        except (OSError, subprocess.TimeoutExpired):
            continue
        if ok.returncode == 0 and b'ffmpeg version' in ok.stdout:
            ffmpeg = exe
            break
    if not ffmpeg:
        raise RuntimeError('No runnable FFmpeg. Set --ffmpeg to a verified executable.')
    return chrome, ffmpeg


def public_origin(url):
    u = urlsplit(url)
    if u.scheme != 'http' or u.hostname not in ('localhost', 'product-manager.localhost', 'metrika-analyst.localhost') or not u.port:
        raise ValueError('This recorder accepts local Airlock demo origins only.')
    if u.username or u.password or u.path not in ('', '/') or u.query or u.fragment:
        raise ValueError('Use an origin with no credentials, path, query or fragment.')
    return f'{u.scheme}://{u.netloc}'


def visible_matches_backend(dom, markdown):
    """Compare all word/number tokens; tolerate Markdown decoration and copy controls."""
    flat = re.sub(r'\[([^\]]+)\]\([^)]+\)', r'\1', markdown)
    normalize = lambda s: re.findall(r'[a-zа-яё0-9]+', unescape(s).casefold())
    want, seen = normalize(flat), normalize(dom)
    if len(want) < 4:
        return False
    return any(seen[i:i + len(want)] == want for i in range(max(0, len(seen)-len(want)+1)))
    
    
class Session:
    def __init__(self, args, out):
        self.args = args
        self.out = out
        self.start = None
        self.answer = ''
        self.new_watch = None
        self.source_links = []
        self.owner_id = None
        self.observer_token = None
        self.case_ids_before = set()
        self.events = []
        self.r = {
            'schema_version': 1, 'status': 'partial', 'product_result': {'diagnosis': 'not graded by the recorder', 'task_created': False}, 'mode': args.mode,
            'profile_kind': args.profile_kind, 'origin': args.origin,
            'prompt': PROMPT, 'conversation_id': None, 'model_answer_verified': False,
            'visible_answer_sha256': None, 'graph': None, 'events': self.events,
            'files': {}, 'recorded_seconds': 0, 'headed': True, 'context_count': 1,
            'page_count': 1, 'source': 'single native Playwright BrowserContext.record_video at 1440 x 810', 'capture_mode': 'record_video', 'viewport': [1440,810],
            'audio': False, 'unsolicited_task_confirmation': False,
            'seeded_hypothesis_id': (args.watch_id if args.mode == 'prepared' else None), 'answer_quality': 'unassessed',
            'scope': {'fresh':'New hosted question under this owner. No saved answer or foreign card is used.', 'replay':'Read-only revisit of one actual recorded hosted conversation under its owner.', 'prepared':'Read-only presentation of a pre-existing S14 hypothesis, task and release graph. No hosted answer is claimed.'}[args.mode],
        }

    def mark(self, action, **details):
        t = round(time.monotonic() - self.start, 3) if self.start is not None else 0
        item = {'t': t, 'action': action, **details}
        self.events.append(item)
        print(json.dumps(item, ensure_ascii=False), flush=True)
        json_write(self.out / 'progress.json', self.r)

    def timeout(self, cap=8000):
        remaining = 166 - (time.monotonic() - self.start)
        if remaining <= 0:
            raise TimeoutError('The continuous recording deadline expired.')
        return max(1, min(cap, int(remaining * 1000)))

    def pause(self, page, ms):
        page.wait_for_timeout(self.timeout(ms))

    def open_graph(self, pg, watch_id):
        if not watch_id:
            self.mark('native_graph_unavailable_for_this_profile')
            return False
        url = self.args.pm_origin + '/?id=' + watch_id + '&mode=demo&tab=after'
        resp = pg.goto(url, wait_until='domcontentloaded', timeout=self.timeout(9000))
        if not resp or resp.status != 200 or urlsplit(pg.url).netloc != urlsplit(self.args.pm_origin).netloc:
            raise RuntimeError('The real Product Manager did not open under this account.')
        card = pg.locator('.release-history').first
        card.wait_for(timeout=self.timeout())
        points = card.locator('.release-point')
        visible = card.inner_text(timeout=self.timeout())
        if points.count() != 12 or not all(x in visible for x in ('R-25','R-26','58,2','27,4')):
            raise RuntimeError('The graph does not match the selected case data.')
        card.scroll_into_view_if_needed(timeout=self.timeout())
        self.mark('opened_existing_native_s14_graph', watch_id=watch_id, points=points.count())
        self.pause(pg, 3000)
        last = points.last
        last.locator('circle').first.hover(force=True, timeout=self.timeout())
        label = last.get_attribute('aria-label') or ''
        if not all(x in label for x in ('213','775')):
            raise RuntimeError('The displayed weekly point does not match S14.')
        self.r['graph'] = {'source': url, 'http': resp.status, 'points': points.count(),
                           'period_totals_per_100': [58.2, 27.4], 'last_week': label,
                           'native_html_sha256': sha(resp.body()), 'prepared_before_chat': self.args.mode == 'prepared'}
        self.mark('hovered_last_week', label=label)
        self.pause(pg, 4000)
        return True

    def show_case(self, pg, watch_id, prepared):
        """Follow an actual PM card. Approval is exact-card, fresh-mode, local-demo-only."""
        url = self.args.pm_origin + '/?id=' + watch_id + '&mode=demo&tab=hypothesis'
        res = pg.goto(url, wait_until='domcontentloaded', timeout=self.timeout())
        if not res or res.status != 200 or urlsplit(pg.url).netloc != urlsplit(self.args.pm_origin).netloc:
            raise RuntimeError('The signed-in owner could not open the case.')
        section = pg.locator('#hypothesis-actions').first
        section.wait_for(timeout=self.timeout())
        self.mark('opened_preexisting_case' if prepared else 'opened_case_linked_by_actual_answer', watch_id=watch_id)
        self.pause(pg, 3000)
        trend = pg.locator('figure.trend:visible').first
        if trend.count():
            trend.scroll_into_view_if_needed(timeout=self.timeout())
            self.mark('examined_sales_chart')
            self.pause(pg, 2700)
        section.scroll_into_view_if_needed(timeout=self.timeout())
        def task_id():
            links = section.locator('a:visible').all_inner_texts()
            return next((t.strip() for t in links if re.fullmatch(r'PM-[a-zA-Z0-9_-]+',t.strip())), None)
        prior = task_id()
        self.r['task_before'] = prior
        self.mark('read_existing_task' if prior else 'read_proposed_task', task_id=prior)
        self.pause(pg, 3600)
        rawform = section.locator('form[action$="approve"]')
        create = rawform.get_by_role('button', name='Создать эту задачу', exact=True)
        if not prior and self.args.mode == 'fresh' and self.args.confirm_demo_task:
            if watch_id != self.new_watch or create.count()!=1 or rawform.locator('input[name=id]').input_value() != watch_id:
                raise RuntimeError('The visible proposal does not match the case returned by the new answer.')
            if create.get_attribute('name') != 'confirmed' or create.get_attribute('value') != 'yes':
                raise RuntimeError('Unrecognized confirmation control.')
            self.mark('explicit_demo_approval_for_this_proposal', watch_id=watch_id)
            create.click(timeout=self.timeout())
            section.locator('a:visible').first.wait_for(timeout=self.timeout())
            if not task_id():
                raise RuntimeError('The demo backlog did not return a task ID.')
        self.r['task_after'] = task_id()
        self.r['product_result']['task_created'] = bool(self.r['task_after'] and not prior)
        if self.r['task_after'] != prior:
            self.mark('verified_new_demo_task', task_id=self.r['task_after'])
            self.pause(pg, 1200)

    def owner_cards(self, ctx):
        path='/api/v1/agents/'+self.args.pm_app_id+'/deployments/production/http/?mode=demo&tab=overview'
        resp=ctx.request.get(self.args.origin+path, headers={'Authorization':'Bearer '+self.observer_token, 'Accept':'text/html'}, timeout=5500, max_redirects=0)
        if resp.status!=200:
            raise RuntimeError('The current owner’s Product Manager catalogue was unavailable.')
        from html.parser import HTMLParser
        records=set()
        class Links(HTMLParser):
            def handle_starttag(self, tag, attrs):
                if tag!='a':
                    return
                href=dict(attrs).get('href','')
                code=parse_qs(urlsplit(href).query).get('id',[''])[0]
                if re.fullmatch(r'watch-[0-9a-f]{32,64}',code):
                    records.add(code)
        Links().feed(resp.text())
        return records

    def guard_initial_prompt(self, pg):
        """Pass the browser request unchanged only when its actual workspace matches."""
        endpoint = re.compile(re.escape(self.args.origin) + r'/api/v1/(?:system/)?conversations/[0-9a-f-]+/prompt/?(?:\?.*)?$')
        def admission_request(req):
            path = urlsplit(req.url).path
            if req.method in ('POST', 'PUT') and path.startswith('/api/v1/') and 'conversations' in path:
                self.mark('native_conversation_request', method=req.method, path=path)
        def guard(route):
            req=route.request
            data=req.post_data_json if req.method=='POST' else {}
            if not isinstance(data,dict):
                data={}
            workspace=data.get('workspace') or {}
            valid=(req.method=='POST' and data.get('message')==PROMPT and
                   workspace.get('appId')==self.args.pm_app_id and workspace.get('path')=='/'
                   and not self.r.get('outbound_workspace_verified'))
            self.r['outbound_workspace_verified']=valid
            self.r['native_workspace']={'appId':workspace.get('appId'),'path':workspace.get('path'), 'attached_view':bool(workspace.get('view'))}
            self.mark('native_pm_workspace_verified' if valid else 'unmatched_prompt_blocked',
                      app_id=workspace.get('appId'), path=workspace.get('path'))
            if valid:
                return route.continue_()
            return route.abort()
        pg.on('request',admission_request)
        pg.route(endpoint, guard)

    def wait_workspace(self, pg):
        # panel=chat is the shell's presentation generated by its «Развернуть чат» button.
        # It keeps appId and path and omits the cross-site iframe from the native view context.
        selected = pg.locator('.workspace-form[aria-label="Product Manager"]')
        selected.wait_for(state='attached', timeout=self.timeout(17000))
        pg.locator('.workspace-open-app:visible').wait_for(timeout=self.timeout(6000))
        self.pause(pg, 600)
        if selected.is_visible() or parse_qs(urlsplit(pg.url).query).get('app') != [self.args.pm_app_id]:
            raise RuntimeError('The native expanded chat is not bound to the Product Manager workspace.')
        self.mark('native_product_manager_expanded_chat_ready',path='/')

    def show_operator_evidence(self, pg):
        path=self.args.evidence_file or (self.args.profile.parent/'evidence_urls.json')
        if not path.is_file():
            self.mark('no_verified_owner_source_file')
            return
        proof=json.loads(path.read_text())
        if proof.get('owner_id')!=self.owner_id:
            raise RuntimeError('Source receipt belongs to another owner.')
        rows=proof.get('rows',[])
        selected=[r for r in rows if r['metric']=='checkouts' and r['device'] in ('desktop','mobile')]
        if len(selected)!=2 or not proof.get('dataset_id'):
            raise RuntimeError('Incomplete source point identities.')
        recorded=[]
        for row in selected:
            u=urlsplit(row['url']);qs=parse_qs(u.query)
            if (u.scheme!='http' or u.netloc!=urlsplit(self.args.pm_origin).netloc or u.path!='/'
                    or qs.get('snapshot')!=[row['snapshot_id']] or qs.get('obs')!=[row['observation_id']]):
                raise RuntimeError('The source link does not match the current owner receipt.')
            resp=pg.goto(row['url'],wait_until='domcontentloaded',timeout=self.timeout(8000))
            txt=pg.locator('body').inner_text(timeout=self.timeout())
            norm=' '.join(txt.casefold().split())
            if (not resp or resp.status!=200 or 'kaspersky small office security' not in norm or
                    row['metric'] not in norm or row['device'] not in norm or str(row['value']) not in norm):
                raise RuntimeError('Visible source row does not match the verified evidence.')
            recorded.append({'url':row['url'],'metric':row['metric'],'value':row['value'],'device':row['device'],
                             'snapshot_id':row['snapshot_id'],'observation_id':row['observation_id']})
            self.mark('operator_checked_source_after_answer',metric=row['metric'],device=row['device'],value=row['value'])
            pg.get_by_text('Kaspersky Small Office Security',exact=False).first.scroll_into_view_if_needed(timeout=self.timeout(2500))
            self.pause(pg,3000)
        self.r['operator_evidence']={'attribution':'read-only independent source check after the answer, not a model citation',
                'rows':recorded, 'dataset_id':proof['dataset_id']}

    def show_actual_followup(self, pg):
        for href in self.source_links:
            parsed = urlsplit(href)
            if parsed.scheme == 'http' and parsed.netloc == urlsplit(self.args.pm_origin).netloc:
                card = parse_qs(parsed.query).get('id', [''])[0]
                if re.fullmatch(r'watch-[0-9a-f]{32,64}', card):
                    self.new_watch = card
                    break
        if not self.new_watch and self.args.mode=='fresh':
            new_ids=self.owner_cards(pg.context)-self.case_ids_before
            self.r['new_case_ids_under_current_owner'] = sorted(new_ids)
            if len(new_ids)==1:
                self.new_watch=next(iter(new_ids))
                self.mark('found_only_new_case_under_verified_owner', watch_id=self.new_watch)
        if self.new_watch:
            self.show_case(pg, self.new_watch, prepared=False)
            self.open_graph(pg, self.new_watch)
            self.r['status'] = 'complete'
        else:
            self.mark('no_proposal_link_in_actual_answer_no_task_created')
            self.r['product_result']['followup'] = 'No attributable new card or proposal; no task was created.'
            if self.args.metrics_url:
                self.open_native_metrics(pg)
                self.r['status'] = 'complete'
            elif self.args.mode == 'fresh':
                self.show_operator_evidence(pg)

    def open_native_metrics(self, pg):
        url=self.args.metrics_url
        res=pg.goto(url, wait_until='domcontentloaded', timeout=self.timeout())
        if not res or res.status != 200:
            raise RuntimeError('The read-only Metrika view was unavailable.')
        text=pg.locator('body').inner_text(timeout=self.timeout())
        expected=json.loads(self.args.metrics_evidence.read_text())
        if (not all(str(expected.get(k,'')).strip() for k in ('dataset_id','metric_label','value_text'))
                or not re.search(r'Kaspersky|Касперск',text,re.I)
                or expected['dataset_id'] not in (res.text() + url)
                or expected['metric_label'] not in text or expected['value_text'] not in text):
            raise RuntimeError('Native Metrika view does not match the selected dataset and its known metric value.')
        chart=pg.locator('svg[role=img],figure svg,svg[class*=chart],canvas').first
        chart.wait_for(timeout=self.timeout())
        chart.scroll_into_view_if_needed(timeout=self.timeout())
        self.mark('opened_native_owner_metrics', source=url)
        self.pause(pg, 3200)
        marks=chart.locator('circle')
        if marks.count():
            marks.last.hover(force=True,timeout=self.timeout())
            self.mark('hovered_native_metric_point')
        self.pause(pg, 2200)
        self.r['graph']={'source':url,'http':res.status,'native_html_sha256':sha(res.body()),'prepared_before_chat':False,'evidence':'native dashboard under same authenticated owner; no preselected PM card'}

    def wait_answer(self, pg, ctx):
        end = self.start + 126
        self.mark('waiting_for_hosted_answer')
        log_next = time.monotonic() + 12
        while time.monotonic() < end:
            if self.args.mode=='fresh' and self.r.get('outbound_workspace_verified') is False:
                raise RuntimeError('Mismatched native workspace; original prompt was not sent to the model.')
            match = UUID.search(pg.url)
            if match:
                cid = match.group(1)
                self.r['conversation_id'] = cid
                url = self.args.origin + '/api/v1/conversations/' + cid + '/chat'
                res = ctx.request.get(url, headers={'Authorization':'Bearer '+self.observer_token}, max_redirects=0, timeout=self.timeout(3500))
                if res.status == 200:
                    d = res.json()
                    c = d.get('conversation', {})
                    if c.get('id') != cid or (self.owner_id and c.get('userId') != self.owner_id):
                        raise RuntimeError('The conversation identity changed.')
                    if c.get('pendingTool') or c.get('pendingTools'):
                        self.mark('confirmation_requested_and_left_pending')
                        return False
                    rows = d.get('messages', [])
                    asked = [m for m in rows if m.get('role') == 'user']
                    pane = pg.locator('.chat-messages').first
                    ans = pane.locator('.msg-response:visible')
                    text = ans.last.inner_text(timeout=self.timeout(1600)).strip() if ans.count() else ''
                    if (len(asked) == 1 and asked[0].get('content', '').strip() == PROMPT
                            and PROMPT in pane.inner_text(timeout=self.timeout(1600)) and text
                            and not c.get('inFlightRunId') and not d.get('activeToolCalls')):
                        back = next((m.get('content','').strip() for m in reversed(rows) if m.get('role') == 'assistant' and m.get('content','').strip()), '')
                        if not back:
                            self.pause(pg, 250)
                            continue
                        # Read once more after paint so a streaming or temporary status is never treated as the final answer.
                        self.pause(pg, 800)
                        if not ans.count() or ans.last.inner_text(timeout=self.timeout(1600)).strip() != text:
                            continue
                        if not visible_matches_backend(text, back):
                            self.pause(pg, 350)
                            continue
                        self.r['backend_answer_sha256'] = sha(back.encode())
                        self.r['answer_dom_matches_backend'] = True
                        self.source_links = ans.last.locator('a[href]').evaluate_all('(els)=>els.map(e=>e.href)')
                        self.answer = text
                        self.r['model_answer_verified'] = True
                        self.r['visible_answer_sha256'] = sha(text.encode())
                        self.r['hosted_message_roles'] = [m.get('role') for m in rows]
                        self.r['answer_quality'] = 'needs human review'
                        self.mark('read_new_hosted_answer', chars=len(text), sha256=self.r['visible_answer_sha256'])
                        return True
            if time.monotonic() >= log_next:
                self.mark('hosted_answer_still_pending', has_id=bool(self.r['conversation_id']))
                log_next = time.monotonic() + 15
            self.pause(pg, 620)
        self.mark('model_deadline_no_substitute_answer')
        return False

    def run(self, pg, ctx):
        box = pg.locator('textarea[aria-label="Написать сообщение"]:visible')
        if self.args.mode != 'prepared':
            box.wait_for(timeout=12000)
            box.click(trial=True, timeout=5000)
        if self.args.mode == 'fresh' and pg.locator('.chat-messages .msg-user').count():
            raise RuntimeError('Expected a new empty conversation.')
        with nullcontext():
            try:
                self.pause(pg, 900)
                if self.args.mode == 'prepared':
                    self.r['model_answer_verified'] = False
                    self.mark('read_only_prepared_S14_no_model_call')
                    self.pause(pg, 2300)
                    self.show_case(pg, self.args.watch_id, prepared=True)
                    self.open_graph(pg, self.args.watch_id)
                    self.r['status'] = 'complete'
                else:
                    if self.args.mode == 'fresh':
                        self.wait_workspace(pg)
                        box.click(timeout=self.timeout())
                        box.press_sequentially(PROMPT, delay=51, timeout=self.timeout(13000))
                        self.pause(pg, 650)
                        if box.input_value() != PROMPT:
                            raise RuntimeError('The editor does not contain the exact director question.')
                        self.mark('verified_exact_prompt_in_native_editor')
                        pg.get_by_role('button', name='Отправить сообщение').click(timeout=self.timeout())
                        if self.r.get('outbound_workspace_verified') is not True:
                            cutoff = time.monotonic() + 27
                            while 'outbound_workspace_verified' not in self.r and time.monotonic()<cutoff:
                                self.pause(pg,120)
                            if not self.r.get('outbound_workspace_verified'):
                                raise RuntimeError('The UI did not send a verified workspace prompt.')
                        self.mark('sent_original_director_prompt')
                    else:
                        self.mark('opened_real_existing_hosted_conversation', conversation_id=self.args.conversation_id)
                    got = self.wait_answer(pg, ctx)
                    if got:
                        self.pause(pg, 1800)
                        msg = pg.locator('.chat-messages .msg-response:visible').last
                        msg.evaluate("(e)=>e.scrollIntoView({behavior:'smooth',block:'start'})")
                        self.pause(pg, 3200)
                        self.show_actual_followup(pg)
                        self.r['status'] = 'complete'
            except Exception as e:
                # Never serialize authentication data, response bodies or a full private URL.
                self.r['failure'] = type(e).__name__ + ': ' + str(e).splitlines()[0][:200]
                self.mark('stopped_incomplete', error_type=type(e).__name__)
            finally:
                self.r['recorded_seconds'] = round(time.monotonic() - self.start, 3)
                self.mark('single_screencast_ended')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--origin', default='http://localhost:42080')
    parser.add_argument('--pm-origin', default='http://product-manager.localhost:42080')
    parser.add_argument('--profile', type=Path, required=True, help='Private external JSON with email and password')
    parser.add_argument('--profile-kind', default='dedicated demo user with preloaded S14 evidence')
    parser.add_argument('--mode', choices=('fresh','replay','prepared'), default='fresh')
    parser.add_argument('--conversation-id', help='Actual hosted conversation UUID for --mode replay')
    parser.add_argument('--pm-app-id', default='01a5630d-261e-4c41-88a7-15fe5bd4fa2b')
    parser.add_argument('--watch-id', help='Dedicated existing card. Allowed in --mode prepared only.')
    parser.add_argument('--evidence-file', type=Path, help='Verified current-owner source links; defaults to evidence_urls.json beside --profile')
    parser.add_argument('--metrics-evidence', type=Path, help='JSON with dataset_id, metric_label and value_text known from this frozen source')
    parser.add_argument('--metrics-url', help='Optional verified native Metrika graph for this signed-in owner')
    parser.add_argument('--confirm-demo-task', action='store_true', help='Explicit approval to click the exact native create button for the case proposed in this new answer, in a verified isolated demo backlog.')
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--chrome')
    parser.add_argument('--ffmpeg')
    args = parser.parse_args()
    args.origin, args.pm_origin = public_origin(args.origin), public_origin(args.pm_origin)
    if args.watch_id and not re.fullmatch(r'watch-[0-9a-f]{32,64}', args.watch_id):
        parser.error('Invalid watch identifier.')
    if args.mode == 'prepared' and not args.watch_id:
        parser.error('--mode prepared requires --watch-id')
    if args.watch_id and args.mode != 'prepared':
        parser.error('A fixed watch ID is reserved for explicitly prepared presentations.')
    if args.mode == 'replay' and not re.fullmatch(r'[0-9a-f-]{36}', args.conversation_id or ''):
        parser.error('--mode replay requires the real hosted conversation UUID')
    if args.mode != 'fresh' and args.confirm_demo_task:
        parser.error('Replay and prepared modes cannot approve tasks.')
    if args.metrics_url:
        if not args.metrics_evidence:
            parser.error('--metrics-url requires --metrics-evidence with an exact S14 dataset identifier and source row/value anchors.')
        u = urlsplit(args.metrics_url)
        if u.scheme != 'http' or u.hostname != 'metrika-analyst.localhost' or u.port != urlsplit(args.origin).port or u.username or u.password:
            parser.error('The native Metrika page must belong to the same local stand.')
    os.umask(0o077)
    out = args.out.expanduser().resolve()
    out.mkdir(parents=True, exist_ok=False)
    cred = json.loads(args.profile.expanduser().read_text(encoding='utf-8'))
    if cred.get('origin') and cred['origin'].rstrip('/') != args.origin:
        raise RuntimeError('The demo account belongs to a different origin.')
    chrome, ffmpeg = get_tools(args)
    session = Session(args, out)
    with sync_playwright() as pw:
        opts = {'headless': False, 'args': ['--use-mock-keychain','--no-default-browser-check',
                                            '--window-size=1440,900','--window-position=25,25']}
        if chrome:
            opts['executable_path'] = chrome
        browser = pw.chromium.launch(**opts)
        # Complete ordinary sign-in and the app relay before opening the only recorded page.
        bootstrap = browser.new_context(locale='ru-RU')
        login = bootstrap.request.post(args.origin + '/auth/login',
                                       data={k: cred[k] for k in ('email', 'password')},
                                       max_redirects=0, timeout=13000)
        if login.status != 200:
            raise RuntimeError(f'Demo sign-in returned HTTP {login.status}.')
        session.observer_token = login.json()['accessToken']
        landing = bootstrap.new_page()
        landing.goto(args.pm_origin + '/', wait_until='domcontentloaded', timeout=15000)
        landing.locator('main,nav,header').first.wait_for(state='visible',timeout=18000)
        if urlsplit(landing.url).netloc != urlsplit(args.pm_origin).netloc:
            raise RuntimeError('Product Manager sign-in could not be verified.')
        state = bootstrap.storage_state()
        bootstrap.close()
        ctx = browser.new_context(viewport={'width': 1440, 'height': 810},
                                  device_scale_factor=1, locale='ru-RU', color_scheme='light',
                                  storage_state=state,
                                  record_video_dir=str(out/'.recording'),
                                  record_video_size={'width':1440,'height':810})
        try:
            me = ctx.request.get(args.origin + '/api/v1/me', headers={'Authorization':'Bearer '+session.observer_token}, timeout=6500)
            if me.status != 200 or (cred.get('user_id') and me.json().get('user', {}).get('id') != cred['user_id']):
                raise RuntimeError('The signed-in demo user could not be verified.')
            session.owner_id = me.json().get('user', {}).get('id')
            session.r['owner_ref_sha256'] = sha((session.owner_id or '').encode())
            if args.mode == 'fresh':
                chats = ctx.request.get(args.origin+'/api/v1/conversations',headers={'Authorization':'Bearer '+session.observer_token},timeout=6500)
                if chats.status != 200 or chats.json().get('conversations') != []:
                    raise RuntimeError('Fresh capture requires a verified owner with zero existing conversations.')
                session.case_ids_before = session.owner_cards(ctx)
                session.r['initial_visible_card_count'] = len(session.case_ids_before)
                session.r['initial_conversation_count'] = 0
                session.r['workspace_presentation'] = 'Native full-width chat. Embedded local app SSO returned 401 on the film stand; independent Product Manager sources open as top-level pages.'
                session.r['preflight'] = 'Authenticated app relay completed in an unrecorded sign-in context. One headed page and one context were recorded.'
            session.start = time.monotonic()
            session.r['started_at'] = datetime.now(timezone.utc).isoformat()
            pg = ctx.new_page()
            video = pg.video
            session.mark('single_screencast_started')
            if args.mode == 'fresh':
                session.guard_initial_prompt(pg)
            pg.set_default_timeout(9000)
            initial = args.origin + '/chat' + ('/' + args.conversation_id if args.mode == 'replay' else '?app=' + args.pm_app_id + '&path=%2F&panel=chat')
            if args.mode == 'prepared':
                # An ordinary local text file labels the rehearsal before its real read-only UI opens.
                # It contains no screenshots, charts or model output.
                slate = out / 'prepared-mode.txt'
                slate.write_text('РЕПЕТИЦИЯ · S14\n\nПросмотр ранее подготовленной гипотезы и задачи.\nНовый ответ модели в этом дубле не запрашивается.\n', encoding='utf-8')
                initial = slate.as_uri()
            pg.goto(initial, wait_until='domcontentloaded', timeout=16000)
            pg.bring_to_front()
            if args.mode=='replay':
                original=pg.locator('.chat-messages .msg-user').first
                original.wait_for(timeout=8000)
                if PROMPT not in original.inner_text():
                    raise RuntimeError('The saved conversation starts with a different question.')
                original.scroll_into_view_if_needed(timeout=5000)
                pg.wait_for_timeout(3300)
            session.run(pg, ctx)
        finally:
            ctx.close()
            if 'video' in locals() and video:
                native = Path(video.path())
            browser.close()
    staged=out/'take.partial.webm'
    if not video:
        raise RuntimeError('Playwright did not attach its native recorder.')
    # The recording was finalized when the context closed. Preserve its original bytes.
    if not native.is_file() or native.stat().st_size<10000:
        raise RuntimeError('No complete native video.')
    os.replace(native,staged)
    os.replace(staged,out/'take.webm')
    (out / 'answer.txt').write_text(session.answer, encoding='utf-8')
    raw = out / 'take.webm'
    mp4 = out / 'take.mp4'
    subprocess.run([ffmpeg, '-hide_banner','-loglevel','error','-nostdin','-n','-i',str(raw),
                    '-map','0:v:0','-an','-c:v','libx264','-preset','veryfast','-crf','18',
                    '-pix_fmt','yuv420p','-movflags','+faststart',str(mp4)], check=True, timeout=110)
    for file in (raw, mp4, out / 'answer.txt'):
        session.r['files'][file.name] = {'sha256':sha(file.read_bytes()), 'bytes':file.stat().st_size}
    session.r['finished_at'] = datetime.now(timezone.utc).isoformat()
    json_write(out / 'receipt.json', session.r)
    print(json.dumps({'status': session.r['status'],'out':str(out)}, ensure_ascii=False), flush=True)
    return 0 if session.r['status'] == 'complete' else 3


if __name__ == '__main__':
    sys.exit(main())
