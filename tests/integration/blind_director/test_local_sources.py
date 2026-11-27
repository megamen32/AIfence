import importlib.util
import json
import sys
import tempfile
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class LocalSourceTests(unittest.TestCase):
    def test_controls_are_marked_synthetic_and_comparable(self):
        module = load('local_sources')
        doc = module.synthetic_controls('2026-07-19T23:59:59Z')
        self.assertEqual([len(x['observations']) for x in doc['batches']], [2, 2, 2])
        for batch in doc['batches']:
            for row in batch['observations']:
                self.assertTrue(row['details']['synthetic'])
                self.assertEqual(row['details']['origin'], 'synthetic_fixture')
                self.assertTrue(row['source_ref']['locator'].startswith('synthetic-neutral/'))
        prices = doc['batches'][2]['observations']
        self.assertEqual({r['product_key'] for r in prices}, {module.SKU})
        self.assertEqual({r['details']['quantity'] for r in prices}, {1})
        self.assertEqual({r['unit'] for r in prices}, {'RUB'})

    def test_chunking_preserves_rows(self):
        module = load('local_sources')
        rows = [{'id': str(i), 'value': 'x' * 3000} for i in range(500)]
        batches = module.chunk_batch({'schema_version':'aifence.v1','observations':rows})
        self.assertGreater(len(batches), 1)
        self.assertEqual([r for b in batches for r in b['observations']], rows)

    def test_remote_origin_is_rejected_before_any_request(self):
        module = load('local_direct')
        with self.assertRaises(RuntimeError):
            module.assert_local('http://localhost:42081')

    def test_numeric_specialist_requires_finite_value_locator_and_discovery(self):
        module = load('local_chat')
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'receipt.json'
            row={'value':12.0,'unit':'count','period':{'from':'2026-01-01'},'source_ref':{'locator':'row'}}
            p.write_text(json.dumps({'output':{'status':'available','snapshot_id':'visible','observations':[row]}}))
            self.assertTrue(module.numeric_read(p, {'visible'}))
            self.assertFalse(module.numeric_read(p, {'other'}))
            row['value']=float('nan')
            p.write_text(json.dumps({'output':{'status':'available','snapshot_id':'visible','observations':[row]}}))
            self.assertFalse(module.numeric_read(p, {'visible'}))
            row['value']=12;row['source_ref']={}
            p.write_text(json.dumps({'output':{'status':'available','snapshot_id':'visible','observations':[row]}}))
            self.assertFalse(module.numeric_read(p, {'visible'}))

    def test_missing_score_blocks_before_admission(self):
        module = load('local_chat')
        with tempfile.TemporaryDirectory() as d:
            with self.assertRaisesRegex(RuntimeError, 'KT1'):
                module.kt1(Path(d)/'missing-matrix', Path(d)/'missing-score')

if __name__ == '__main__':
    unittest.main()
