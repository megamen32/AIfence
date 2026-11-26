# Market Watch

A native Go application for Airlock. The signed-in app members can query public prices by title, SKU or product URL, optionally filtering by store, price basis and quantity tier. Every observation carries its source URL and capture time. The app owns its PostgreSQL schema.

| Chat tool | Result |
| --- | --- |
| `list_offers` | Up to 100 matching indexed offers |
| `catalog_status` | Indexed offer counts and latest observation timestamps per store |
| `refresh_market` | Refresh featured pages and previously observed URLs for a known exact SKU |
| `market_changes` | Price changes on comparable observations of the same store, URL, SKU, price basis and quantity tier |

The authenticated route `GET /catalog.csv` streams the full current index as an Excel-compatible CSV. Catalog counts measure observed offers. Crawl progress and outstanding pages are tracked by the collector.

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

Schedule the importer after refreshed observations are published. Exact SKU matching requires a SKU from the store. Offers without a SKU can be found by title and URL. Minimum and maximum quantities are preserved for comparisons.
