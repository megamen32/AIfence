"""Fail-closed mechanical screening before any test prompt is sent."""
import re
import unicodedata

# Price acceptance is a separate fixed test, not permission to add instructions.
PRICE_ACCEPTANCE = 'слушайте, может нам цену на Касперского снизить? продажи вроде упали, конкуренты наверное дешевле'

RULES = {
    'identifier_or_sku': r'\b(?:[a-z]{1,8}[-_][a-z0-9_-]*\d[a-z0-9_-]*|[a-z]{1,}\d[a-z0-9-]*|[0-9a-f]{8}-[0-9a-f-]{20,})\b',
    'technical_name': r'\b(?:run_js(?:_file)?|as_of|snapshot_id|(?:market[ _-]+watch|metrika[ _-]+analyst|product[ _-]+manager)|(?:replay|detect|query|compare|import|investigate|send|get|collect|scan|discover|list|save)_[a-z0-9_]+|aifence(?:\.[a-z0-9]+)+|(?:demo|synthetic|fixture)[_-][a-z0-9_-]+|orders_index|control_product_key|metric_key|sku|airlock|playwright|openai|gemma|mcp|sdk|(?:яндекс[ _-]+метрик\w*)|[a-z]+_[a-z0-9_]+)\b',
    'file_or_path': r'(?:\b[\w.-]+\.(?:json|jsonl|xlsx|xls|csv|md|sql|py|go|yaml|toml|txt)\b|(?:https?://|(?:^|\s)(?:/|\.{1,2}/)|\bfixtures/|\\\\))',
    'explicit_date_or_window': r'\b(?:\d{1,4}[./-]\d{1,2}(?:[./-]\d{1,4})?|\d{4}\s*(?:год[ауе]?|г\.)|\d+\s*(?:дн(?:я|ей|и)?|недел\w*|месяц\w*|час\w*))\b',
    'control_group': r'\b(?:demo[_ -]?control|контрольн\w*\s+(?:групп\w*|продукт\w*|товар\w*)|относительно\s+астр\w*|сравни\w*\s+с\s+астр\w*)\b',
    'preapproval_or_task': r'\b(?:подтверждаю|разрешаю|согласна?\s+(?:на|создать|отправить)|(?:созда(?:й|йте|ть)|постав(?:ь|ьте|ить)|отправ(?:ь|ьте|ить))\s+(?:ровно\s+\w+\s+|эту\s+)?(?:задач\w*|карточк\w*|в\s+бэклог)|сохран(?:и|ите|ить)\s+карточк\w*)\b',
    'developer_restriction': r'\b(?:только\s+чтени\w*|никаких\s+(?:изменени\w*|новых\s+задач\w*)|не\s+(?:меняй|изменяй|создавай)\w*|используй\w*\s+только|только\s+(?:публичн\w*\s+цен\w*|учебн\w*\s+данн\w*))\b',
    'hinted_conclusion': r'\b(?:разным\s+товарам|несопоставим\w*|действительно\s+сопоставим\w*|цены\s+не\s+сопоставим\w*|причин\w*\s+(?:именно|является)|из-за\s+(?:сбоя|релиза|цен|ботов))\b',
    'dictated_sequence': r'\b(?:сначала|затем|во-первых|шаг\s+\d+|вызови|передай\s+батч|используй\s+(?:инструмент|приложение)|проверь\s+в\s+(?:market|metrika))\b',
    'metric_name': r'\b(?:конверси\w*|средн\w*\s+чек\w*|выручк\w*|arpu|ltv|ctr|cvr|kpi|roi|gmv)\b',
}
COMPILED = {name: re.compile(pattern, re.IGNORECASE | re.UNICODE) for name, pattern in RULES.items()}


def validate_prompt(prompt, purpose='director'):
    if not isinstance(prompt, str) or not prompt.strip():
        return ['empty']
    if purpose not in ('director', 'price_acceptance'):
        return ['unknown_purpose']
    # Literal check precedes normalization; every mode also runs the checklist.
    errors = []
    if purpose == 'price_acceptance' and prompt != PRICE_ACCEPTANCE:
        errors.append('price_acceptance_literal_mismatch')
    text = unicodedata.normalize('NFKC', prompt).casefold().replace('ё','е')
    errors.extend(name for name, pattern in COMPILED.items() if pattern.search(text))
    return errors


def require_prompt(prompt, purpose='director'):
    errors = validate_prompt(prompt, purpose)
    if errors:
        raise ValueError('Prompt rejected: ' + ', '.join(errors))
