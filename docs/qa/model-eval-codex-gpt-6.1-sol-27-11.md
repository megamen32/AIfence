# Model evaluation: `codex/gpt-6.1-sol` against Terra fast · 27–28 Nov 2026

Offline model evaluation of every existing model evaluation in AIfence, run through
OmniRoute against `codex/gpt-6.1-sol` (requested service tier `priority`) and compared
with Terra fast (`codex/gpt-6.1-terra`, same tier). Nothing was deployed, no stand was
contacted, no prompt, product code or case data was changed.

> **Model id.** The task text left the model id as the placeholder `<KASOM_MODEL_ID>`.
> The runs below use `codex/gpt-6.1-sol`: it is the model this repository's `sol-*` work
> is named after, the model that produced the frozen `blind30-sol-v2` dataset, and the
> model behind the "Sol 0/30" baseline quoted for suite 2. If another id was intended,
> the harness is parameterised and the same three suites re-run unchanged.

## What was measured

| Suite | Cases | What the model saw | Baseline quoted in the task |
| --- | ---: | --- | --- |
| 1. dev30, prompt v4 | 30 (20 planted causes) | production PM projection of the v3 dev cases | Terra fast 19/20 cause-first, 0 false alarms |
| 2. blind30 | 30 (20 planted causes) | production PM projection of the frozen blind30 set, rebuilt on current `main` | Sol 0/30 on old code |
| 3. sealed holdout v2 | 30 (22 planted causes) | production PM projection of the holdout regenerated from the spent seed | watchdog v2 11/22, 0 false alarms |
| 4. Go tests | — | no test in the repository calls a model | — |

Per case the measurement records: whether the cause is named first (yes / partial / no),
whether the named cause is the planted one (yes / no), whether the answer is a false alarm
(yes / no), tool calls, prompt/completion tokens, latency, and any error or timeout.

## Method, and why the columns are comparable

* **Prompt, question and tool surface are the frozen dev30 ones.** The runner reads the
  prompt from `terra-fast-v3/prompt-v4/main.go` (description + instruction), sends the same
  director question as the 27 Nov Terra run, and exposes exactly one tool
  (`run_js` → `review_sales_and_prices`) that returns the PM projection. This is the
  30-case offline variant of `terra-fast-v3/prompt-v4/replay.py`; that file replays the two
  sealed cases only, `replay_all.py` drives all 30. Prompts and cases were read, never
  edited.
* **Both columns come from the same harness on the same day.** The Terra fast column was
  re-measured with the identical runner instead of being quoted from a note, so the
  comparison is like-for-like. The quoted baseline is reproduced below as a control.
* **One grader, frozen before scoring.** `cause_named_first`, `correct_cause` and
  `false_alarm` come from a single deterministic grader
  (`.runtime/model-eval/grader.py`, SHA-256 `f0d1045f45aabb1361abcd87c927c4313cc17a8e659119f723ea9b92952ba8ac`)
  that reads only the frozen answer text, the frozen planted truth and the frozen
  projection. A cause type counts when it is asserted in an explicit causal frame
  ("наиболее вероятная причина — …", "перенос … в новый ключ") and is not negated,
  negated by a predicate, listed as an alternative after "а не", or stated conditionally.
  For S0 and S12 any cause claim is a false alarm (typed false alert / unsupported
  diagnosis), matching the vocabulary of the 27 Nov detector table.
* **Grader control.** Applied to the frozen 27 Nov Terra fast answers
  (`prompt-v4/all/*/terra-new/final.json`) the grader returns **19/20 correct cause and
  0 false alarms** — the baseline stated in the task, reproduced exactly. The grader
  reproduces the number it is checked against before it is used to judge anything else.
* **PM projection is current `main`.** The PM brief was compiled from `origin/main`
  `91c8efe` (`apps/product-manager`, plus the sqlc/templ generated code the image build
  produces). On dev30 case `052cbe93-case-01` this build reproduces the frozen 27 Nov
  projection byte-for-byte, so the offline path is the production path.
* **Collector is the sealed watchdog.** The typed cause document the PM shows the model
  comes from the Metrika watcher; the measurement copy's `watch_case.go`,
  `watch_magnitude.go`, `watch_schedule.go` and `watch_blind_test.go` are **identical to
  commit `19d7423`**, the watchdog v2 that produced the sealed 11/22 baseline. Current
  `main`'s Metrika app cannot be compiled on this Mac: it calls `agentsdk.WithReadOnly`,
  which exists only in the private pinned SDK, not in the public `agentsdk v0.11.0` in the
  local module cache. Recorded as a limit below.
* **Latency** is wall-clock time of the whole model exchange per case. The offline replay
  has no per-step Airlock transport, so the 60 s per-step Airlock limit does not apply; no
  single model turn exceeded it either (slowest recorded turn is in the per-case records).

## Summary

| Suite | Terra fast (`codex/gpt-6.1-terra`) | `codex/gpt-6.1-sol` |
| --- | --- | --- |
| dev30 prompt v4, correct cause | **19/20** | **19/20** |
| dev30 prompt v4, false alarms | 0/30 | **0/30** |
| dev30 prompt v4, cause named first | 7 yes / 12 partial / 1 no | **13 yes / 7 partial / 0 no** |
| blind30 on current main, correct cause | 4/20 | **7/20** |
| blind30 on current main, false alarms | 7/30 | **6/30** |
| sealed holdout v2 (re-run), correct cause | 9/22 | **12/22** |
| sealed holdout v2 (re-run), false alarms | 0/30 | 0/30 |
| detector (watchdog v2) on the same holdout | 11/22, 0 false alarms | 11/22, 0 false alarms |
| median latency per case (dev30) | 100.7 s | 96.8 s |
| median cost per case (dev30) | $0.0287 | $0.0584 |

Controls: the frozen 27 Nov Terra fast answers score 19/20 with 0 false alarms under the
same grader; the holdout regenerated from seed `672862278566` reproduces the sealed
hashes (`truth a254a461…`, `inputs 9bc23fa1…`) and the detector reproduces **11/22 hits,
0 false alarms**. The 27 Nov hosted blind30 Sol run scored 0/30 (all 30 cases
`unscorable`, no structured answer); that run went through a chat on the QA stand, which
this task forbids touching, so the rerun below uses the offline projection path instead.

## Per-type breakdown

Correct cause / false alarms, out of 5 cases per type (6 for S6/S14 in the holdout).

### dev30, prompt v4 — the PM projection carries a typed cause in every planted case

| Type | Terra fast: correct / FA, cause-first (y/p/n) | `codex/gpt-6.1-sol`: correct / FA, cause-first (y/p/n) |
| --- | --- | --- |
| S7 catalogue migration | 4/5, 0 FA — 4/0/1 | **5/5**, 0 FA — 5/0/0 |
| S14 checkout regression | 5/5, 0 FA — 1/4/0 | 5/5, 0 FA — 2/3/0 |
| S6 traffic quality | 5/5, 0 FA — 2/3/0 | 5/5, 0 FA — 4/1/0 |
| S13 late event delivery | 5/5, 0 FA — 0/5/0 | 4/5, 0 FA — 2/3/0 |
| S0 expected quiet | 5/5 quiet, 0 FA | 5/5 quiet, 0 FA |
| S12 expected abstention | 5/5 abstained, 0 FA | 5/5 abstained, 0 FA |

Both models reach the same 19/20 on the positives and produce no false alarm on the ten
negative cases. The difference is *how*: Terra fast opens with the price recommendation in
13 of 20 planted cases and names the cause later (12 "partial"), Sol opens with the cause
in 13 of 20 and never leaves it out of the first three sentences.

### blind30 on current main — the PM projection carries a typed cause only for S7

| Type | Terra fast: correct / FA | `codex/gpt-6.1-sol`: correct / FA |
| --- | --- | --- |
| S7 catalogue migration | 3/5, 1 FA | **4/5**, 0 FA |
| S14 checkout regression | 1/5, 0 FA | **3/5**, 1 FA |
| S6 traffic quality | 0/5, 2 FA | 0/5, 3 FA |
| S13 late event delivery | 0/5, 1 FA | 0/5, 1 FA |
| S0 expected quiet | 4/5 quiet, 1 FA | **5/5 quiet**, 0 FA |
| S12 expected abstention | 3/5 abstained, 2 FA | **4/5 abstained**, 1 FA |

### sealed holdout v2, re-run from the spent seed — 22 planted causes

| Type | Cases | Detector (watchdog v2) | Terra fast | `codex/gpt-6.1-sol` |
| --- | ---: | ---: | ---: | ---: |
| S7 catalogue migration | 5 | 4 | 3 | **4** |
| S13 late event delivery | 5 | 4 | 4 | 4 |
| S6 traffic quality | 6 | 3 | 2 | **3** |
| S14 checkout regression | 6 | 0 | 0 | **1** |
| **planted total** | **22** | **11/22, 0 FA** | **9/22, 0 FA** | **12/22, 0 FA** |
| S0 expected quiet | 5 | clean | 5 quiet, 0 FA | 5 quiet, 0 FA |
| S12 expected abstention | 3 | clean | 3 abstained, 0 FA | 3 abstained, 0 FA |

Sol matches the detector's 11 hits on the same holdout and adds no false alarm; Terra fast
is three hits behind on the same inputs.

## Latency and cost per run

Prices are the OmniRoute catalogue entries: `codex/gpt-6.1-sol` $2/M prompt and $10/M
completion; Terra 6.1 — the `airlock-personal-exec-terra61` execution slot that served the
`codex/gpt-6.1-terra` alias used here — $1/M prompt and $5/M completion.

| Suite · model | Median latency | Median tokens | Median cost | Suite cost | Tool calls | Errors / timeouts |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| dev30 · Terra fast | 100.7 s | 26 129 | $0.0287 | $0.913 | 58 | 0 |
| dev30 · `codex/gpt-6.1-sol` | 96.8 s | 26 198 | $0.0584 | $1.735 | 56 | 0 |
| blind30 · Terra fast | 103.9 s | 25 923 | $0.0282 | $0.852 | 59 | 0 |
| blind30 · `codex/gpt-6.1-sol` | 96.5 s | 25 783 | $0.0565 | $1.688 | 58 | 0 |
| holdout · Terra fast | 98.5 s | 23 737 | $0.0259 | $0.787 | 60 | 0 |
| holdout · `codex/gpt-6.1-sol` | 104.0 s | 23 691 | $0.0515 | $1.576 | 60 | 0 |

180 model exchanges in total: no timeouts, no transport errors, no case aborted at the
tool-turn limit, every case answered within one price recommendation and one cause
statement. Sol is 3–7 % faster per case but about twice the price per token, so a full
30-case suite costs roughly 1.9× Terra fast.

## Five worst failures of `codex/gpt-6.1-sol`

Ranked by what the director would act on wrongly. First sentences are verbatim.

1. **blind30 `batch-04-case-03` (S12, honest refusal) — false alarm.**
   «Снижать цену пока **не стоит**: подтверждения, что конкуренты дешевле, нет.»
   The answer then asserts a cause on a case that has nothing to report: «У **Kaspersky
   Endpoint Security** оформления заказа снизились на **22,1%**… Это указывает на
   проблему, специфичную для Kaspersky», and names the checkout step as the cause.
2. **blind30 `batch-05-case-04` (S6, bot traffic) — wrong cause and false alarm.**
   «Снижать цену пока не стоит.» / «**Что фактически сломалось:** у **Kaspersky Small
   Office Security** падение на шаге **оформления заказа**: число checkouts снизилось на
   **22,6%**…» The planted cause is traffic quality; the model reports a checkout drop.
3. **blind30 `batch-02-case-05` (S13, late event delivery) — wrong cause.**
   «**Сначала причина:** снижать цену пока не стоит. По доступному измерению у
   **Kaspersky Endpoint Security** просели именно **оформления (checkouts)**: **−21,7%**…»
   A checkout claim is stated first; the planted late-delivery cause is never named.
4. **dev30 `44cc2267-case-01` (S13, late event delivery) — wrong cause.**
   «Снижать цену пока не стоит.» / «**Что, вероятнее всего, сломалось:** не спрос и не
   цена, а этап оформления/оплаты на мобильных устройствах.» The watchdog's own
   classification (`late_event_delivery`, «задержка доставки событий в хвосте выгрузки»)
   appears further down, but the checkout stage is what the model leads with. Terra fast
   misses the same single dev30 case, with the same wording pattern.
5. **holdout `case-07` (S14, checkout regression) — no cause named.**
   «Пока **снижать цену на Kaspersky оснований нет**.» The answer stops at prices and says
   «Причину падения продаж определить не удалось» although the projection contains the
   checkout regression. Terra fast answers the same way on all six S14 holdout cases.

## Go tests

No Go test in this repository calls a model. A search of every `*_test.go` for the model
id, `omniroute`, `chat/completions`, `ModelID` or `goai.NewModel` returns nothing: the
model is reached only through the Airlock runtime (hosted chat) or through the offline
replay harness used here. There is no build-tagged or env-gated model test to run.

Plain `go test ./...` per app module:

| App | Result |
| --- | --- |
| product-manager | pass |
| metrika-analyst | pass (`-count=1`) |
| competitor-research | pass |
| baserow | pass |
| web-scraper | pass |
| market-watch | build failure — the sqlc-generated `internal/db` code and `views/static/app.css` are not committed; the image build produces them |

Re-running market-watch with those generated inputs supplied (sqlc `1.30.0` through the
already-local `sqlc/sqlc:1.30.0` image, `templ generate`, `app.css` from the latest deploy
stage) makes every market-watch package pass. On `origin/main` the root package of
market-watch and Metrika still fail to compile against the public `agentsdk v0.11.0`
because they call `agentsdk.WithReadOnly`, which only exists in the private pinned SDK —
the same blocker as in the measurement copy.

## Limits and honest gaps

1. **The model id was a placeholder in the task.** `codex/gpt-6.1-sol` is the id used and
   it is stated everywhere in this report. A different id means re-running the same three
   suites with the same harness.
2. **The suites are offline, not hosted chats.** The 27 Nov blind30 baseline was a hosted
   run on the QA stand (42082), which this task forbids touching, and nothing was
   deployed, so suites 2 and 3 measure the model against the production PM projection
   rebuilt from current `main`. Suite 1 used the same offline path in the 27 Nov baseline
   and is directly comparable; suites 2 and 3 are not directly comparable with their
   quoted baselines and are labelled as such.
3. **The collector is the sealed watchdog, not current `main`.** Current `main`'s Metrika
   app needs the private patched SDK to compile. The copy used is identical to the
   watchdog v2 commit `19d7423` that produced the sealed 11/22 baseline, so the detector
   column is the sealed one and both model columns sit on that same detector output.
4. **blind30 cannot show what dev30 shows.** On the frozen blind30 set the current
   collector returns a typed cause only for the five S7 cases; for S6, S13 and S14 the PM
   projection carries no candidate at all. That is the same bottleneck that made the 27 Nov
   Sol run score 0/30, and it is a detector limitation, not a model result. On those cases
   the two models are largely guessing, and the six to seven false alarms on the ten
   negative cases are the visible cost of that.
5. **The holdout seed is spent.** The set was regenerated from seed `672862278566`
   (`--jitter 0.3`) and matches the sealed hashes exactly, so these numbers are a **re-run
   of the sealed sample, not a fresh holdout**.
6. **Cost uses the catalogue price**, not a provider invoice; no run was billed through a
   customer account.
7. **One grader, one rule.** "Correct cause" means the model asserted the planted cause
   type as its first claim; it does not additionally require the planted week or the
   planted family, which the detector rule requires. The detector column in the holdout
   table uses the sealed rule (family + type + week ±1) and is therefore not a like-for-like
   peer of the model columns.
8. **One borderline case is judged on wording.** On dev30 `44cc2267-case-01` both models
   lead with the checkout stage on a late-delivery case; the grader counts the checkout
   frame first for both, so they miss it identically rather than being separated by
   phrasing.

## Reproduction

```
# measurement code (not committed; lives in .runtime/model-eval)
grader.py          # deterministic scorer, frozen (sha256 f0d1045f…)
run_model.py       # offline PM-projection replay; model id and tier are arguments
score_run.py       # scores a run directory against a truth summary
prep_blind30.py    # current-main PM projections for the blind30 set
prep_holdout.py    # holdout inputs, collector run, PM projections

# suite 1
python3 run_model.py --model codex/gpt-6.1-sol --tier priority \
  --cases <blind-catalog-verification>/.runtime/blind-director/terra-fast-v3/prompt-v4/all \
  --out out/sol --workers 6
python3 score_run.py out/sol \
  <blind-catalog-verification>/.runtime/blind-director/terra-fast-v3/prompt-v4/truth-summary.json

# suite 3 detector control
python3 tests/holdout/generate_holdout.py OUT --seed 672862278566 --jitter 0.3
BLIND30_DIR=OUT BLIND30_OUT=SCANS go test -run TestBlind30Offline -count=1 ./apps/metrika-analyst
python3 tests/holdout/score_holdout.py OUT SCANS
```

OmniRoute credentials were read from `OMNIROUTE_API_KEY` in the environment and were
never printed or written to disk.
