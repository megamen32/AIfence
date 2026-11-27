# Implementation decisions

- Scope: standalone apps/google-trends in the external-demand worktree. No changes to AIfence core, Wordstat, main, global MCP settings, accounts, billing, or common infrastructure.
- Ready upstream: official HasData hosted Google Trends MCP. Its current endpoint is /mcp?apis=google_trends. The npm @hasdata/google-trends-mcp 1.0.1 wrapper used an older endpoint and interpolated the key into child argv, so the launcher uses its ready mcp-remote dependency directly at the verified 0.1.43 pin with an environment placeholder.
- Raw MCP on Mac is intentionally separate from normalized app tools. The launcher does not invent or rename provider tools. The typed service has stricter input and interpretation rules.
- Auth: owner-only ignored file for the raw launcher; native MCPAuthToken + shared resource + x-api-key injection for Airlock. No fake keys, no fallback to unauthenticated data calls, and no secrets in source or argv.
- Comparison: one source call for all terms. No per-term fetching, normalization, aggregation across calls, or retries. Request fingerprint and response comparison ID serve distinct purposes.
- Time: UTC, exact requested dates, default last 12 complete calendar months. Returned weekly labels are retained even when a bucket crosses a selected date boundary.
- Parser: supports MCP text/structured content, provider status/json envelopes, the documented geoMap shape, and tri-state quality flags. HTTP/provider errors are distinct from empty data. Numeric values outside 0–100 are rejected for indices, not clamped.
- Related queries: top index and rising growth are different measures. Breakout is a category even if extractedValue is numeric.
- UI: compact analysis form, default light/dark DaisyUI themes, system fonts, semantic colors. Functional controls remain outside the standalone-only identity header. The result is the only htmx swap target. The chart breaks lines on missing, unknown, partial, or censored observations; the table retains all rows and source flags.
- No persistence or background jobs are needed. No new orchestrator is introduced.
- Public method instructions forbid treating search interest as sales, market share, customers, or causal funnel evidence. Source material and private metrics are not embedded.
- Verification: offline fixtures cover protocol/envelope status, zero vs missing, censored values, unknown flags, partial intervals, common cohort means, regional payloads, Breakout, date validation, redaction, and one-call comparison behavior. One real source request was separately authorized and saved privately.
