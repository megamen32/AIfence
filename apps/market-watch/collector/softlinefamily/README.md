# Softline software catalog

The Go command `collector/cmd/softlinefull` enumerates Softline's published family sitemap and every softwareProducts sitemap from the current index. It reads the anonymous family-page license model, including individual license links, SKU where supplied, quantity tiers and per-unit prices. It compares the observed links with the full software-product URL inventory and visits remaining published product pages individually. A 404 or 410 is recorded separately from a live offer. Prices in JSON are integer kopecks. An empty manufacturer SKU is left empty.

From `apps/market-watch` on the M1:

```sh
go build -o ../../.runtime/bin/softlinefull ./collector/cmd/softlinefull
go build -o ../../.runtime/bin/market-coverage ./collector/cmd/coverage
../../.runtime/bin/softlinefull -out ../../.runtime/market-catalog/softline
../../.runtime/bin/market-coverage -catalog ../../.runtime/market-catalog
```

The Mac LaunchAgent runs the scanner every three hours. A lock prevents overlapping passes. An unfinished generation resumes from its page checkpoints. A finished generation leaves its checkpoints in place, and the next invocation starts a fresh sitemap inventory. The latest known price can remain visible while a newer scan is in progress. Its original observation time is retained.

`softline/inventory.json` gives the unique product URL denominator, current sweep observations by state, unresolved coverage and whether the sweep is complete. A public product without a listed price has no price observation. The number of priced offers can differ from the number of sitemap entries. A full sweep can take longer than three hours. Use captured_at to check the freshness of each price.

The merged `coverage.csv` includes known product URLs, family pages, discovery or observation status, published prices and source timestamps across all three shops. Filter the page type to avoid counting a family page and its child variants as distinct licenses. The `offers.csv` file contains the priced rows used for comparison.
