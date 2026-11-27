# Market Watch

A native Go application for Airlock. The signed-in app members can query public prices by title, SKU or product URL, optionally filtering by store, price basis and quantity tier. Every observation carries its source URL and capture time. The app owns its PostgreSQL schema.

| Chat tool | Result |
| --- | --- |
| `list_offers` | A page of up to 100 matching indexed offers, optionally filtered by store, license words in the title, exact SKU or price basis |
| `compare_offers` | Groups of observations for an exact SKU with original terms and a link to the same comparison in the web UI |
| `crawl_coverage` | Latest dated reconciliation of unique license sitemap URLs with price, missing price, unavailability, exclusion, unresolved and pending counts where the denominator is known |
| `catalog_status` | Indexed offer counts and latest observation timestamps per store |
| `add_competitors` (admin) | Enable the vetted Allsoft and Syssoft profiles, persist activation, collect a bounded real sample via robots-aware fetch, and include them in normal refreshes |
| `list_competitors` | Verify activation, observations, seed URLs and errors; these profiles have no full sitemap inventory |
| `refresh_market` | Refresh featured pages and previously observed URLs for a known exact SKU |
| `market_changes` | Price changes on comparable observations of the same store, URL, SKU, price basis and quantity tier |
| `price_week_delta` | Paired stored prices for one exact SKU in two complete UTC weeks, with missing periods |
| `family_price_week_delta` | Paired prices for explicit or title-discovered SKUs, with coverage denominators and evidence IDs |

The authenticated route `GET /catalog.csv` streams the full current index as an Excel-compatible CSV. Catalog counts measure observed offers. The Sources tab displays these counts alongside a separately dated reconciliation of license URLs. An unknown license inventory remains explicitly unknown.

The main route provides Catalog, Comparison, Price history and Sources. The catalog applies filters to the entire index before returning a page of 25 rows. Each row links to its original store page and related observations. Share a comparison with `/?tab=compare&q=KL4141RCEFS`. History uses inclusive UTC dates and consecutive price observations for the same offer identity.

## Local Airlock on Mac M1

Run from this directory. Create the app once and update later using the same remote and binding.

```sh
go tool air build
go tool air login http://localhost:42080
go tool air deploy --remote default --target production --create --name 'Market Watch' --slug market-watch -m 'Install Market Watch' --wait
go tool air status
```

For updates omit the creation flags. Start a stopped runtime with `go tool air start --target production`.

## Import public observations

The Mac collector atomically publishes one `<store>/offers.jsonl` file per source. The importer reads these files and submits new observations to the authenticated app administrator route. Each batch is an idempotent SQL statement that stores history and updates current offers. A checkpoint records receipts only after the server acknowledges a batch. Use a separate checkpoint for each app deployment.

```sh
go build -o ../../.runtime/bin/market-air github.com/airlockrun/agentsdk/cmd/air
go build -o ../../.runtime/bin/importcatalog ./cmd/importcatalog
../../.runtime/bin/importcatalog -root ../../.runtime/market-catalog -air ../../.runtime/bin/market-air -app-dir . -checkpoint ../../.runtime/market-catalog/local-market-watch.uploaded
```

On the local Mac deployment, the existing three-hour import also publishes the sitemap reconciliation when all three collector checkpoints exist. Run `go run ./cmd/coverageimport` to inspect the current snapshot without uploading. Run `go run ./cmd/coverageimport -upload -air ../../.runtime/bin/market-air` to upload only that report. Schedule the importer after refreshed observations are published. Exact SKU matching requires a SKU from the store. Offers without a SKU can be found by title and URL. Minimum and maximum quantities are preserved for comparisons.

A bounded Mac refresh is `go run ./collector/cmd/feedfirst -root <existing-feed-root> -out <separate-slice-root> -skus KL4141RCEFS -max-pages 3`. It revisits exact URLs already in the feed through the collector's robots and pacing policy, writes a separate per-store `offers.jsonl` for the ordinary admin `POST /import`, and reports requested SKUs, pages, new offers, missing SKUs and failures. `-family` can replace `-skus` to discover up to 20 SKUs from existing offer titles. Use a checkpoint specific to the selected app deployment when importing this slice.

Both weekly read tools take `baseline_from`, `baseline_to`, `current_from` and `current_to` for complete Monday-to-Sunday UTC weeks. `price_week_delta` also takes `product_key`. `family_price_week_delta` takes either `skus` or `family`, a title phrase whose returned SKU list is a set of indexed candidates. Its response contains `results[]` by SKU, `coverage` with selected SKU and observed-series denominators, `evidence_ids[]`, and `evidence` in the shared `aifence.v1` shape. Each numeric observation links its two source readings in `details.evidence_ids`. Absolute prices stay within their own SKU, seller, URL, basis and quantity tier.
