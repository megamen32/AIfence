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

## Feed-first and comparison-first collection

Use the publisher's authorized software price export before crawling standalone
product pages. The official Softline price-list directory is
https://store.softline.ru/pricelists/. Obtaining a file must follow the
publisher's ordinary permission/consent process; this worker does not solve
CAPTCHAs, submit contact data or guess private endpoints.

\`softlinefull -verified-batch /private/path/batch.json\` is a normalization
boundary for an operator-verified source, not an Excel downloader. Supply
\`evidence_url\` on the approved Softline host, \`observed_at\` (RFC3339), and
\`offers\` using the existing catalog Offer JSON schema. Each offer must have an
exact software sitemap URL, RUB price_minor > 0, per_unit basis, a verified
minimum quantity, and the same observed timestamp. Do not transform a dated
wholesale list or estimated price into a current public store price. Actual
XLSX column mappings must be validated against a real authorized export.
The batch is retained inside the active checkpoint generation and fills
unresolved product URLs only; resolved family/card evidence takes precedence.
A missing SKU is still missing and a non-matching URL is rejected.

Without a bulk export the worker preserves the complete sitemap queue, but
first visits pending URL slugs matching observed competitor SKUs (two stores
before one). This is a scheduling hint, never a product match. The actual
card still needs to establish its license and price terms.

Five in-flight reads share the existing per-host start interval (at least one
second, or the longer robots.txt Crawl-delay), stop on 403/429/503, and have
one checkpoint writer. Successful family responses and unresolved product
responses are compressed and kept locally under the existing checkpoints,
with SHA-256 tied to the original observation. A later parser version can
re-read the retained evidence without another HTTP request or a new timestamp.
The store archive is capped at 8 GiB; hitting the cap is logged and never
changes an unresolved state into a completed one. The active worker remains
single-owner and resumes using the existing checkpoint format.
