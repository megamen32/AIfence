# Decisions
- BoxLab yandex-metrica-mcp 1.0.1 + Supergateway 4.3.0; both are upstream packages. No custom Yandex API client or transport adapter.
- Expose only six aggregate read tools; separate gateway authentication; dedicated customer instance/token. Raw event exports and write tools excluded from MVP.
- Live comparisons use BoxLab run_comparison. Typed application tools normalize evidence-v1; historical exact aggregates use local deterministic arithmetic. No database, scheduler or import service.
- INSTRUCTIONS.md is embedded with native SDK AddInstruction, not a runtime skill loader.
- Store-level metrics, explicit verified native filters, completed dates, RUB for MVP; no unsupported SKU or unverified checkout metric. Product impressions/cart quantities are not unique users.
