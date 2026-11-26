# AIfence inter-app contracts v1

These contracts are the shared boundary, not a service. Every app builds/deploys independently. Use Go structs conforming to the schemas; keep output small and typed where SDK permits. No dependency on another app's DB or source checkout.

## Tools to implement (names frozen for MVP)

| App | Tool | Input | Output |
|---|---|---|---|
| market-watch | list_offers | product_key, limit | evidence batch |
| market-watch | refresh_market | product_key, source_ids (approved) | evidence batch or native task receipt, with status |
| market-watch | market_changes | product_key, from, to | evidence batch |
| metrika-analyst | query_metrics | product_key, metric_keys, from, to, segment | evidence batch |
| metrika-analyst | compare_periods | product_key, metric_keys, baseline_from, baseline_to, current_from, current_to, segment | evidence batch (baseline/current and derived change with source refs) |
| metrika-analyst | compare_demand | phrase, monday (UTC) | observed Wordstat query counts for two complete weeks, or unavailable |
| metrika-analyst | check_metrika | configured counter | status, observed source and timestamp; never include token |
| product-manager | save_hypothesis | hypothesis object, expected_version | id/version/status |
| product-manager | send_to_backlog | hypothesis_id, confirmed, idempotency_key | task id, url, exact tag |
| product-manager | evaluate_hypothesis | hypothesis_id | hypothesis status, measured evidence, reason |

Tools returning an asynchronous result return exact job/task ID with honest queued/running/ready; if not ready do not use as evidence. Keep tools read-only unless their names describe an authorized mutation.

Schemas: evidence-v1.schema.json and hypothesis-v1.schema.json. Dates are ISO YYYY-MM-DD; UTC as_of. Period ends inclusive. Ratio = 0–1; relative_change_pct = percent (e.g. -12.5); percentage-point change unit pp. Currency ISO 4217; amounts in base currency units, specify tax basis. Include numerator/denominator/sample_size when known. Missing != zero. Baseline and current observations link to raw rows or exact API query description; include data collection breaks.

Canonical product_key: vendor/family/edition/license model/term/seats or SKU; data owner maintains normalization mapping with provenance. Unknown SKU match must stay unknown/estimated; never silently compare unlike products. Offer details should include seller, URL, edition, term, seats, region, currency, tax basis, captured_at, match_status. Sales index != revenue/volume; no absolute profit without sufficient inputs.

Hypothesis links actual IDs, competing explanation, next cheapest distinguishing test, acceptance rule. Economic effect status unknown unless data support calculation; scenario assumptions explicit. Exact backlog tag: `сгенерирована ИИ-помощником`. Idempotency key stable per hypothesis/action and scoped to current workspace; same repeated command returns same task.

Contract changes: integration owner updates schema+synthetic fixtures and all tracks agree before editing. JSON fixture is synthetic, not partner data.

## Weekly signals and browser checks

`detect_signal` returns the decision for the week containing the end date. `replay_signals` returns a series of the same decisions. Both use the sales evidence envelope constrained by `signal-v1.schema.json`. An observed Boolean records whether the threshold was crossed. A null decision includes the reason the week could not be evaluated. Each decision identifies the target, control, windows and underlying source rows.

Use `source=browser`, `kind=path_check` for a dated device-specific check. The hypothesis may link these rows in `browser_evidence_ids`, name alternatives in `competing_explanations`, record a `forecast`, and set `evidence_level` to `signal_only`, `correlation` or `path_verified`. A current browser observation can establish today's behavior. Attributing a historical drop or measuring recovery requires evidence for its own period.
