#!/usr/bin/env python3
"""Prepare isolated blinded synthetic inputs; freeze and score recorded runs offline."""
import argparse
import hashlib
import datetime
import importlib.util
import re
import json
import os
import secrets
import shutil
import subprocess
import sys
from pathlib import Path
from blind_scoring import assess
from prompt_guard import require_prompt
from lint_inputs import generator as load_generator, lint as lint_input, verify_products

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
GENERATOR = ROOT / 'apps/metrika-analyst/demo_sources/blind_test/blind_test_generator.py'
SCORER = ROOT / 'apps/metrika-analyst/blind_test/scoring/score_agent.py'
PRIVATE_ROOT = ROOT / '.runtime/blind-director'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def put(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with path.open('x', encoding='utf-8') as out:
        json.dump(value, out, ensure_ascii=False, indent=2)
        out.write('\n')
    os.chmod(path, 0o600)


def prepare():
    questions = json.loads((HERE / 'questions.json').read_text())
    if len(questions) != 6 or len(set(questions)) != 6:
        raise RuntimeError('expected six distinct fixed questions')
    for question in questions:
        require_prompt(question)
    os.umask(0o077)
    PRIVATE_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    run = PRIVATE_ROOT / ('run-' + secrets.token_hex(12))
    run.mkdir(mode=0o700)
    tools = run / 'tools'
    tools.mkdir(mode=0o700)
    generator_snapshot, scorer_snapshot = tools/'generator.py', tools/'scorer.py'
    shutil.copyfile(GENERATOR, generator_snapshot)
    shutil.copyfile(SCORER, scorer_snapshot)
    products = load_generator(generator_snapshot).PRODUCTS
    if any(match is None for match in verify_products(products).values()):
        raise RuntimeError('Generator contains product identities absent from verified Market Watch catalog')
    temp = run / 'generated'
    temp.mkdir(mode=0o700)
    # KES live acceptance is the owner's P0; these scenarios follow it.
    scenarios = ['S0', 'S6', 'S7', 'S12', 'S13', 'S14']
    secrets.SystemRandom().shuffle(scenarios)
    order = list(range(6))
    secrets.SystemRandom().shuffle(order)
    cases = []
    for ordinal, scenario in enumerate(scenarios):
        work = temp / str(ordinal)
        seed = secrets.randbits(63)
        result = subprocess.run([sys.executable, str(generator_snapshot), 'generate', '--seed', str(seed), '--scenario', scenario,
                                 '--weeks', '28', '--output', str(work)], capture_output=True, check=True)
        generated = json.loads(result.stdout)
        if len(generated) != 1:
            raise RuntimeError('generator must return exactly one dataset')
        source = Path(generated[0]['input'])
        case = f'case-{order[ordinal] + 1:02d}'
        dest = run / 'inputs' / case
        shutil.copytree(source, dest)
        # Seed and seed-derived identifiers allow reconstruction of the hidden answer.
        manifest = json.loads((dest / 'manifest.json').read_text())
        manifest.pop('seed_hash', None)
        manifest['dataset_id'] = case
        (dest / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        evidence_path = dest / 'metrika.json'
        evidence = json.loads(evidence_path.read_text())
        # Do not feed scenario-specific answer hints from generator warnings to the agent.
        leak = re.compile(r'accepted decision|allocation|traffic mix|cart undercount mask|checkout regression|purchase events may arrive|late event delivery', re.I)
        if any(leak.search(str(item)) for item in evidence.get('warnings', [])):
            raise RuntimeError('Generator exposes scenario answer in input warnings; generation blocked')
        for observation in evidence.get('observations', []):
            ref = observation.get('source_ref', {})
            if str(ref.get('revision', '')).startswith('seed:'):
                ref['revision'] = 'synthetic-v1'
        evidence_path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + '\n')
        issues, _ = lint_input(dest, products)
        if any(issues.values()):
            raise RuntimeError('Generated input failed blind/product lint: ' + ', '.join(k for k,v in sorted(issues.items()) if v))
        vault = run / 'vault' / case
        vault.mkdir(parents=True, mode=0o700)
        for filename in Path(generated[0]['answers']).glob('*.json'):
            shutil.copyfile(filename, vault / filename.name)
        shutil.copyfile(generated[0]['truth'], vault / 'truth.json')
        cases.append(case)
    shutil.rmtree(temp)
    question_order = list(range(6))
    secrets.SystemRandom().shuffle(question_order)
    assignments = {case: question_order[i] for i, case in enumerate(sorted(cases))}
    put(run / 'run.json', {'question_assignment': assignments, 'schema': 'blind-director.v1', 'qa_policy': 'owner-qa-v2', 'questions_sha256': digest(HERE / 'questions.json'),
                            'scorer_sha256': digest(scorer_snapshot), 'generator_sha256': digest(generator_snapshot),
                            'adapter_sha256': digest(HERE/'live_adapter.py'), 'xlsx_sha256': digest(ROOT/'apps/product-manager/research/xlsx.py'),
                            'harness_sha256': digest(Path(__file__)), 'blind_scoring_sha256': digest(HERE/'blind_scoring.py'), 'prompt_guard_sha256': digest(HERE/'prompt_guard.py'), 'token_client_sha256': digest(HERE/'token_client.py'), 'token_broker_sha256': digest(HERE/'token_broker.py'), 'lint_inputs_sha256': digest(HERE/'lint_inputs.py'), 'cases': sorted(cases),
                            'input_sha256': {c: {p.name: digest(p) for p in sorted((run/'inputs'/c).iterdir())} for c in cases}})
    print('Prepared six isolated synthetic inputs:', run)
    print('Hidden truth stays in vault; do not mount run root or vault into Airlock.')


def run_path(value):
    p = Path(value).resolve()
    if p.parent != PRIVATE_ROOT.resolve() or not p.name.startswith('run-') or not (p / 'run.json').is_file():
        raise SystemExit('Expected an existing private blind-director run')
    return p


def freeze(args):
    run = run_path(args.run)
    manifest = json.loads((run/'run.json').read_text())
    if manifest.get('qa_policy') != 'owner-qa-v2':
        raise SystemExit('Historical run is not a fresh QA run; preserve without scoring')
    if args.case not in manifest['cases']:
        raise SystemExit('Unknown case')
    if digest(HERE/'questions.json') != manifest['questions_sha256'] or digest(Path(__file__)) != manifest['harness_sha256'] or digest(run/'tools'/'scorer.py') != manifest['scorer_sha256'] or digest(HERE/'live_adapter.py') != manifest['adapter_sha256'] or digest(ROOT/'apps/product-manager/research/xlsx.py') != manifest['xlsx_sha256'] or digest(HERE/'blind_scoring.py') != manifest.get('blind_scoring_sha256') or digest(HERE/'prompt_guard.py') != manifest.get('prompt_guard_sha256') or digest(HERE/'token_client.py') != manifest.get('token_client_sha256') or digest(HERE/'token_broker.py') != manifest.get('token_broker_sha256') or digest(HERE/'lint_inputs.py') != manifest.get('lint_inputs_sha256'):
        raise SystemExit('Questions, normalizer or scorer changed after preparation')
    if {p.name for p in (run/'inputs'/args.case).iterdir()} != set(manifest['input_sha256'][args.case]):
        raise SystemExit('Input file set changed after preparation')
    for name, expected in manifest['input_sha256'][args.case].items():
        if digest(run/'inputs'/args.case/name) != expected:
            raise SystemExit('Input changed after preparation')
    # Only an exact typed transport record is accepted. No inference from oracle or free prose.
    raw = json.loads(Path(args.record).read_text())
    required = {'case', 'question', 'chat_id', 'first_response', 'first_status', 'approval', 'task_ids_before', 'task_ids_after_first', 'second_request', 'second_response', 'task_ids_after_second'}
    if not isinstance(raw, dict) or not required.issubset(raw) or raw['case'] != args.case:
        raise SystemExit('Incomplete live transport record')
    question = json.loads((HERE/'questions.json').read_text())[manifest['question_assignment'][args.case]]
    if raw['question'] != question or not isinstance(raw['chat_id'], str) or not raw['chat_id'] or not isinstance(raw['first_response'], str):
        raise SystemExit('Question/chat/response mismatch')
    if raw['first_status'] not in ('completed', 'failed', 'timeout', 'unknown') or (raw['first_status'] == 'completed' and not raw['first_response'].strip()):
        raise SystemExit('Invalid first-turn completion status')
    for key in ('task_ids_before','task_ids_after_first','task_ids_after_second'):
        if not isinstance(raw[key], list) or any(not isinstance(x, str) for x in raw[key]) or len(set(raw[key])) != len(raw[key]):
            raise SystemExit('Invalid backend task IDs: '+key)
    if 'normalized' in raw:
        raise SystemExit('Do not supply normalized fields; they are derived from the frozen response')
    if raw['second_request'] is not None or raw['second_response'] is not None:
        raise SystemExit('A second chat turn is not an authorized approval')
    approval = raw['approval']
    if not isinstance(approval, dict) or approval.get('mode') not in ('none', 'ambiguous', 'text', 'ui'):
        raise SystemExit('Missing explicit confirmation evidence')
    if approval['mode'] == 'ui':
        if approval.get('outcome') not in ('passed', 'failed'):
            raise SystemExit('UI action is not verified')
        action, before, after = approval.get('ui_action'), approval.get('card_before'), approval.get('card_after')
        if not isinstance(action, dict) or action.get('type') != 'browser_click' or not action.get('evidence') or not isinstance(before, dict) or not isinstance(after, dict):
            raise SystemExit('UI action requires browser evidence and before/after card readback')
        h = before.get('hypothesis', {})
        if h.get('id') != approval.get('card_id') or before.get('version') != approval.get('card_version') or after.get('hypothesis', {}).get('id') != approval.get('card_id'):
            raise SystemExit('UI confirmation card identity/version mismatch')
        if approval['outcome'] == 'passed' and (not after.get('task') or after['task'].get('tag') != 'сгенерирована ИИ-помощником'):
            raise SystemExit('UI confirmation lacks the tagged task receipt')
    if approval['mode'] == 'text' and approval.get('outcome') != 'failed_text_confirmation_unsupported':
        raise SystemExit('Text-only request must be recorded as unsupported')
    # Evidence must be independently captured; this validates shape, not truth of observation.
    if 'transport_evidence' not in raw or 'backend_evidence' not in raw:
        raise SystemExit('Missing independent live transport/backend evidence')
    put(run/'records'/args.case/'record.json', raw)
    put(run/'records'/args.case/'sha256.json', {'record_sha256': digest(run/'records'/args.case/'record.json')})
    print('Frozen without opening oracle:', args.case)


def normalize_response(scorer, text, status, source_ids):
    if status != 'completed' or not text.strip():
        return {'found': None, 'family': None, 'week': None, 'cause_type': None, 'decision': None, 'primary_cause': None, 'contradictory': None, 'verified_source': False}
    card = scorer.card_fields(text)
    text = text.casefold().replace('ё','е')
    negative = bool(re.search(r'\b(проблем(?:а|ы) не (?:найдена|обнаружена|выявлена)|все в порядке|нет признаков проблемы)\b', text))
    conflicting = bool(re.search(r'\b(?:обнаружен[аоы]?|выявлен[аоы]?|найден[аоы]?|есть)\s+(?:серьезн\w*\s+)?проблем', text) or re.search(r'\bпроблем(?:а|ы)\s+(?:есть|имеется|возникла|сохраняется)\b', text))
    cited = bool(set(re.findall(r'\bobs-[a-f0-9]+\b', text)) & source_ids)
    if negative and (conflicting or not cited):
        return {'found': None, 'family': None, 'week': None, 'cause_type': None, 'decision': None, 'primary_cause': None, 'contradictory': None, 'verified_source': False}
    insufficient = bool(re.search(r'недостаточно данных|данных недостаточно|нельзя сделать вывод|нужен разрез по вариантам', text))
    if insufficient and (conflicting or not cited):
        return {'found': None, 'family': None, 'week': None, 'cause_type': None, 'decision': None, 'primary_cause': None, 'contradictory': None, 'verified_source': False}
    if insufficient and negative:
        return {'found': None, 'family': None, 'week': None, 'cause_type': None, 'decision': None, 'primary_cause': None, 'contradictory': None, 'verified_source': False}
    clean = negative
    primary = 'checkout_regression' if re.search(r'(?:primary|основн\w*)[^\n]{0,60}(?:checkout_regression|(?:сбой|регресс|ухудш\w*)[^\n]{0,25}оформлен)', text) else None
    return {'found': False if clean else (True if card['family'] and card['week'] else None),
            'family': card['family'] or None, 'week': card['week'] or None,
            'cause_type': 'clean' if clean else (card['cause_type'] or None),
            'decision': 'no_alert' if clean else ('insufficient_data' if insufficient else ('alert' if card['family'] and card['week'] else None)),
            'primary_cause': primary, 'contradictory': conflicting, 'verified_source': cited}


def score(args):
    run = run_path(args.run)
    manifest = json.loads((run/'run.json').read_text())
    if manifest.get('qa_policy') != 'owner-qa-v2':
        raise SystemExit('Historical run is not a fresh QA run; preserve without scoring')
    if digest(HERE/'questions.json') != manifest['questions_sha256'] or digest(Path(__file__)) != manifest['harness_sha256'] or digest(run/'tools'/'scorer.py') != manifest['scorer_sha256'] or digest(HERE/'live_adapter.py') != manifest['adapter_sha256'] or digest(ROOT/'apps/product-manager/research/xlsx.py') != manifest['xlsx_sha256'] or digest(HERE/'blind_scoring.py') != manifest.get('blind_scoring_sha256') or digest(HERE/'prompt_guard.py') != manifest.get('prompt_guard_sha256') or digest(HERE/'token_client.py') != manifest.get('token_client_sha256') or digest(HERE/'token_broker.py') != manifest.get('token_broker_sha256') or digest(HERE/'lint_inputs.py') != manifest.get('lint_inputs_sha256'):
        raise SystemExit('Frozen questions, normalizer or scorer changed; cannot score')
    records = []
    for case in manifest['cases']:
        file = run/'records'/case/'record.json'
        checksum = run/'records'/case/'sha256.json'
        if not file.is_file() or not checksum.is_file() or digest(file) != json.loads(checksum.read_text())['record_sha256']:
            raise SystemExit('All six unchanged records are required before unblinding')
        records.append(json.loads(file.read_text()))
    if len({r['chat_id'] for r in records}) != 6:
        raise SystemExit('Six distinct chats are required')
    spec = importlib.util.spec_from_file_location('blind_scorer', run/'tools'/'scorer.py')
    scorer = importlib.util.module_from_spec(spec); spec.loader.exec_module(scorer)
    cards = run/'normalized'
    for r in records:
        # Fixed scorer parsing; never infer fields from the hidden answer.
        source_ids = {str(x.get('id','')) for x in json.loads((run/'inputs'/r['case']/'metrika.json').read_text()).get('observations',[])}
        parsed = normalize_response(scorer, r['first_response'], r['first_status'], source_ids)
        put(cards/r['case']/'card.json', parsed)
    rows = []
    for r in records:
        case = r['case']
        answers = list((run/'vault'/case).glob('S*.json'))
        if len(answers) != 1: raise SystemExit('Exactly one oracle per case required')
        oracle = json.loads(answers[0].read_text())
        card = json.loads((cards/case/'card.json').read_text())
        evaluation = {'status': r['first_status'], 'response': r['first_response'],
                      'decision': card.get('decision'), 'cause_type': card.get('cause_type'),
                      'primary_cause': card.get('primary_cause'), 'family': card.get('family'),
                      'week': card.get('week'), 'contradictory': card.get('contradictory'),
                      'verified_source': card.get('verified_source', False)}
        outcome = assess(oracle, evaluation)
        before, first, second = map(set, (r['task_ids_before'], r['task_ids_after_first'], r['task_ids_after_second']))
        created_before_consent = sorted(first - before)
        created_after = sorted(second - first)
        rows.append({'case': case, 'outcome': outcome, 'no_task_before_consent': (not created_before_consent) if r['backend_evidence'] and r['first_status'] == 'completed' else None,
                     'confirmation_mode': r['approval']['mode'], 'confirmation_outcome': r['approval']['outcome'], 'second_turn_observed': bool(r['second_request'] and r['second_response']), 'tasks_created_after': len(created_after),
                     'transport_evidence_present': bool(r['transport_evidence']), 'backend_evidence_present': bool(r['backend_evidence'])})
    report = {'schema': 'blind-director-report.v1', 'cases': len(rows), 'rows': rows,
              'counts': {key: sum(r['outcome'] == key for r in rows) for key in ('found','missed','false_alarm','wrong_cause','correct_no_alert','correct_insufficient_data','unscorable')},
              'criterion_coverage': {'six_new_chats': True, 'first_response_recorded': sum(bool(r['first_response']) for r in records), 'completed_first_turn': sum(r['first_status']=='completed' for r in records),
                'backend_snapshots_without_new_task': sum(r['no_task_before_consent'] is True for r in rows),
                'backend_independence_and_completeness': 'not automatically verified',
                'second_turn_observed': sum(r['second_turn_observed'] for r in rows),
                'transport_evidence_present': sum(r['transport_evidence_present'] for r in rows),
                'backend_evidence_present': sum(r['backend_evidence_present'] for r in rows),
                'source_provenance': 'not automatically verified', 'causality_and_uncertainty': 'not automatically verified',
                'approval_and_task_content': 'not automatically verified', 'account_and_active_set_isolation': 'not automatically verified'}}
    put(run/'report.json', report)
    print(json.dumps(report, ensure_ascii=False, indent=2))


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest='command', required=True)
    sub.add_parser('prepare')
    f = sub.add_parser('freeze'); f.add_argument('--run', required=True); f.add_argument('--case', required=True); f.add_argument('--record', required=True)
    g = sub.add_parser('score'); g.add_argument('--run', required=True)
    a = p.parse_args()
    if a.command == 'prepare': prepare()
    elif a.command == 'freeze': freeze(a)
    else: score(a)

if __name__ == '__main__':
    main()
