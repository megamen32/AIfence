# Source watch on the stand

For project revision `b09ef84`, the read-only stand receipt at `/Users/user/AIfence/.runtime/kes-handoff/sourcewatch-acceptance-20261127/prior-deployment.json` records `/job-handlers` HTTP 200 with no `check_source_watch` handler. The jobs read returned HTTP 200 with no matching runs, and the source-watch analysis route was not declared. There is no job/run ID to report for an absent invocation.

This is a point-in-time observation, not proof about a later deployment. A final released version needs its own deployment identity, registered handler and route, and an actual scheduled job receipt before stand acceptance can be claimed. The offline replay and hosted chat observations are documented separately.

## 27 November 2026 · contest deployment

The contest stand runs Airlock `be0f9ee` and public Agents SDK `v0.11.0` (`2367c082`). Product Manager was deployed from AIfence `95ab127` using the stand compatibility profile. The installed Product Manager source is `14ac246a0cdff23b70d231d0437f1f1c540320a2`, revision `2220f416-1be0-423a-89dc-15c595857c51`.

Authenticated `/job-handlers` returned HTTP 200 and listed `check_source_watch` version 1 as active. `/job-app-tool-bindings` returned HTTP 404. `/jobs` returned HTTP 200 with zero `check_source_watch` runs. The pinned host lacks owner-bound app-tool reads for background jobs. The compatibility profile records `unsupported` if asked to read those bindings. This check produced no job run ID and did not invoke `review_source_signal`.
