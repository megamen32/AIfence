import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE))

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module


class BlindPackageTest(unittest.TestCase):
    def test_input_only_preparation_and_all_record_gate(self):
        prep=load('blind30_prepare');score=load('blind30_score')
        with tempfile.TemporaryDirectory() as directory:
            private=Path(directory);prep.PRIVATE=private;score.PRIVATE=private;runs=[]
            for batch in range(5):
                run=private/f'run-{batch}';runs.append(run);(run/'tools').mkdir(parents=True)
                (run/'tools/scorer.py').write_text('# synthetic parser\n')
                hashes={};cases=[]
                for i in range(1,7):
                    case=f'case-{i:02d}';cases.append(case);folder=run/'inputs'/case;folder.mkdir(parents=True)
                    data=json.dumps({'batch':batch,'case':case}).encode();(folder/'metrika.json').write_bytes(data)
                    hashes[case]={'metrika.json':hashlib.sha256(data).hexdigest()}
                (run/'run.json').write_text(json.dumps({'cases':cases,'input_sha256':hashes}))
            package=private/'blind-package';argv=['blind30_prepare','--runs',*(str(x) for x in runs),'--output',str(package)]
            # No vault exists in this test: preparation and partial-freeze checks must not touch it.
            with patch.object(sys,'argv',argv),contextlib.redirect_stdout(io.StringIO()):prep.main()
            manifest=json.loads((package/'manifest.json').read_text());self.assertEqual(len(manifest['cases']),30)
            case=manifest['cases'][0]['id'];record={'case':case,'question':manifest['question'],'chat_id':'chat','run_id':'run','owner_id':'owner','status':'completed','response':'answer','duration_seconds':1,'source_receipts':{}}
            path=private/'raw.json';path.write_text(json.dumps(record));score.freeze(package,case,path)
            with self.assertRaises(FileExistsError):score.freeze(package,case,path)
            with self.assertRaisesRegex(RuntimeError,'All 30 records'):score.score(package)
            self.assertFalse((package/'normalized.json').exists())

if __name__=='__main__':unittest.main()
