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
Watch offers. Seven independently configured profiles cover Kaspersky Small Office
Security (`KL4141RCEFR`), Astra Linux Special Edition (`OS2304ELB81BOX000WS01-SO36`),
Microsoft 365 Family (`6GQ-00084`), VMware vSphere Edition Starter Kit
(`PRGN-0121-105`), Kaspersky Endpoint Security (`KL4210RAKDE`), Dr.Web Desktop
Security Suite (`LBW-BC-12M-100-A3`) and МойОфис Частное облако
(`X2-CLD-NB-3NUNL-A`). Every SKU is accompanied by its exact offer URL.
The Astra link identifies an OS licence for the specified hardware variant.
The generated observations and prices are synthetic. The taxonomy case changes an observed
catalog key from the canonical family to its genuine SKU alias.

Each opaque `set-<hash>/input` contains:

- `metrika.json` — 28+ weeks × 7 verified catalog families × desktop/mobile, with visits, card views, cart additions, checkouts, orders and revenue;
- `Продажи.xlsx` — sheets `Продажи`, `Релизы`, `Бэклог_задачи`;
- `releases.json` and `backlog.json` — noisy product context;
- `manifest.json` — no scenario name, answer path or cause hint.

`answers/<scenario>.json` and `truth.json` are outside `input/` and must not be
mounted into the agent's catalog. Generation itself is deterministic and offline.
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

Scenarios are selected by the seed and contain the original S0–S4 set plus
S6 bot/low-quality traffic, S7 product-taxonomy migration, S12 an honest
refusal where the evidence is intentionally incomparable, S13 delayed purchase
events, and S14 a multi-cause regression masked by traffic growth and cart
under-counting. S12 accepts either `no_alert` or `insufficient_data`; an
alarm with a guessed family is a false alarm.
Observed behaviour metrics intentionally lose 10–25% independently to model
blockers and undercounting. The source truth still contains full orders and
revenue for the sales workbook.

## Score agent cards

Write one card per opaque dataset at `<cards>/set-<hash>/card.json`:

```json
{"found": true, "family": "kaspersky_small_office_security", "week": 13, "cause_type": "release_regression"}
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
