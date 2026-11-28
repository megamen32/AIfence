# Owner UI grants on 42080

Date: 2026-11-28 (Asia/Yekaterinburg)

All six source apps plus Product Manager were checked through the authenticated owner session. Every app already has a direct `megamen932@gmail.com` member grant with role `admin`, so no delete/re-add mutation was necessary:

- competitor-research `03dc1bb2-869e-4783-897f-7c7fbc4b395d`
- web-scraper `cdd2224a-e2e8-4be8-a325-49fe7fa37be7`
- google-trends `865ff815-30fb-4a03-983e-300501230909`
- wordstat `31d1d3e9-2488-47ca-ab01-ddd50afe8882`
- metrika-analyst `6e985f49-ae84-4f6d-b345-edb4ea0d95cf`
- market-watch `8702e3ad-c528-45af-8172-00e3be59ae7f`
- product-manager `01a5630d-261e-4c41-88a7-15fe5bd4fa2b`

Competitor Research now renders for the owner at the requested workspace URL. A fresh bezbrowser receipt contains full-page PNG captures for all seven workspace URLs and zero occurrences of `нет доступа к веб-интерфейсу`: `oc1.NzY0MjMxYjJlYTcwNWZkMDU0NGIwNzEzYWE2ZDhiZDE.02bd2a36d7c1bc8a088412f2049b98e9`.

No app was redeployed.

Added executable `scripts/regrant-owner-ui.py`. It reads the Airlock CLI credential store without printing tokens, confirms the owner identity, checks all seven app member lists, and POSTs an `admin` member only when absent. `scripts/deploy-apps` runs this idempotent check after a personal 42080 deployment. Validation and live run: `python3 -m py_compile scripts/regrant-owner-ui.py`, `bash -n scripts/deploy-apps`, `owner UI grants verified=7 restored=0`.

Individual PNG files were not written to the repository because bezbrowser stores them in its receipt. Redmine/Dion were not touched.
