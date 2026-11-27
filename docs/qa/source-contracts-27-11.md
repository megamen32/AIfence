# Product Manager source contracts · 27 November 2026

This table records the read-only source calls and the subsequent data-path check on demo stand
42082. The stand was not redeployed, restarted or rebuilt. rows counts observations
in the returned aifence.v1 evidence envelope. Mode a means the background
ReadAppTool path is available. Mode b means data reaches PM only through
manual import. Mode c means the source path is broken or unavailable.

| Source | PM path and operation | rows | Source status | Delivery mode | Evidence |
| --- | --- | ---: | --- | --- | --- |
| Metrika Analyst | background job check_source_watch → compare_periods | 4 | ok | a | .runtime/sol-sessions/sol-stand/compare_periods.json, aifence.v1, four weekly observations |
| Wordstat | background job check_source_watch → compare_demand | 0 | unavailable | a, source connection is unconfigured | .runtime/sol-sessions/sol-stand/compare_demand.json, aifence.v1, warning says the authorized Wordstat connection or folder is unconfigured |
| Google Trends | background job check_source_watch → compare_google_trends | 2 | ok | a | .runtime/sol-sessions/sol-stand/compare_google_trends.json, aifence.v1, two observed search-interest rows |
| Market Watch, one SKU | background job check_source_watch → price_week_delta | 0 | unavailable | a, requested weeks have no stored observations | .runtime/sol-sessions/sol-stand/price_week_delta.after-import.json, run `35a7df14-ed89-429f-8ce3-a4d6ccebae7e`; current Market Watch exact-SKU read has 4 rows in the 2026-11-27 week only |
| Market Watch, family | background job check_source_watch → family_price_week_delta | 0 | unavailable | a, no comparable numeric pairs | .runtime/sol-sessions/sol-stand/family_price_week_delta.after-import.json, run `08f8881d-5122-49c2-8b1c-43b9bedfacba`; 2 SKUs selected, 4 missing SKU-weeks |
| Web Scraper | host publisher → Web Scraper snapshot → Market Watch import → PM market binding | 79,156 imported observations | ok | b, data path is available through the existing PM Market Watch binding | `.runtime/sol-sessions/sol-stand/web-scraper-publish-42082.json`, run `ae964917-61a1-49cb-8267-69551b9898c3`; 32,294 Softline + 31,652 SoftMagazin + 15,210 SoftMap |

## Contract conclusion

The Web Scraper data path is now available through the host publisher, its 42082 snapshot, the Market Watch import route and the existing PM Market Watch binding. The imported data contains current KES-family observations. The requested weekly windows still have no baseline week, so `price_week_delta` and `family_price_week_delta` correctly return zero comparable pairs. A second complete comparable week requires another dated scraper observation window.

The live source-watch record remained owner data on the stand. No redeploy, restart or rebuild was performed on 42082.
