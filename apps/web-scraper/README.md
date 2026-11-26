# Web Scraper
Independent native Airlock SDK v0.11.0 app for monitoring the collection
process, not market analytics. Read-only import of the existing AIfence
collector's real reports. No changes to platform, scraper worker code, or
Baserow. It shows per-source coverage, rules, page classifications, capture
times and CSV/Baserow export receipts; no fabricated historical trends.
A new-source form saves an administrative draft only. Actual activation,
parser generation/test, start/pause and retries require an agreed worker
control API; do not show those as working until connected.

## Local Mac M1
1. Deploy this directory with the pinned official Airlock CLI:
   `../../.runtime/bin/market-air deploy --url http://localhost:42080 --create --name 'Web Scraper' --slug web-scraper -m 'Install scraper panel' --wait`.
2. Run `python3 publish.py --dry-run` to inspect real sanitized reports.
3. Run `python3 publish.py` to send an authenticated admin snapshot to
   the app. The host-side publisher reads the existing reports and does
   not start, stop or modify any collector. The local installation uses launchd label
   local.aifence.web-scraper-report at 60-second intervals; the UI refresh
   button reloads the latest stored snapshot.
4. Use `get_scraper_status` in the app or open the Sources view.
   The data layer is not a second crawler; per-license coverage remains
   distinct from family-page coverage. A missing denominator is unknown.

The Mac-specific links are to the already installed Market Watch and
Baserow apps. Baserow import status is a receipt from the last import,
not a live count. Local Airlock browser relay must work before claiming
embedded UI acceptance.

## Acceptance
The app is deployed to http://web-scraper.localhost:42080/; native status tool,
authenticated snapshot upload, and overview/detail routes returned success.
Direct browser navigation shows real data. Embedded dashboard navigation
still needs the existing local gateway to include web-scraper.localhost;
no platform files were changed. Existing workers remain independently managed.
