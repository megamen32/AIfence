#!/usr/bin/env python3
"""Offline audit of generated inputs; never reads oracle or sends requests."""
import argparse
import collections
import csv
import importlib.util
import json
from pathlib import Path
import re
import zipfile
from xml.etree import ElementTree as ET

ROOT=Path(__file__).resolve().parents[3]
CATALOG=ROOT/'.runtime/market-catalog/verified-app-offers.csv'
FAKE=re.compile(r'\b(?:atlas|lumen|demo[_-](?:target|control)|product\s+[a-z]|продукт\s+[абв])\b',re.I)
HINT=re.compile(r'accepted decision|allocation.*(?:no_alert|insufficient_data)|traffic mix and cart undercount mask|additional cart-event undercount overlaps a real checkout regression|purchase events may arrive after|checkout_regression_pct',re.I)


def generator(path):
    spec=importlib.util.spec_from_file_location('blind_generator_audit',path)
    mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod)
    return mod


def verify_products(products):
    labels={k:str(v['label']) for k,v in products.items()}
    matches={key:None for key in labels}
    with CATALOG.open(encoding='utf-8-sig',newline='') as f:
        for row in csv.DictReader(f,delimiter=';'):
            name=row.get('name','').casefold()
            for key,label in labels.items():
                if (matches[key] is None and label.casefold() in name
                    and str(products[key].get('sku','')).casefold() == row.get('sku','').strip().casefold()
                    and str(products[key].get('catalog_seller','')).casefold() == row.get('source','').strip().casefold()):
                    matches[key]={'source':row.get('source',''),'url':row.get('url','')}
    return matches


def walk(value):
    if isinstance(value,dict):
        for k,v in value.items():
            yield str(k)
            yield from walk(v)
    elif isinstance(value,list):
        for v in value: yield from walk(v)
    elif isinstance(value,str): yield value


def xlsx_text(path):
    texts=[];sheets=0
    with zipfile.ZipFile(path) as z:
        if z.testzip() is not None: raise ValueError('damaged XLSX')
        for name in z.namelist():
            if name.startswith('xl/worksheets/sheet') and name.endswith('.xml'):
                sheets+=1
            if name.startswith('xl/') and name.endswith('.xml'):
                node=ET.fromstring(z.read(name))
                texts.extend(t for t in node.itertext() if t)
    if sheets < 1: raise ValueError('XLSX has no sheets')
    return texts,sheets


def lint(folder, products):
    issues=collections.Counter(); checks=collections.Counter()
    required={'manifest.json','metrika.json','releases.json','backlog.json','Продажи.xlsx'}
    if {x.name for x in folder.iterdir()} != required: issues['unexpected_input_files']+=1
    allowed={str(x).lower() for x in products}
    # A migrated catalog key is the product's own verified SKU.
    aliases={str(v.get('sku','')).lower():str(k).lower() for k,v in products.items() if v.get('sku')}
    for name in ('manifest.json','metrika.json','releases.json','backlog.json'):
        obj=json.loads((folder/name).read_text(encoding='utf-8'))
        strings=list(walk(obj));checks['json_strings']+=len(strings)
        issues['unverified_placeholder_strings']+=sum(bool(FAKE.search(x)) for x in strings)
        issues['answer_leak_strings']+=sum(bool(HINT.search(x)) for x in strings)
        if name=='manifest.json':
            issues['seed_hash_exposed']+=int('seed_hash' in obj)
            issues['unknown_manifest_products']+=len({str(x).lower() for x in obj.get('families',[])}-allowed)
        if name in ('releases.json','backlog.json'):
            for row in obj:
                checks['json_context_rows']+=1
                product=products.get(str(row.get('family','')).lower())
                if product is None or any(row.get(k)!=product.get(v) for k,v in [('product_label','label'),('sku','sku'),('catalog_seller','catalog_seller')]):
                    issues['unverified_json_context_product']+=1
        if name=='metrika.json':
            for row in obj.get('observations',[]):
                checks['observations']+=1
                key=str(row.get('product_key','')).lower()
                canonical=str(row.get('details',{}).get('canonical_product_key','')).lower()
                if key not in allowed:
                    if canonical in allowed or key in aliases:
                        canonical=canonical if canonical in allowed else aliases[key]; checks['synthetic_renamed_keys']+=1
                    else: issues['unverified_product_keys']+=1
                else: canonical=key
                if canonical in products:
                    detail=row.get('details',{}); expected=products[canonical]
                    if any(detail.get(k)!=expected.get(v) for k,v in [('product_label','label'),('sku','sku'),('catalog_seller','catalog_seller')]):
                        issues['unverified_observation_product']+=1
                if str(row.get('source_ref',{}).get('revision','')).startswith('seed:'): issues['seed_exposed']+=1
                if row.get('status') not in ('observed','missing','incomparable','estimated'): issues['unsupported_evidence_status']+=1
    import sys
    sys.path.insert(0,str(ROOT/'apps/product-manager/research'))
    from xlsx import Workbook
    book=Workbook(folder/'Продажи.xlsx')
    try:
        rows=book.rows('Продажи'); header=rows.pop(1)
        valid_labels={v['label'] for v in products.values()}
        for data in rows.values():
            row={header[col]:v for col,v in data.items() if col in header}
            checks['xlsx_product_rows']+=1
            product=products.get(str(row.get('product_key','')).lower())
            if product is None or row.get('product_family') != product['label'] or row.get('sku') != product['sku']:
                issues['unverified_xlsx_product']+=1
        for sheet in ('Релизы', 'Бэклог_задачи'):
            rows=book.rows(sheet); header=rows.pop(1)
            for data in rows.values():
                row={header[col]:v for col,v in data.items() if col in header}
                checks['xlsx_context_rows']+=1
                product=products.get(str(row.get('family','')).lower())
                if product is None or any(row.get(k)!=product.get(v) for k,v in [('product_label','label'),('sku','sku'),('catalog_seller','catalog_seller')]):
                    issues['unverified_xlsx_context_product']+=1
    finally: book.close()
    texts,n=xlsx_text(folder/'Продажи.xlsx');checks['xlsx_xml_text_nodes']+=len(texts);checks['xlsx_sheets']+=n
    issues['unverified_placeholder_strings']+=sum(bool(FAKE.search(x)) for x in texts)
    issues['answer_leak_strings']+=sum(bool(HINT.search(x)) for x in texts)
    return issues,checks


def main():
    p=argparse.ArgumentParser();p.add_argument('--inputs',type=Path,required=True);p.add_argument('--generator',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
    products=generator(a.generator).PRODUCTS;matches=verify_products(products)
    sets=sorted(x for x in a.inputs.iterdir() if x.is_dir())
    if not sets:raise SystemExit('No generated input directories')
    issues=collections.Counter();checks=collections.Counter()
    for item in sets:
        found,tested=lint(item,products);issues.update(found);checks.update(tested)
    issues['unverified_catalog_labels']+=sum(v is None for v in matches.values())
    report={'inputs':len(sets),'catalog_families':len(products),'catalog_labels_matched':sum(v is not None for v in matches.values()),
            'checked':dict(checks),'issues':{k:v for k,v in sorted(issues.items()) if v},'safe_for_blind':not any(issues.values())}
    a.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print(json.dumps(report,ensure_ascii=False))
    return 0 if report['safe_for_blind'] else 1

if __name__=='__main__':raise SystemExit(main())
