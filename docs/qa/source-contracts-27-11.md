# Product Manager source contracts · 27 November 2026

This table records the read-only source calls and the subsequent data-path checks. The
customer workbook snapshot was imported only on demo stand 42080. Stand 42082 was not
redeployed, restarted or rebuilt. Row counts mean observations
in the returned aifence.v1 evidence envelope. Mode a means the background
ReadAppTool path is available. Mode b means data reaches PM only through
manual import. Mode c means the source path is broken or unavailable.

| Source | PM path and operation | rows | Source status | Delivery mode | Evidence |
| --- | --- | ---: | --- | --- | --- |
| Metrika Analyst | background job check_source_watch → compare_periods | 4 | ok | a | .runtime/sol-sessions/sol-stand/compare_periods.json, aifence.v1, four weekly observations |
| Wordstat | background job check_source_watch → compare_demand | 0 | unavailable | a, source connection is unconfigured | .runtime/sol-sessions/sol-stand/compare_demand.json, aifence.v1, warning says the authorized Wordstat connection or folder is unconfigured |
| Google Trends | background job check_source_watch → compare_google_trends | 2 | ok | a | .runtime/sol-sessions/sol-stand/compare_google_trends.json, aifence.v1, two observed search-interest rows |
| Market Watch, one SKU | background job check_source_watch → price_week_delta | 0 comparable pairs, 8 observed snapshot series | partial | a, 42080 workbook snapshot only | 42080 run `952e34b3-8f4b-4fda-8335-21adc889f1e3`; `KL4863RAKFS`; eight `ДЕМО` rows from `Store_Конкурентное_исследование.xlsx`, sheet `03_Цена`, cells `I85:I94` where populated; requested weeks have no same-offer pair |
| Market Watch, family | background job check_source_watch → family_price_week_delta | 0 comparable pairs, 8 observed series | unavailable | a, 42080 workbook snapshot only | 42080 explicit-SKU run `e7a60c40-2d65-4dd8-8a08-555d13e29572`; family discovery run `ca277187-4ce2-4364-9cef-a719900a299a` returned 0 observed series for its indexed 20-SKU candidate set; `KL4863RAKFS`; one dated observation per seller, so no weekly delta is asserted |
| Web Scraper | host publisher → Web Scraper snapshot → Market Watch import → PM market binding | 79,156 imported observations | ok | b, data path is available through the existing PM Market Watch binding | `.runtime/sol-sessions/sol-stand/web-scraper-publish-42082.json`, run `ae964917-61a1-49cb-8267-69551b9898c3`; 32,294 Softline + 31,652 SoftMagazin + 15,210 SoftMap |

## Contract conclusion

The Web Scraper data path is now available through the host publisher, its 42082 snapshot, the Market Watch import route and the existing PM Market Watch binding. The imported workbook snapshot on 42080 contains eight exact KES competitor prices, each visibly labelled `ДЕМО` and carrying a sheet/cell locator in `source_ref`. The workbook has one observation per populated competitor row, so `price_week_delta` and `family_price_week_delta` correctly return zero comparable pairs. A second observation for the same offer is required before a weekly delta can be shown.

The live source-watch record remained owner data on the stand. No redeploy, restart or rebuild was performed on 42082.
