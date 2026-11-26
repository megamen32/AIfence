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

Each opaque `set-<hash>/input` contains only generic names:

- `metrika.json` — 28+ weeks × 5 product families × desktop/mobile, with visits, card views, cart additions, checkouts, orders and revenue;
- `Продажи.xlsx` — sheets `Продажи`, `Релизы`, `Бэклог_задачи`;
- `releases.json` and `backlog.json` — noisy product context;
- `manifest.json` — no scenario name, answer path or cause hint.

`answers/<scenario>.json` and `truth.json` are outside `input/` and must not be
mounted into the agent's catalog. The generator never sends to Yandex. Use the
existing Measurement Protocol helper only after an explicit review of the
generated `metrika.json`; the blind test itself should stay reproducible and
offline.

Scenarios are selected by the seed and contain S0 clean data, S1 missing cart
events, S2 mobile-only regression, S3 price-versus-competitor regression, and
S4 a family regression aligned with a release and a stable control group.
Observed behaviour metrics intentionally lose 10–25% independently to model
blockers and undercounting. The source truth still contains full orders and
revenue for the sales workbook.

## Score agent cards

Write one card per opaque dataset at `<cards>/set-<hash>/card.json`:

```json
{"found": true, "family": "KES", "week": 13, "cause_type": "release_regression"}
```

Then run:

```sh
python3 blind_test_generator.py score \
  --root /path/to/blind-runs \
  --cards /path/to/cards \
  --output /path/to/score.json
```

The result marks found, missed, false alarm, and correct cause type. Silence on
S0 is scored as correct when no card is supplied.

## QA

```sh
python3 -m py_compile blind_test_generator.py test_blind_test_generator.py
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
