#!/usr/bin/env python3
"""Fail when fixtures, generator outputs or UI strings use placeholder product names.

Owner rule (AGENTS.md): demo and synthetic data use real catalog products
(Kaspersky Small Office Security, Dr.Web, Astra Linux, MyOffice ...).
Synthetic origin is marked by a hidden flag or path, never by the visible name.

Usage:
  python3 scripts/lint-placeholder-names.py              # repo files (tracked + untracked, not ignored)
  python3 scripts/lint-placeholder-names.py PATH ...     # also scan extra dirs/files, e.g. .runtime/blind-director/run-*/inputs
Exit code 1 on any hit.
"""
import re, subprocess, sys, zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DATA_EXT = {'.json', '.jsonl', '.csv', '.tsv', '.yaml', '.yml', '.xlsx'}
UI_EXT = {'.templ', '.html', '.js', '.go'}
SKIP = ('AGENTS.md', '.agents/', 'scripts/lint-placeholder-names.py')

ALWAYS = [
    r'\bdemo_(?:target|control|product)\b',
    r'\bproduct_[a-z0-9]\b',
    r'\b(?:Product|Продукт|Товар) [A-ZА-Я]\b',
    r'[Уу]чебн\w*',
    r'[Тт]естов(?:ый|ая|ое|ые|ого|ой|ую)\s+(?:продукт|товар|магазин|пример|набор|сценари\w*|задач\w*)',
]
DATA_ONLY = [r'"(?:foo|bar|baz|qux)\w*"']

ALWAYS_RE = re.compile('|'.join(ALWAYS))
DATA_RE = re.compile('|'.join(ALWAYS + DATA_ONLY))


def repo_files():
    out = subprocess.run(['git', '-C', str(ROOT), 'ls-files', '-z', '--cached', '--others', '--exclude-standard'],
                         capture_output=True, check=True).stdout.decode().split('\0')
    return [ROOT / p for p in out if p and not p.startswith(SKIP)]


def text_of(path):
    if path.suffix == '.xlsx':
        with zipfile.ZipFile(path) as z:
            return '\n'.join(z.read(n).decode('utf-8', 'replace') for n in z.namelist()
                             if n.startswith('xl/') and n.endswith('.xml'))
    return path.read_text('utf-8', 'replace')


def scan(path):
    ext = path.suffix.lower()
    if path.name.endswith('_test.go') or (ext not in DATA_EXT and ext not in UI_EXT):
        return []
    rx = DATA_RE if ext in DATA_EXT else ALWAYS_RE
    hits = []
    try:
        text = text_of(path)
    except (OSError, zipfile.BadZipFile):
        return []
    for n, line in enumerate(text.splitlines(), 1):
        for m in rx.finditer(line):
            hits.append((n, m.group(0)))
    return hits


def expand(args):
    for a in args:
        p = Path(a)
        yield from (q for q in p.rglob('*') if q.is_file()) if p.is_dir() else [p]


def main(argv):
    total = 0
    for path in [*repo_files(), *expand(argv)]:
        if not path.is_file():
            continue
        for n, word in scan(path):
            total += 1
            rel = path.relative_to(ROOT) if path.is_relative_to(ROOT) else path
            print(f'{rel}:{n}: placeholder name {word!r}; use a real catalog product')
    if total:
        print(f'\n{total} placeholder name(s). Synthetic data must look like real products; mark synthetic origin by a hidden flag or path.', file=sys.stderr)
        return 1
    print('No placeholder product names found.')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
