# Catalog extractor — Market Watch core
Apify-inspired, not an Apify clone. One configuration per source; sitemap
enumeration -> bounded queue -> HTML extraction -> exact manufacturer SKU
and integer RUB kopecks -> CSV + immutable run manifest. Airlock owns schedules,
authentication, storage, and user-facing tools. This directory is a standalone
Go module for the core/worker; no extra server or orchestration platform.

## Run
```sh
go test ./...
go run ./cmd/catalog -out /tmp/aifence-catalog
go run ./cmd/catalog -discover -max-pages 20 -out /tmp/aifence-catalog
```

Default is a small known-page smoke test. Sitemap mode is intentionally
bounded, not a claim of full catalog coverage. Seed pages precede sorted
candidates. No scraping search paths, no bypass, redirects not followed,
one request/sec per host (or robots Crawl-delay), no automatic retries.
The real HTML is public; validate current rules/access conditions before
large scheduled collection. The current candidate rules do not yet distinguish
all software from hardware/category pages. A nonmatching page is counted empty;
ambiguous prices on an exact SKU fail the run rather than being guessed.
A successful sample writes sample.csv, not catalog.csv. Failed, partial or
page-limited runs do not overwrite catalog.csv. Each run emits dated CSV
(if offers exist) and JSON manifest. CSV is UTF-8 BOM with semicolons, usable
in Excel and importable elsewhere. SoftMap uses the displayed total, not the per-unit price hidden in the cart parameter; the selected quantity is exported. Other adapters currently mark quantity unknown. Price is a publicly displayed amount,
not a guaranteed offer; taxes, volume discounts and final terms are unknown.
No secret, purchase or private partner data is required.

New site: add a source in sources.json (host, sitemap filter, URL pattern,
row/field selectors, seed pages); add a fixture test. Extend the adapter only
if its HTML differs materially. Keep normalization/extraction pure and inject
network fetching via Fetch. Three-hour scheduling and persistent per-run
checkpointing for big catalogs are Airlock integration work, not a second
scheduler here. Before a full production crawl, add coverage checks, page
classification and incremental queues. The approved Go SDK means the host
can reuse this code without a Rust subprocess or separate service. The CLI
currently uses a local filesystem, not the deployed Airlock native storage.

## Full public catalog on macOS

Run `python3 apps/market-watch/collector/install-macos.py` from a logged-in
macOS user session with Go on PATH. This installs the collectors for
SoftMagazin, SoftMap and Softline as local per-user launch agents. Each runs at
login and every 10,800 seconds. A source lock protects an unfinished generation.
The joined Excel-compatible UTF-8 CSV is written to
`.runtime/market-catalog/offers.csv`. Prices are RUB decimals, the delimiter
is a semicolon, and an empty SKU means the publisher supplied no code.

The two competitor workers use their public software sitemaps. The journal
`<source>/pages.jsonl` checkpoints each visited page with its URL, capture time,
license rows and extraction state. SoftMap's child URLs are reconciled to
offers on family pages. Missing children are requested individually. A denied
or removed page remains a separate observation. A template without recognized
prices remains explicit. `progress.json` distinguishes checked families from
pending variants. A new generation rescans the current sitemap and retains
older observations with original capture times until refreshed. A temporary
error retains the previous priced rows. Atomic per-source snapshots feed the
joined CSV every minute and the URL coverage export every three hours.

For an immediate manual collection from the app directory, run
`go run ./collector/cmd/fullcatalog -source softmagazin,softmap -out ../../.runtime/market-catalog -refresh-after 3h`.
To refresh the local files immediately after a change, run
`../../.runtime/bin/catalogexport -dir ../../.runtime/market-catalog` and
`../../.runtime/bin/market-coverage -catalog ../../.runtime/market-catalog`.
