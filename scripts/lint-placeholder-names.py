#!/usr/bin/env python3
"""Fail when fixtures, generator outputs or UI strings use placeholder product names.

Owner rule (AGENTS.md): demo and synthetic data use real catalog products
(Kaspersky Small Office Security, Dr.Web, Astra Linux, MyOffice ...).
Synthetic origin is marked by a hidden flag or path, never by the visible name.

Usage:
  python3 scripts/lint-placeholder-names.py              # repo files (tracked + untracked, not ignored)
  python3 scripts/lint-placeholder-names.py PATH ...     # also scan extra dirs/files, e.g. .runtime/blind-director/run-*/inputs
  python3 scripts/lint-placeholder-names.py --strict     # ignore the slop baseline, list every hit
Exit code 1 on any hit.

Second rule set (UI slop, .agents/skills/design-review/SKILL.md): hedge phrases and empty
labels («не подтверждено», «не указан», «не рассчитаны», «ориентировочно», «предположительно»,
«может быть», «нет данных») are banned in UI templates and user-visible Go strings. Existing hits
are frozen per file in scripts/lint-ui-slop-baseline.json; a file may only go down, never up.
After fixing a file run with --update-baseline to lower its count.
"""
import json, re, subprocess, sys, zipfile
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

SLOP = [
    r'не\s+подтвержд\w*',
    r'не\s+указан\w*',
    r'не\s+рассчитан\w*',
    r'не\s+определ[её]н\w*',
    r'ориентировочн\w*',
    r'предположительн\w*',
    r'может\s+быть',
    r'нет\s+данных',
    r'ожидается\s+снимок',
]
SLOP_RE = re.compile('|'.join(SLOP), re.I)
SLOP_EXT = {'.templ', '.html', '.js', '.go'}
BASELINE = ROOT / 'scripts' / 'lint-ui-slop-baseline.json'
COMMENT = re.compile(r'^\s*(//|\*|/\*|<!--|\{\{/\*)')

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


def slop_hits(path):
    """Hedge phrases in UI files; in .go only lines that hold a string literal, no comments."""
    ext = path.suffix.lower()
    if ext not in SLOP_EXT or path.name.endswith('_test.go'):
        return []
    try:
        text = path.read_text('utf-8', 'replace')
    except OSError:
        return []
    hits = []
    for n, line in enumerate(text.splitlines(), 1):
        if COMMENT.match(line) or (ext == '.go' and '"' not in line and '`' not in line):
            continue
        for m in SLOP_RE.finditer(line):
            hits.append((n, m.group(0)))
    return hits


def rel_of(path):
    return str(path.relative_to(ROOT)) if path.is_relative_to(ROOT) else str(path)


def check_slop(files, strict, update):
    counts, found = {}, {}
    for path in files:
        if path.is_file() and (h := slop_hits(path)):
            counts[rel_of(path)] = len(h)
            found[rel_of(path)] = h
    if update:
        BASELINE.write_text(json.dumps(dict(sorted(counts.items())), ensure_ascii=False, indent=1) + '\n')
        print(f'slop baseline written: {sum(counts.values())} hit(s) in {len(counts)} file(s)')
        return 0
    base = {} if strict else (json.loads(BASELINE.read_text()) if BASELINE.exists() else {})
    bad = 0
    for rel, h in found.items():
        if len(h) > base.get(rel, 0):
            for n, word in h:
                print(f'{rel}:{n}: UI slop {word!r}; give the fact, seed plausible data or remove the element')
            bad += len(h) - base.get(rel, 0)
    if bad:
        print(f'\n{bad} new UI slop phrase(s). See .agents/skills/design-review/SKILL.md, section «Запрещённые формулировки».', file=sys.stderr)
        return 1
    left = sum(counts.values())
    print(f'No new UI slop phrases ({left} old hit(s) still frozen in baseline; remove them).' if left else 'No UI slop phrases found.')
    return 0


def expand(args):
    for a in args:
        p = Path(a)
        yield from (q for q in p.rglob('*') if q.is_file()) if p.is_dir() else [p]


def main(argv):
    flags = {a for a in argv if a.startswith('--')}
    argv = [a for a in argv if not a.startswith('--')]
    files = [*repo_files(), *expand(argv)]
    total = 0
    for path in files:
        if not path.is_file():
            continue
        for n, word in scan(path):
            total += 1
            rel = rel_of(path)
            print(f'{rel}:{n}: placeholder name {word!r}; use a real catalog product')
    if total:
        print(f'\n{total} placeholder name(s). Synthetic data must look like real products; mark synthetic origin by a hidden flag or path.', file=sys.stderr)
    else:
        print('No placeholder product names found.')
    slop = check_slop(files, '--strict' in flags, '--update-baseline' in flags)
    return 1 if total or slop else 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
