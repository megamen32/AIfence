# Blind test generator

`blind_test_generator.py` creates one deterministic internal truth and derives
from it a Metрика-like evidence-v1 JSON, a customer-shaped `Продажи.xlsx`, and
release/backlog context. The agent receives only the `input/` directory. Hidden
answers and truth stay beside it in `answers/` and `truth.json`.

## Generate

```sh
python3 blind_test_generator.py generate \
  --seed 20261126 \
  --scenario all \
  --weeks 28 \
  --output /path/to/blind-runs
```

Use a new output directory for each run. The generator refuses to replace an
existing dataset. Visible product names and SKU fields identify verified Market
Watch offers; list prices are the real offers captured on 2026-11-26
(`PRICE_CAPTURED`), e.g. Kaspersky Small Office Security 4 718.70 ₽ at Softline
and 4 719 ₽ at Softmap. Volumes, conversion (≈1–2% visit→order) and weekly
movements are synthetic. Weeks run from 2026-05-11 to 2026-11-22.

Design rules (audit 27.11, `notes/blind-cases-quality-2026-11-27.md`):

- planted changes hit a Kaspersky family (`--family-pool kaspersky`, default;
  `all` restores the old random choice) because the director question is about
  Kaspersky, and they land in the last 4–6 weeks (S13: last 2 weeks);
- Metrika undercount is one stable share per series (10–25%), weekly noise is
  small, seasonality follows the calendar; a clean S0 never shows a 4-week
  orders drop deeper than −12% per family or −15% per family × device (test);
- inputs carry no answer fields (`bot_share_estimate`, `traffic_quality`,
  `canonical_product_key`, `available_on`, `alias_*`, `experiment_id`, …) and
  no prices in releases; every dataset has four or more specific-looking decoy
  releases, so the planted release is not the only unusual one;
- `details.synthetic` / `origin: synthetic_fixture` stay on every row as
  platform provenance; captions are ordinary Russian export captions.

Each opaque `set-<hash>/input` contains:

- `metrika.json` — 28 weeks × 7 catalog families × desktop/mobile, with visits, card views, cart additions, checkouts, orders, revenue, bounce rate and average visit duration;
- `Продажи.xlsx` — sheets `Продажи` (full orders from sales accounting, catalog prices), `Релизы`, `Бэклог_задачи`;
- `releases.json` and `backlog.json` — noisy product context;
- `manifest.json` — no scenario name, answer path or cause hint.

`answers/<scenario>.json` and `truth.json` are outside `input/` and must not be
mounted into the agent's catalog. Answer `week` is the ISO week start
(`week_index` keeps the number); `severity` is the effect actually applied.

What each scenario looks like to an analyst:

| Scenario | Metrika | Sales workbook |
|---|---|---|
| S0 | ordinary noise and season | ordinary |
| S6 | desktop visits ×2–3, bounce ~0.6, duration drops, orders flat | orders flat |
| S7 | family key stops, its SKU key continues at the same level | orders flat |
| S12 | checkouts/orders/revenue `incomparable` for the last weeks | comment: reconciliation open |
| S13 | orders/revenue of the last 2 weeks −45% / −82% | orders flat |
| S14 | visits +50%, cart under-counted, orders −25…−38% | orders −25…−38% |

For an explicit live-counter smoke test, build a token-free recent plan from the
latest evidence week:

```sh
python3 blind_test_generator.py mp-plan \
  --input /Users/user/AIfence/data/blind-runs/set-<hash>/input/metrika.json \
  --output /Users/user/AIfence/data/blind-runs/mp-plan.json \
  --counter 114698578 --seed 20261126 --max-visits 900
```

Measurement Protocol does not backfill old weekly timestamps, so the plan
re-times a proportional sample into the last eight hours and labels that
limitation. After reviewing the plan, send it only with a token held in the
environment; the command never writes the token:

```sh
export YANDEX_METRIKA_TOKEN='…'
python3 blind_test_generator.py mp-send \
  --plan /Users/user/AIfence/data/blind-runs/mp-plan.json \
  --counter 114698578 --token-env YANDEX_METRIKA_TOKEN
```

S12 accepts either `no_alert` or `insufficient_data`; an alarm with a guessed
family is a false alarm. The source truth contains full orders and revenue
for the sales workbook.

## Score agent cards

Write one card per opaque dataset at `<cards>/set-<hash>/card.json`:

```json
{"found": true, "family": "kaspersky_small_office_security", "week": "2026-10-26", "cause_type": "release_regression"}
```

Then run:

```sh
python3 blind_test_generator.py score \
  --root /path/to/blind-runs \
  --cards /path/to/cards \
  --output /path/to/score.json
```

The result marks found, missed, false alarm, and correct cause type. Silence on
S0 is scored as correct when no card is supplied. For S12, these equivalent
refusal cards are correct:

```json
{"outcome": "no_alert"}
{"status": "insufficient_data"}
{"decision": "no_alert"}
{"alert": false}
```

The standalone scorer also accepts the new cause classes
`traffic_quality`, `taxonomy_migration`, `late_event_delivery`,
`masked_multi_cause_regression`, and `experiment_allocation_shift`.

## QA

```sh
python3 -m py_compile blind_test_generator.py test_blind_test_generator.py
python3 -m unittest -v test_blind_test_generator_unittest.py
python3 - <<'PY'
from pathlib import Path
import blind_test_generator as g
import tempfile
with tempfile.TemporaryDirectory() as d:
    x=g.write_dataset(Path(d), 20261126, 'S1', 28)
    assert min(__import__('json').loads(Path(x['truth']).read_text())['losses']) >= .10
print('blind generator smoke: ok')
PY
```
