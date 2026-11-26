#!/usr/bin/env python3
"""AIfence DEMO: synthetic, recent test events for the isolated counter only.
The official historical dataset remains the source for real release comparisons.
Dry run is the default. Use --send once to upload; the token is entered locally.
"""
import argparse
import collections
import getpass
import hashlib
import json
import random
import time
import urllib.error
import urllib.parse
import urllib.request

COUNTER = '114698578'
BASE = 'https://aifence-demo.example.com'
ENDPOINT = 'https://mc.yandex.ru/collect'


def make_data(run_id, now):
    rng = random.Random(20261126)
    stats = collections.Counter()
    def cid(i):
        return str(10**17 + int(hashlib.sha256(f'aifence-demo:{run_id}:{i}'.encode()).hexdigest()[:15], 16) % (9*10**17))
    def hit(i, ts, path, kind='pageview', **extra):
        return urllib.parse.urlencode(dict(cid=cid(i), t=kind, et=ts, dl=BASE+path, dr='https://www.example.com/', **extra))
    def goal(i, ts, path, event):
        stats[event] += 1
        return hit(i, ts, path, 'event', ea=event)
    def ecom(i, ts, path, action, **extra):
        stats['ecom_'+action] += 1
        return hit(i, ts, path, 'event', pa=action, pr1id='DEMO-ASTRA', pr1nm='DEMO Astra Linux', pr1ca='DEMO / Лицензии ПО', pr1pr=12000, pr1qt=1, cu='RUB', **extra)
    smoke = [hit(0, now-120, '/catalog/astra?utm_source=aifence_demo&utm_medium=synthetic', dt='DEMO smoke test', params=json.dumps({'aifence_demo':{'synthetic':'yes','test':'smoke'}})), hit(0, now-115, '/catalog/astra', 'event', ea='astra_cfg_open')]
    products = ['kaspersky']*400 + ['astra']*300 + ['lk']*200
    rng.shuffle(products)
    batches, batch, size = [], [], 0
    for i, product in enumerate(products, 1):
        ts = now-8*3600 + int(i*(8*3600-600)/len(products))
        device = rng.choices(['desktop','mobile'], [.8,.2])[0]
        source = rng.choice(['direct_demo','search_demo','partner_demo'])
        path = '/lk/home' if product=='lk' else '/catalog/'+product
        params = json.dumps({'aifence_demo':{'synthetic':'yes','product':product,'device':device,'run':run_id}}, ensure_ascii=False, separators=(',',':'))
        v = [hit(i, ts, path+'?utm_source='+source+'&utm_medium=synthetic&utm_campaign=aifence_demo', dt='DEMO: '+product, params=params, sr='1440x900x24' if device=='desktop' else '390x844x24')]
        stats['visits_'+product] += 1
        if product=='kaspersky':
            # 144 / 400 selection visits; the source's observed range is 34–39%.
            if stats['visits_kaspersky'] % 25 in range(1,10):
                v.append(goal(i,ts+12,path,'kes_cfg_option'))
            # No KES cart event: source data explicitly lacks reliable tracking.
        elif product=='astra':
            v.append(ecom(i,ts+4,path,'detail'))
            if rng.random()<.45:
                v.append(goal(i,ts+10,path,'astra_cfg_open'))
                if rng.random()<.20:
                    v.append(goal(i,ts+15,path,'astra_cfg_back'))
                if rng.random()<.65:
                    v.append(goal(i,ts+32,path,'astra_cfg_complete'))
                    if rng.random()<.25:
                        v.append(goal(i,ts+45,path,'astra_cfg_cart'))
                        v.append(ecom(i,ts+46,path,'add'))
                        if rng.random()<.30:
                            v.append(hit(i,ts+55,'/checkout',dt='DEMO: Checkout'))
                            v.append(ecom(i,ts+60,'/checkout','purchase',ti=f'AIFENCE-DEMO-{run_id}-{i}',tr=12000))
        else:
            if rng.random()<.7:
                v.append(goal(i,ts+5,path,'lk_reco_status'))
                if rng.random()<.68:
                    v.append(goal(i,ts+8,path,'lk_reco_shown'))
                    if rng.random()<.75:
                        v.append(goal(i,ts+10,path,'lk_reco_viewed'))
                        if rng.random()<.15:
                            v.append(goal(i,ts+18,path,'lk_reco_click'))
        chunk = sum(len(x.encode('utf-8'))+1 for x in v)
        if batch and size+chunk>240000:
            batches.append(batch); batch=[]; size=0
        batch.extend(v); size+=chunk
    if batch: batches.append(batch)
    return smoke,batches,stats


def send(lines, token):
    url = ENDPOINT+'?'+urllib.parse.urlencode({'tid':COUNTER,'ms':token})
    body='\n'.join(lines).encode('utf-8')
    assert 0 < len(body) < 500000 and not body.endswith(b'\n')
    request=urllib.request.Request(url, data=body, headers={'Content-Type':'text/plain; charset=UTF-8'}, method='POST')
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            text=response.read(500).decode('utf-8','replace').replace(token,'[redacted]')
            return response.status,text
    except urllib.error.HTTPError as exc:
        return exc.code,exc.read(500).decode('utf-8','replace').replace(token,'[redacted]')
    except (OSError, urllib.error.URLError) as exc:
        return 0,f'Unconfirmed delivery: {type(exc).__name__}; do not retry blindly.'


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--send',action='store_true',help='Upload the synthetic sample to the isolated demo counter (run once).')
    p.add_argument('--run-id',default='seed-v1',help='Synthetic run ID, used for unique clients and transactions.')
    args=p.parse_args()
    if not args.run_id or len(args.run_id)>48 or not all(c.isalnum() or c in '-_' for c in args.run_id):
        p.error('run-id must be 1–48 letters, digits, underscores or hyphens')
    smoke,batches,stats=make_data(args.run_id,int(time.time()))
    print('DEMO ONLY | Counter:',COUNTER,'| Run:',args.run_id)
    print('Plan: 900 synthetic visits + one smoke test; recent 8-hour sample, NOT historical replay')
    print(json.dumps(stats,ensure_ascii=False,indent=2))
    print('Main hits:',sum(map(len,batches)), '| Batches:',len(batches), '| Smoke hits:',len(smoke))
    if not args.send:
        print('DRY RUN: nothing sent. Use --send once to upload.')
        return 0
    if input('Type 114698578 to confirm the isolated test counter: ').strip()!=COUNTER:
        print('Cancelled.'); return 1
    token=getpass.getpass('Measurement Protocol token (not echoed): ').strip()
    if not token: print('No token; cancelled.');return 1
    status,body=send(smoke,token)
    print('SMOKE:',status,body,flush=True)
    if status!=200 or 'OK' not in body:
        print('Stopped: main sample not sent.');return 2
    accepted=0
    for idx,batch in enumerate(batches,1):
        status,body=send(batch,token)
        print(f'BATCH {idx}/{len(batches)}: {status}; hits={len(batch)}; {body}',flush=True)
        if status!=200 or 'OK' not in body:
            print('Stopped. Accepted main hits:',accepted,'; failed/uncertain batch may have been processed. Do not blindly rerun.');return 3
        accepted+=len(batch)
    print('All main hits accepted:',accepted,'; check reports after processing. HTTP 200 is not proof of report visibility.')
    return 0

if __name__=='__main__':
    raise SystemExit(main())
