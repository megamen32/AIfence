# Architecture v1 — sprint baseline

Goal: increase store profit/margin by locating a supported opportunity, checking a hypothesis, recording a decision and measuring effect after release. Exact metric/allowed actions remain adjustable by the product owner.

## Three products, not five services

**Market Watch**: independent competitive intelligence. Add a product and competitor/source; read matched offers/features with date, source and confidence; compare changes. MVP: three approved public stores, a complete discovery inventory, and exact-SKU comparisons on observed variants. Each source uses its own configuration or family-page adapter. Prefer HTML/API/structured data with browser for dynamic pages; obey access conditions, rate limits, robots and authorization. Keep old snapshots; no hallucinated matches or bypass of access restrictions.

**Metrika Analyst**: independent data analyst. Read authorized Reports API, test on permitted test counter; import historical aggregates separately, settings define goals/period/segment/threshold and known breaks. Calculate metrics, comparable windows, sample sizes and missing data. Return bounded structured observations, not prose-only reports.

**Product Manager**: independent user value = decision and learning loop even without live collectors (imported evidence supported). One chat uses existing cross-app capability discovery; consumes evidence from both apps, research and sales/backlog files. Owns hypothesis record and idempotent task creation in native storage or configured external tracker. No direct edits to competitor/metric source data. Human-approved changes. Fix criterion before release; evaluate only when observations mature. Do not claim that temporal correlation is cause.

## Existing platform capabilities

Airlock owns user auth, assistant loop, model slots, storage, cross-app calls, jobs/scheduling. Existing tracker/mail and browser are adapters, not services bundled by default. A real tagged prototype backlog in PM native DB is acceptable fallback until customer tracker access; label it clearly and allow later adapter. Send one notification per material new signal using configured channel. For MVP research import/search can be deterministic keyword/index with source locators, no embedding service required. Reuse native UI, avoid a new React SPA.

## Data flow

Inputs -> per-domain observations (contract aifence.v1) -> one hypothesis with source IDs -> approve -> one tagged backlog task -> actual release date and criterion -> comparable observations -> decision. Missing data is explicit null with reason. Source references include file/row/page or public URL and version. Keep upstream originals unchanged; do not ingest held-out answer/ethalon sheet into model or analysis input.

Use the approved partner materials in the closed environment; public fixtures remain wholly synthetic. Do not depend on internal personal Airlock installations or private code. Where Airlock SDK cannot reach a resource, report unavailable and use explicitly labeled local historical import; don't simulate real external integration as success.

## Scope for 48 hours

0–6 h: shared contract, source/access validation, live platform and minimal typed tools.
6–18 h: market + metrics + imports work; PM builds on synthetic contracts in parallel.
18–30 h: integrate one real use case including task creation; second independent test.
30–38 h: follow-up evaluation, settings, notification, basic error handling.
38–48 h: freeze functionality, fix acceptance blockers, measure request cost/time, prepare one-take demo and public-safe source.

Not in MVP: universal parser of all websites, automatic price changes, model fine-tuning, replacement CRM/Jira, auto deployment of arbitrary code from web pages, five autonomous agents, fake post-release success.

## Shared knowledge: use the native Airlock file catalog

Use `platform.files` with the same authorized workspace/user for all three applications. It already supports list/search/read/write, stable file IDs, and content-version compare-and-swap. Store shared research, derived evidence and investigation briefs as versioned documents with source references; use native app storage for typed operational state and idempotency. A shared file catalog is not proof of automatic cross-app semantic memory. Do not import the owner's personal AgentMemory/Hindsight banks or credentials into the contest workspace. Only authorized partner material is available in the closed project environment. No additional memory service is required for the MVP.
