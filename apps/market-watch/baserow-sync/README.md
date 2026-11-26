# Baserow catalog projection

The local sync reads the authenticated `/matrix.csv` Market Watch route and writes one comparison table to the existing signed-in user's Market Watch database in Baserow. Both calls use the local Airlock SDK. Prices, equivalence, seven-day changes, and comparison links come from the same Market Watch service as the web UI. Baserow stores a one-way projection. Edits to managed prices are replaced on the next synchronization. Extra annotation fields and manually created rows are preserved.

The primary cell is a native Baserow link: its label is the license name and its destination is the matching Market Watch comparison. The “Артикул в нескольких магазинах” view includes an exact SKU when at least two sellers reported a price. It keeps different quantity tiers and unconfirmed terms on separate rows; cross-store spreads appear only for offers on confirmed matching terms.

Run `python3 sync.py` in this directory for a complete sync. A repeated run matches the stable comparison key, including after an interrupted upload. A completed snapshot makes withdrawn managed rows inactive. Rows with unknown purchase conditions remain available in their own view, with empty spread and change fields when unsupported.

On the initialized Mac, run `./install-on-m1` to register the projection for updates following the native import log, with a three-hour fallback. Counts and native grid IDs are recorded in `.runtime/baserow/matrix-progress.json`. The existing price importer and the Offers table have separate ownership.

Run `python3 -m unittest -v test_sync` in this directory for offline checks.
