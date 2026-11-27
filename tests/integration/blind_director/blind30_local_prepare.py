#!/usr/bin/env python3
"""Prepare owner-scoped local inputs from the frozen blind manifest, without oracle."""
import hashlib
import json
from pathlib import Path

import blind30_score
import local_sources as s

PACKAGE = s.ROOT / '.runtime/blind-director/blind30-sol-v2'
OUT = PACKAGE / 'local-inputs'


def main():
    _, manifest = blind30_score.verify(PACKAGE)
    OUT.mkdir(mode=0o700, exist_ok=False)
    adapter = s.adapter()
    result = {'schema': 'aifence.local-blind-sources.v1', 'cases': {}}
    for item in manifest['cases']:
        case = item['id']
        run = Path(item['run'])
        folder = run / 'inputs' / case[-7:]
        target = OUT / case
        target.mkdir(mode=0o700)
        series, context = adapter.prepare_inputs(folder, run / 'tools/generator.py')
        batch, context_doc = json.loads(series), json.loads(context)
        docs = [{'schema_version': 'aifence.case.v1', 'as_of': batch['as_of'], 'batches': [part]}
                for part in s.chunk_batch(batch)]
        docs.extend({'schema_version': 'aifence.case.v1', 'as_of': batch['as_of'], 'batches': [part]}
                    for part in context_doc['batches'].values())
        docs.append(s.synthetic_controls(batch['as_of']))
        s.save(target / 'metrika-series.json', series)
        s.save(target / 'metrika-context.json', context)
        output = []
        for i, doc in enumerate(docs):
            raw = s.encoded(doc)
            if len(raw) > 1024 * 1024:
                raise RuntimeError('Document exceeds PM import limit: ' + case)
            name = f'pm-{i:02d}.json'
            s.save(target / name, raw)
            counts = [{'source': b['source'], 'metric': m, 'unit': u,
                       'count': sum(1 for r in b['observations'] if r.get('metric_key') == m and r.get('unit') == u)}
                      for b in doc['batches'] for m, u in sorted({(r.get('metric_key'), r.get('unit')) for r in b['observations']})]
            output.append({'file': name, 'sha256': hashlib.sha256(raw).hexdigest(), 'counts': counts})
        result['cases'][case] = {'inputs_sha256': item['input_hashes'], 'documents': output,
                                 'provenance': 'frozen synthetic input plus independently fixed synthetic controls'}
    s.save(OUT / 'manifest.json', s.encoded(result))
    s.save(OUT / 'provenance.json', s.encoded({'controls': 'constant synthetic controls; not real observations', 'synthetic': True}))
    print('Prepared', len(result['cases']), 'isolated input sets without oracle access', flush=True)


if __name__ == '__main__':
    main()
