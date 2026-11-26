#!/usr/bin/env python3
"""Read existing worker reports; publish sanitized snapshot via authenticated SDK CLI."""
import argparse, collections, csv, datetime, json, pathlib, plistlib, subprocess, sys, time
ROOT=pathlib.Path(__file__).resolve().parents[2]
APP=pathlib.Path(__file__).resolve().parent
CAT=ROOT/'.runtime/market-catalog'
def read(p):
    return json.loads(p.read_text())
def iso(t): return datetime.datetime.fromtimestamp(t,datetime.timezone.utc).isoformat().replace('+00:00','Z')
def cov(label,done,total,known=True): return dict(label=label,done=int(done),total=int(total),known=bool(known))
def get_worker(name):
    labels={'softline':'local.aifence.marketwatch.softline','softmap':'local.aifence.market-catalog-softmap','softmagazin':'local.aifence.market-catalog-softmagazin'}
    label=labels.get(name)
    p=pathlib.Path.home()/'Library/LaunchAgents'/f'{label}.plist'
    interval=0
    if p.is_file(): interval=int(plistlib.loads(p.read_bytes()).get('StartInterval',0))
    try:
        result=subprocess.run(['launchctl','list',label],capture_output=True,text=True,timeout=5)
        return result.returncode==0, result.returncode==0 and '"PID" =' in result.stdout, interval
    except (OSError,subprocess.TimeoutExpired): return False,False,interval
def build():
    cfg=read(ROOT/'apps/market-watch/collector/sources.json')
    ss=[]
    for c in cfg:
        id=c['id'];p=CAT/id/('inventory.json' if id=='softline' else 'progress.json')
        if not p.exists(): continue
        x=read(p);o,r,interval=get_worker(id)
        cs=[];states={};note='';missing=None
        if id=='softline':
            cs=[cov('Семейства',x['families_processed'],x['families']),cov('Лицензии по sitemap',x['inventory_accounted'],x['inventory_products'])]
            states=x.get('states_by_inventory_url',{}); updated=x['as_of'];status='complete' if x.get('complete') else 'running';missing=x.get('priced_offers_without_sku')
            note='Семейства и отдельные лицензии учитываются раздельно. Артикул может отсутствовать.'
        else:
            cs=[cov('Страницы семейств',x['processed_pages'],x['candidate_pages'])]
            if x.get('expected_variant_urls',0)>0:
                total=x['expected_variant_urls']; pending=x.get('pending_variant_urls',total); cs.append(cov('URL лицензий учтено',total-pending,total))
            elif id=='softmagazin':
                cs.append(cov('Обнаружено вариантов',x.get('variants',0),0,False))
                note='Полный объём лицензий неизвестен; обход программных разделов ещё проверяется.' if not x.get('category_discovery_complete') else 'Полный объём отдельных лицензий неизвестен.'
            states=x.get('page_states',{});updated=x['updated_at'];status=x.get('state','unknown')
        if o and not r and status=='running':status='stopped'
        rules=[]
        for k,v in c.get('rule',{}).items():
            if isinstance(v,dict): rules.append(dict(name=k,selector=v.get('selector',''),attribute=v.get('attr','')))
            elif isinstance(v,str):rules.append(dict(name=k,selector=v))
        item=dict(id=id,name={'softline':'Softline','softmap':'SoftMap','softmagazin':'SoftMagazin'}.get(id,id),host=c['host'],sitemap=c.get('sitemap',''),mode='Sitemap + HTML',status=status,updated_at=updated,inventory_at=x.get('inventory_at',''),offers=x['priced_offers'],coverage=cs,states=[dict(name=k,count=v) for k,v in sorted(states.items())],fields=rules,page_pattern=c.get('page_include',''),seeds=c.get('seeds',[]),note=note,worker_observed=o,worker_running=r,interval_seconds=interval)
        if missing is not None:item['missing_sku']=missing
        ss.append(item)
    file=CAT/'offers.csv'; rows=0;at=''
    if file.exists():
        with file.open(encoding='utf-8-sig',newline='') as f:
            at=iso(__import__('os').fstat(f.fileno()).st_mtime); rd=csv.DictReader(f,delimiter=';')
            byid={x['id']:x for x in ss}
            for row in rd:
                rows+=1
                item=byid.get(row.get('source'))
                if item and 'sample_fields' not in item:
                    item['sample_url']=row.get('url','')
                    item['sample_fields']={'Название':row.get('name','')[:500],'Артикул':row.get('sku') or 'Не указан','Цена':row.get('price','')+' '+row.get('currency',''),'Количество':row.get('quantity') or 'Не указано','Снимок':row.get('captured_at','')}
    d=dict(generated_at=iso(time.time()),sources=ss,export_rows=rows,export_at=at)
    p=ROOT/'.runtime/baserow/import-progress.json'
    if p.exists():
        b=read(p)
        if isinstance(b.get('confirmed'),int):d.update(baserow_imported=b['confirmed'],baserow_at=iso(b['updated_at']))
    return d
def main():
    parser=argparse.ArgumentParser();parser.add_argument('--dry-run',action='store_true');args=parser.parse_args()
    data=build()
    if args.dry_run: print(json.dumps(data,ensure_ascii=False));return
    air=ROOT/'.runtime/bin/market-air'
    cmd=[str(air),'request','POST','/admin/snapshot','--header','Content-Type: application/json','--data',json.dumps(data,ensure_ascii=False,separators=(',',':')),'--timeout','20s']
    subprocess.run(cmd,cwd=APP,check=True,timeout=30)
if __name__=='__main__':main()
