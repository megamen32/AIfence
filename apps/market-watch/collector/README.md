# Market Watch catalog collector

The Go package supplies source rules, public HTTP fetching, price extraction
and CSV serialization to the native Airlock app and the local catalog workers.
Money is stored in integer kopecks. Each price includes its source URL, original
capture time, quantity limits when supplied by the publisher, and price basis.
SoftMap's visible total and a page showing only a minimum price retain their
respective bases. Products without a manufacturer SKU remain searchable by name
and URL. Cross-store SKU matching requires an actual code.

## Run on macOS

From the checkout root in a logged-in macOS session with Go available, run
`python3 apps/market-watch/collector/install-macos.py`. The three collectors
start at login and every 10,800 seconds. Each source has a lock and durable
progress. A long run resumes its generation. The exporter writes
`.runtime/market-catalog/offers.csv` atomically every minute for Excel or other
importers. It uses UTF-8 BOM, a semicolon delimiter and plain decimal prices.
The separate `coverage.csv` checks known URLs, including removed pages and
licenses available by request. Its scheduled rebuild runs every three hours.

SoftMagazin's family sitemap also contains hardware. The collector follows
the store's own software sections and their published pagination, adds newly
listed family URLs, and verifies canonical family or standalone license pages.
A price enters the software snapshot after explicit software-category evidence.
Hardware URLs verified against the merchant's markup are recorded in
`softmagazin/excluded.jsonl`. A published category can supply prices while
the family awaits direct verification. Its record includes `evidence_url`.

SoftMap's child URLs are matched to license rows on the parent page. Missing
children are requested individually. The original inventory, page audit journal,
progress and priced rows reside in each source's directory. A new generation
keeps previous observations with their capture times until it refreshes them.
HTTP access restrictions, publisher rate limits, permanent removals, temporary
failures and unrecognized templates retain distinct states. HTTP starts are
paced independently per source at up to one request per second, respecting
robots.txt and Retry-After. Transient page failures receive bounded retries.

## Developer commands

From `apps/market-watch` run:

```sh
go test ./collector ./collector/cmd/fullcatalog
go run ./collector/cmd/catalog -config collector/sources.json -out /tmp/aifence-sample
go run ./collector/cmd/fullcatalog -source softmagazin,softmap -out ../../.runtime/market-catalog -refresh-after 3h
../../.runtime/bin/catalogexport -dir ../../.runtime/market-catalog
../../.runtime/bin/market-coverage -catalog ../../.runtime/market-catalog
```

The bounded `cmd/catalog` entry point collects configured seed pages by default.
With `-discover -max-pages N` it includes sitemap candidates under a per-source
budget and writes dated manifests. For a fourth source, add an owned host,
sitemap and URL filters, seed URLs and row/field selectors in `sources.json`.
Add fixtures for official software categories, price basis and quantity tiers
when the new store's catalog uses different templates. The domain parser accepts
HTML and an injected capture time. Network and persistence remain in the
worker and Airlock integration.
