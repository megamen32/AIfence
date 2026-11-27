#!/usr/bin/env python3
"""Render results only after the fixed scorer has frozen and scored all cases."""
import json
from pathlib import Path

import blind30_score
import blind30_trace

ROOT=Path(__file__).resolve().parents[3]
PACKAGE=ROOT/'.runtime/blind-director/blind30-sol-v2'
REPORT=ROOT/'docs/qa/local-sol-blind30-2026-11-27.md'


def clean(value):
    return str(value).replace('|','\\|').replace('\n',' ').strip()


def main():
    _,manifest=blind30_score.verify(PACKAGE)
    if any(not (PACKAGE/'records'/f"{x['id']}.json").is_file() for x in manifest['cases']):
        raise RuntimeError('All 30 records must be frozen before the report')
    report=json.loads((PACKAGE/'report.json').read_text())
    usage=json.loads((PACKAGE/'local-model-usage-v2.json').read_text())['rows']
    rows={r['case']:r for r in report['rows']}
    if set(rows)!={x['id'] for x in manifest['cases']}:raise RuntimeError('Incomplete score report')
    blind30_trace.main()
    lines=['# Локальный Sol: 30 слепых наборов','',
           'Локальный QA Airlock `42082`, отдельный владелец и новый чат на каждый набор. '
           'Вопрос одинаковый; исходы заморожены до открытия эталонов. '
           'Эффективный маршрут проверялся перед каждым запросом; upstream независимо не аттестован. '
           'Синтетические данные не являются измерениями бизнеса. Общий публичный каталог Market Watch '
           'не является изолированным снимком кейса.','',
           '| Кейс / сценарий | Итог | Какие источники использовал | Финальный ответ | Чат / run |',
           '|---|---|---|---|---|']
    groups={'positive':[],'negative':[],'mixed':[]}
    for item in manifest['cases']:
        case=item['id'];row=rows[case];raw=json.loads((PACKAGE/'records'/f'{case}.json').read_text())['record']
        trace=json.loads((PACKAGE/'traces-v4'/f'{case}.json').read_text())
        sources=sorted({str(o['source']) for call in trace['calls'] for o in call['observations'] if o.get('source')})
        operations=sorted({op for call in trace['calls'] if call['result_received'] and not call['result_failed'] for op in call['operations']})
        source_text=', '.join(sources) if sources else 'числовое чтение с локатором не подтверждено'
        if operations:source_text+='; вызовы: '+', '.join(operations)
        final=' '.join(raw['response'].split())
        if len(final)>220:final=final[:217]+'…'
        lines.append(f"| {case} / {clean(row.get('scenario','?'))} | {clean(row['outcome'])} ({clean(raw['status'])}) | {clean(source_text)} | {clean(final) or 'пустой ответ'} | `{raw['chat_id']}` / `{raw['run_id']}` |")
        oracle_files=list((Path(item['run'])/'vault'/case[-7:]).glob('S*.json'))
        if len(oracle_files)!=1:raise RuntimeError('Missing oracle')
        oracle=json.loads(oracle_files[0].read_text())
        accepted=set(oracle.get('expected_decisions') or oracle.get('accepted_outcomes') or [])
        if oracle.get('cause_type') in ('none','clean') or (accepted and accepted<={'no_alert','insufficient_data'}):group='negative'
        elif accepted & {'no_alert','insufficient_data'}:group='mixed'
        else:group='positive'
        groups[group].append(row['outcome'])
    pos=groups['positive'];neg=groups['negative'];mixed=groups['mixed']
    records=[json.loads((PACKAGE/'records'/f"{item['id']}.json").read_text())['record'] for item in manifest['cases']]
    covered={record['case'] for record in records if any(r.get('model')=='codex/gpt-6.1-sol' and (r.get('conversation_id')==record['chat_id'] or r.get('system_run_id')==record['run_id']) for r in usage)}
    models=sorted({str(r.get('model')) for r in usage})
    lines+=['',f"Положительных кейсов: {len(pos)}, оценено {len(pos)-pos.count('unscorable')}; подтверждённых попаданий {pos.count('found')}. "
            f"Отрицательных кейсов: {len(neg)}, оценено {len(neg)-neg.count('unscorable')}; подтверждённых ложных тревог {neg.count('false_alarm')}. "
            f"Смешанных вариантов эталона: {len(mixed)}; неоцениваемых всего: {sum(r['outcome']=='unscorable' for r in rows.values())}/30. "
            'Частоты при нуле оцениваемых не определены; неоцениваемые не считаются ни успехом, ни промахом.', '',
            f"Локальный `llm_usage`: Sol подтверждён по чату или system run для {len(covered)}/30; наблюдавшиеся модели: {', '.join(models) or 'нет записей'}. "
            'Это запись Airlock, а не независимая аттестация upstream.', '',
            'Источник считается численно прочитанным, когда вызов сопоставлен с результатом по ID и в результате есть число с локатором; '
            'само название источника или каталог SKU чтением не считается. Сырые tool results, включая submit_answer, '
            'и финалы сохранены раздельно в приватных terminal и traces.']
    if REPORT.exists():raise RuntimeError('Report already exists; preserve it')
    REPORT.write_text('\n'.join(lines)+'\n')
    print('Report saved:',REPORT)


if __name__=='__main__':main()
