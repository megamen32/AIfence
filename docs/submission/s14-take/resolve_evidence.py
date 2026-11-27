#!/usr/bin/env python3
"""Find the current case's latest weekly checkout/cart rows and verify their native browser views."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import date
from html.parser import HTMLParser
import json
from pathlib import Path
from urllib.parse import urlencode
from urllib.request import Request

from prepare_case import ALLOWED_ORIGIN, Stand, digest, read, save


class VisibleText(HTMLParser):
    def __init__(self):
        super().__init__()
        self.hidden = 0
        self.segments = []

    def handle_starttag(self, tag, attrs):
        if tag in ('script', 'style'):
            self.hidden += 1

    def handle_endtag(self, tag):
        if tag in ('script', 'style'):
            self.hidden -= 1

    def handle_data(self, data):
        if not self.hidden and data.strip():
            self.segments.append(' '.join(data.split()))


def latest_rows(source, product_key):
    requested = {('checkouts', 'desktop'), ('checkouts', 'mobile'), ('cart_additions', 'desktop'), ('cart_additions', 'mobile')}
    rows = [r for r in source['observations'] if r.get('product_key') == product_key and (r.get('metric_key'), r.get('segment')) in requested and r['status'] == 'observed' and r.get('unit') == 'count']
    latest = max(r['period']['to'] for r in rows)
    rows = [r for r in rows if r['period']['to'] == latest]
    if len(rows) != len(requested) or {(r['metric_key'], r['segment']) for r in rows} != requested or len({r['period']['from'] for r in rows}) != 1:
        raise RuntimeError('Latest source week requires one complete pair of device rows per metric')
    return {r['id']: r for r in rows}


def resolve(profile, inputs, product):
    receipt, owner = read(profile / 'receipt.json'), read(profile / 'access.json')
    if receipt['origin'] != ALLOWED_ORIGIN or owner['origin'] != receipt['origin'] or owner['user_id'] != receipt['owner_id']:
        raise RuntimeError('Origin or owner receipt mismatch')
    source = (inputs / 'metrika.json').read_bytes()
    if digest(source) != receipt['raw_inputs']['metrika.json']:
        raise RuntimeError('Source does not match the staged frozen input')
    raw = latest_rows(json.loads(source), product)
    api = Stand(receipt['origin'])
    token = api.request('POST', '/auth/login', payload={'email': owner['email'], 'password': owner['password']})['accessToken']
    me = api.request('GET', '/api/v1/me', token)
    if me['user']['id'] != owner['user_id'] or me.get('fullAdministrator') or me.get('full_administrator'):
        raise RuntimeError('Expected the ordinary case owner')
    pm, met = receipt['apps']['product-manager'], receipt['apps']['metrika-analyst']
    if api.tool(token, met, 'discover_watchdog_series')['dataset_id'] != receipt['dataset_id']:
        raise RuntimeError('Active Metrika source no longer matches the verified dataset')
    selected = receipt['pm_snapshots']
    visible = {s['snapshot_id'] for s in api.tool(token, pm, 'list_evidence')['snapshots']}
    if not set(selected) <= visible:
        raise RuntimeError('The owner cannot see all staged source snapshots')

    def read_snapshot(snapshot):
        native = api.tool(token, pm, 'read_evidence', {'snapshot_id': snapshot, 'source': 'metrika', 'product_key': product, 'ids': list(raw), 'limit': 16})
        if native.get('snapshot_id') != snapshot:
            raise RuntimeError('Native read returned a different snapshot')
        return [(snapshot, row) for row in native['rows']]

    with ThreadPoolExecutor(max_workers=3) as pool:
        found = [pair for group in pool.map(read_snapshot, selected) for pair in group]
    if len(found) != len(raw) or {r['id'] for s, r in found} != set(raw):
        raise RuntimeError('Source rows are missing or duplicated in the native owner snapshots')
    route = f'/api/v1/agents/{pm}/deployments/production/http/'
    app_origin = api.origin.replace('localhost', 'product-manager.localhost')
    rows = []
    for snapshot, row in found:
        original = raw[row['id']]
        if (row['source'] != 'metrika' or row['product'] != product or row['metric'] != original['metric_key']
                or row['segment'] != original['segment'] or row['period'] != original['period']
                or row['source_ref']['locator'] != original['source_ref']['locator'] or row['unit'] != original['unit']
                or float(row['value']) != original['value'] or not float(row['value']).is_integer()):
            raise RuntimeError('Native row differs from the original observation')
        query = '?' + urlencode({'snapshot': snapshot, 'obs': row['id']})
        url = app_origin + '/' + query
        req = Request(api.origin + route + query, headers={'Authorization': 'Bearer ' + token, 'Accept': 'text/html'})
        with api.opener.open(req, timeout=25) as response:
            if response.status != 200 or 'text/html' not in response.headers.get('Content-Type', ''):
                raise RuntimeError('Native source page did not render')
            parser = VisibleText()
            parser.feed(response.read().decode())
        html = ' '.join(parser.segments)
        values = [original['details']['product_label'], row['metric'], f"{row['value']} {row['unit']}", row['segment'], row['source_ref']['locator']]
        values += [date.fromisoformat(x).strftime('%d.%m.%Y') for x in row['period'].values()]
        if not all(text in html for text in values):
            raise RuntimeError('Rendered source page does not visibly contain the verified observation')
        rows.append({'metric': row['metric'], 'device': row['segment'], 'value': int(float(row['value'])), 'unit': row['unit'], 'period': row['period'], 'source_locator': row['source_ref']['locator'], 'snapshot_id': snapshot, 'observation_id': row['id'], 'url': url})
    rows.sort(key=lambda r: (r['metric'] != 'checkouts', r['device'] != 'desktop'))
    return {'owner_id': owner['user_id'], 'dataset_id': receipt['dataset_id'], 'product_key': product, 'period': rows[0]['period'], 'evidence_url': rows[0]['url'], 'rows': rows, 'total_checkouts': sum(r['value'] for r in rows if r['metric'] == 'checkouts'), 'total_cart_additions': sum(r['value'] for r in rows if r['metric'] == 'cart_additions')}


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--profile-dir', type=Path, required=True)
    ap.add_argument('--inputs', type=Path, default=Path('/Users/user/AIfence-worktrees/blind-catalog-verification/.runtime/blind-director/run-a1a920496bbd616bf084c4d0/inputs/case-06'))
    ap.add_argument('--product', default='kaspersky_small_office_security')
    args = ap.parse_args()
    result = resolve(args.profile_dir, args.inputs, args.product)
    target = args.profile_dir / 'evidence_urls.json'
    save(target, result)
    print(target, result['total_checkouts'], result['total_cart_additions'])


if __name__ == '__main__':
    main()
