# Architecture v1 — sprint baseline

Goal: increase store profit/margin by locating a supported opportunity, checking a hypothesis, recording a decision and measuring effect after release. Exact metric/allowed actions remain adjustable by the product owner.

## Applications and roles

**Market Watch**: independent competitive intelligence. Add a product and competitor/source; read matched offers/features with date, source and confidence; compare changes. MVP: three approved public stores, a complete discovery inventory, and exact-SKU comparisons on observed variants. Each source uses its own configuration or family-page adapter. Prefer HTML/API/structured data with browser for dynamic pages; obey access conditions, rate limits, robots and authorization. Keep old snapshots; no hallucinated matches or bypass of access restrictions.

**Metrika Analyst**: independent data analyst. Read authorized Reports API, test on permitted test counter; import historical aggregates separately, settings define goals/period/segment/threshold and known breaks. Calculate metrics, comparable windows, sample sizes and missing data. Return bounded structured observations, not prose-only reports.

**Product Manager** is the only application that orchestrates investigations. It uses evidence from source modules or imported snapshots and owns the hypothesis, decision and measurement loop. It records tasks idempotently in native storage or a configured tracker after human approval. Set the success criterion before release and evaluate it when comparable observations are available. Use release timing to decide what to investigate and verify any proposed cause with independent evidence.

## Existing platform capabilities

Airlock provides authentication, the assistant loop, model slots, storage, scheduling and file access under the current user’s grants. The applications exchange evidence through the user’s file catalog. The collection workflow uses read-only source tools in the hosted user context. Tracker, mail and browser adapters can be configured separately. Until tracker access is available, Product Manager can use a clearly labelled native backlog with tagged tasks. For research, use a deterministic search or index with source locators.

## Data flow

1. In the hosted Airlock conversation, [`workflows/collect-evidence.js`](../workflows/collect-evidence.js) runs as a JavaScript body in ordinary `run_js` under the current user’s grants. It collects read-only observations from the Market Watch and Metrika Analyst source modules and includes selected authorized evidence files.
2. The workflow writes and reads back a versioned snapshot in the shared Airlock file catalog. It returns the file ID and content version.
3. The hosted conversation reads the snapshot from that catalog and passes its JSON to `Product Manager.import_evidence`. Product Manager also accepts authorized snapshots prepared offline. The stand recorded a catalog file version and a matching import receipt for a partial collection. [Server workflow verification](qa/watchdog-chat.md).
4. Product Manager links source IDs to the hypothesis. After approval it records one tagged backlog task. It evaluates the release against the agreed criterion when comparable post-release observations are available.

Missing data is explicit null with a reason. Source references include file, row or page, or a public URL and version. Keep source originals unchanged and keep held-out answer sheets outside the analysis inputs.

Use the approved partner materials in the closed environment; public fixtures remain wholly synthetic. Do not depend on internal personal Airlock installations or private code. Where Airlock SDK cannot reach a resource, report unavailable and use explicitly labeled local historical import; don't simulate real external integration as success.

## Scope for 48 hours

0–6 h: shared contract, source/access validation, live platform and minimal typed tools.
6–18 h: market + metrics + imports work; PM builds on synthetic contracts in parallel.
18–30 h: integrate one real use case including task creation; second independent test.
30–38 h: follow-up evaluation, settings, notification, basic error handling.
38–48 h: freeze functionality, fix acceptance blockers, measure request cost/time, prepare one-take demo and public-safe source.

Not in MVP: universal parser of all websites, automatic price changes, model fine-tuning, replacement CRM/Jira, auto deployment of arbitrary code from web pages, five autonomous agents, fake post-release success.

## Shared Airlock file catalog

The hosted workflow and subsequent import use the same authorized workspace and user. Keep shared research, evidence snapshots and investigation briefs in versioned files managed through `platform.files`. Keep operational state and idempotency records in native application storage. Personal memory banks and credentials stay outside the contest workspace. Authorized partner material stays in the closed project environment.
