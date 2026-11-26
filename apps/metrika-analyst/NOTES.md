# Decisions
- No custom Yandex API client, metric database, scheduler or import service.
- Native Airlock MCP declaration uses a dedicated, private expremiental server pinned to its reviewed commit. DevCrabs would require a separate transport bridge and local OAuth/keychain lifecycle.
- Historical data stays in authorized Airlock files. Only exact evidence-v1 aggregates are interpreted by deterministic tools; source selection is explicit. Data owner supplies normalized aggregates and provenance.
- Store-wide live scope, completed days and equal windows for MVP. Currency RUB; e-commerce instrumentation must be confirmed. No SKU attribution, no claim of causality.
- UI and typed tools use the same service. App cannot be marked deployed until actual Airlock host, MCP binding and end-to-end test are available.
