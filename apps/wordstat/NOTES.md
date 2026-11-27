# Wordstat decisions

Standalone AIfence app on agentsdk v0.11.0. Product managers get three typed read-only demand tools; overall product investigation stays with the PM orchestrator.

MCPAuthToken + BindingShared remain the native connection scheme. The public endpoint is embedded from mcp-endpoint.txt. There are no runtime reads or network calls during newAgent registration. The Mac launcher uses the ready Supergateway bridge and a protected ignored runtime bearer. Yandex credentials remain at the remote provider.

Official documentation checked 2026-11-27 limits API statistics to dates through 2026-10-07. Monthly requests after 2026-09 return unavailable_period without changing the request. The actual top/regions window remains unknown. Original tool descriptions still say last 30 days; the app instruction and source metadata carry the documented limit.

Regional names come from the same bound list_regions resource. Parse id, label and children recursively; cache a successful directory for the service lifetime, share concurrent requests, retry a failure on a later request. Statistics and lookup share one 55-second deadline. Ambiguous IDs remain unresolved. Preserve raw data/count types; rows without counts remain only in raw data.

Directory failure or partial resolution retains statistics. Answers finish with codes and a warning, without other MCP or web lookup. Nested territories and overlapping phrases are not additive. Omitted region_ids means no region filter, regardless of the UI's initial selection.

UI and typed tools share demand.Service. No domain background jobs or persisted state. Tests cover explicit human callers, denied nonhuman access, real zero, missing counts, lookup failure, ambiguous IDs, concurrent cache use and exact shared deadlines.

Source provenance: isolated from the separately developed standalone Wordstat application under apps/wordstat. No private Airlock core or organizer materials were copied. Toolchain/generated output, credentials and runtime state remain outside tracked source.

Chat QA 2026-11-27: the unchanged general-demand prompt produced an invalid sum across overlapping commercial phrases and inferred purchase intent from download wording. Instructions now prohibit all sums across main/associated phrase counts, including commercial clusters; download alone is not buying intent. A general-demand answer should use 3-5 short paragraphs with phrase frequency, actual coverage, period and source limits, without unsolicited cluster totals or a top-10 table.
