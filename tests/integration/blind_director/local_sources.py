#!/usr/bin/env python3
"""Prepare synthetic blind inputs for the isolated Mac Airlock; never reads oracle."""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
OLD = ROOT / '.runtime/blind-director/run-2291b25d10ad531be967bf03'
NEW = Path('/Users/user/AIfence/.runtime/kes-handoff/multisource-fixed-question-20261127/inputs')
OUT = ROOT / '.runtime/blind-director/local-source-package'
CASES = [f'case-{i:02d}' for i in range(1, 12)]
PRODUCT = 'kaspersky_small_office_security'
SKU = 'KL4141RCEFR'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encoded(data):
    return (json.dumps(data, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode()


def save(path, data):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'wb') as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())


def adapter():
    sys.path.insert(0, str(HERE))
    spec = importlib.util.spec_from_file_location('blind_adapter', HERE / 'live_adapter.py')
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def synthetic_controls(as_of):
    """Constant synthetic controls; no values are inferred from the hidden cases."""
    last = dt.datetime.fromisoformat(as_of.replace('Z', '+00:00')).date()
    monday = last - dt.timedelta(days=last.weekday()) - dt.timedelta(days=14)
    batches = []
    for source, metric, unit, value in [('wordstat', 'search_queries', 'count', 2500), ('google_trends', 'search_interest', 'index', 50)]:
        rows = []
        for i in range(2):
            start = monday + dt.timedelta(weeks=i)
            end = start + dt.timedelta(days=6)
            rows.append({'id':f'neutral/{source}/{start}', 'kind':'metric', 'product_key':PRODUCT,
                         'metric_key':metric, 'value':value, 'unit':unit, 'period':{'from':str(start),'to':str(end)},
                         'status':'observed', 'source_ref':{'source_id':source, 'locator':f'synthetic-neutral/{source}/{start}'},
                         'details':{'origin':'synthetic_fixture','synthetic':True,'geo':'RU','sku':SKU,
                                    'product_label':'Kaspersky Small Office Security','normalization':'constant_synthetic_control'}})
        batches.append({'schema_version':'aifence.v1','source':'research','status':'ok','as_of':as_of,'warnings':[], 'observations':rows})
    prices = []
    for i in range(2):
        date = monday + dt.timedelta(weeks=i, days=6)
        prices.append({'id':f'neutral/market/{SKU}/{date}','kind':'offer','product_key':SKU,'metric_key':'displayed_price',
                       'value':4900,'unit':'RUB','currency':'RUB','status':'observed', 'period':{'from':str(date),'to':str(date)},
                       'source_ref':{'source_id':'synthetic-neutral-market','locator':f'synthetic-neutral/market/{SKU}/{date}'},
                       'details':{'origin':'synthetic_fixture','synthetic':True,'sku':SKU,'name':'Kaspersky Small Office Security',
                                  'seller':'synthetic-neutral-market','quantity':1,'max_quantity':1,'price_basis':'per_unit',
                                  'captured_at':f'{date}T12:00:00Z','market_comparison':{'comparable':True,'group':f'{SKU}/{date}/one'}}})
    batches.append({'schema_version':'aifence.v1','source':'market','status':'ok','as_of':as_of,'warnings':[], 'observations':prices})
    return {'schema_version':'aifence.case.v1','as_of':as_of,'batches':batches}


def chunk_batch(batch):
    template = {k:v for k,v in batch.items() if k != 'observations'}
    chunks, rows, size = [], [], 0
    for row in batch['observations']:
        n = len(encoded(row)) + 256
        if rows and size + n > 650000:
            chunks.append({**template,'observations':rows}); rows, size = [], 0
        rows.append(row); size += n
    if rows: chunks.append({**template,'observations':rows})
    return chunks


def prepare():
    if any(OUT.iterdir()):
        raise RuntimeError('Output directory is not empty; preserve previous package')
    a = adapter()
    result = {'schema':'aifence.local-blind-sources.v1','cases':{}}
    for case in CASES:
        folder = (OLD / 'inputs' / case) if case in CASES[:6] else NEW / case
        inputs = {p.name:digest(p.read_bytes()) for p in sorted(folder.iterdir()) if p.is_file()}
        target = OUT / case; target.mkdir(mode=0o700)
        docs = []
        if case in CASES[:6]:
            series, context = a.prepare_inputs(folder, OLD / 'tools/generator.py')
            series_batch, context_doc = json.loads(series), json.loads(context)
            for batch in chunk_batch(series_batch):
                docs.append({'schema_version':'aifence.case.v1','as_of':series_batch['as_of'],'batches':[batch]})
            for batch in context_doc['batches'].values():
                docs.append({'schema_version':'aifence.case.v1','as_of':series_batch['as_of'],'batches':[batch]})
            docs.append(synthetic_controls(series_batch['as_of']))
            save(target/'metrika-series.json', series)
            save(target/'metrika-context.json', context)
        else:
            docs.append(json.loads((folder/'snapshot.json').read_text()))
        output = []
        for i, doc in enumerate(docs):
            raw = encoded(doc)
            if len(raw) > 1024*1024:
                raise RuntimeError('Document exceeds PM import limit')
            name = f'pm-{i:02d}.json'; save(target/name, raw)
            counts = [{'source':b['source'],'metric':m,'unit':u,'count':sum(1 for r in b['observations'] if r.get('metric_key') == m and r.get('unit') == u)}
                      for b in doc['batches'] for m,u in sorted({(r.get('metric_key'),r.get('unit')) for r in b['observations']})]
            output.append({'file':name,'sha256':digest(raw),'counts':counts})
        result['cases'][case] = {'inputs_sha256':inputs,'documents':output,
            'provenance':'original synthetic input plus independent constant synthetic controls' if case in CASES[:6] else 'original synthetic multisource snapshot',
            'native_bindings':'not established', 'hosted_readback':'not yet performed'}
    save(OUT/'manifest.json', encoded(result))
    save(OUT/'provenance.json', encoded({'control_method':'Fixed values, two complete weeks, same public SKU and quantity; no case answers used.',
        'wordstat_count':2500,'google_trends_index':50,'market_rub_per_unit':4900,'synthetic':True,
        'limits':'Synthetic data: not real measurements, prices, search demand, or evidence of a native binding.'}))
    print('Prepared 11 private input packages without oracle access')


def main():
    p = argparse.ArgumentParser()
    p.add_argument('action', choices=['prepare'])
    args = p.parse_args()
    if args.action == 'prepare': prepare()

if __name__ == '__main__':
    main()
