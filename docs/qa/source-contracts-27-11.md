# Product Manager source contracts · 27 November 2026

This table records the read-only source calls already made on demo stand
42082. The stand was not changed during this check. rows counts observations
in the returned aifence.v1 evidence envelope.

| Source | PM path and operation | rows | Source status | Delivery mode | Evidence |
| --- | --- | ---: | --- | --- | --- |
| Metrika Analyst | background job check_source_watch → compare_periods | 4 | ok | background ReadAppTool works | .runtime/sol-sessions/sol-stand/compare_periods.json, aifence.v1, four weekly observations |
| Wordstat | background job check_source_watch → compare_demand | 0 | unavailable | background ReadAppTool call works, source is unconfigured | .runtime/sol-sessions/sol-stand/compare_demand.json, aifence.v1, warning says the authorized Wordstat connection or folder is unconfigured |
| Google Trends | background job check_source_watch → compare_google_trends | 2 | ok | background ReadAppTool works | .runtime/sol-sessions/sol-stand/compare_google_trends.json, aifence.v1, two observed search-interest rows |
| Market Watch, one SKU | background job check_source_watch → price_week_delta | 0 | unavailable | background ReadAppTool call works, requested weeks have no stored observations | .runtime/sol-sessions/sol-stand/price_week_delta.json, nested aifence.v1 evidence with zero observations |
| Market Watch, family | background job check_source_watch → family_price_week_delta | 0 | unavailable | background ReadAppTool call works, no comparable numeric pairs | .runtime/sol-sessions/sol-stand/family_price_week_delta.json, nested aifence.v1 evidence with zero observations |
| Web Scraper | no PM binding or source-watch operation | — | blocked | broken for this stand | docs/qa/scenes-27-11.md records access denied for the available Web Scraper route |
| Baserow | no PM source-watch binding | — | not used | manual import only if an exported evidence document is supplied | apps/product-manager/main.go registers no Baserow source. PM exposes /import and import_evidence for authorized evidence documents |

## Contract conclusion

The demo PM can obtain Metrika Analyst, Wordstat, Google Trends and Market Watch
data through the source-watch background job and its owner-scoped
ReadAppTool bindings. The two successful calls prove returned evidence rows.
The three unavailable results preserve the source status and warnings instead
of turning missing observations into zeros.

The live source-watch record remained owner data on the stand. No source,
watch, binding, or application configuration was changed for this table.
