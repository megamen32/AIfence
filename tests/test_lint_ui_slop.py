"""Guards the UI-slop rule set of scripts/lint-placeholder-names.py (see .agents/skills/design-review)."""
import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('lint', ROOT / 'scripts' / 'lint-placeholder-names.py')
lint = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lint)


def hits(name, body):
    with tempfile.TemporaryDirectory() as d:
        p = Path(d) / name
        p.write_text(body, 'utf-8')
        return [w.lower() for _, w in lint.slop_hits(p)]


class SlopLint(unittest.TestCase):
    def test_banned_phrases_in_templates(self):
        for phrase in ['не подтверждено', 'Не указан', 'не рассчитаны', 'Ориентировочно', 'предположительно',
                       'может быть', 'Нет данных', 'не определён']:
            self.assertTrue(hits('a.templ', f'<p>{phrase}</p>'), phrase)

    def test_go_strings_only(self):
        self.assertTrue(hits('a.go', 'return "не указано"'))
        self.assertFalse(hits('a.go', '// не указано в схеме'))
        self.assertFalse(hits('a.go', 'x := notSpecified // нет данных'.replace('// нет данных', '')))
        self.assertFalse(hits('a_test.go', 'want := "нет данных"'))

    def test_clean_text_passes(self):
        self.assertFalse(hits('a.templ', '<p>Kaspersky Endpoint Security: −12 % к прошлой неделе</p>'))

    def test_repo_has_no_new_slop(self):
        r = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'lint-placeholder-names.py')],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)


if __name__ == '__main__':
    unittest.main()
