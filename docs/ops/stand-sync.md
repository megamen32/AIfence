# Stand sync operations

\`scripts/stand-sync\` is the one-command, resumable workflow for rebuilding the server-100 stand. Docker images are built natively on server-100; state is transferred only in explicit \`--state\` mode.

## Usage

\`\`\`sh
scripts/stand-sync --to server-100
scripts/stand-sync --to server-100 --apps product-manager --no-state
scripts/stand-sync --to server-100 --apps product-manager,market-watch --no-state
scripts/stand-sync --to server-100 --apps product-manager --state
\`\`\`

No \`--apps\` means a full stateful migration. Supplying \`--apps\` defaults to code-only, so daytime fixes take the build/reconcile/health/smoke path without dumping databases. \`--state\` and \`--no-state\` are mutually exclusive.

## Resumption and phases

The target root is \`/home/roomhacker/aifence-move\`. Successful phases leave \`.stand-sync/<source-app-mode-key>-<phase>.done\` markers there. Local receipts are under \`.runtime/stand-sync/<UTC timestamp>/\`; they contain \`pin.env\`, a secret-free app/SHA manifest, timings, health output, smoke status and optional screenshots. A failure prints the phase name and the exact retry command.

| Phase | Behavior |
| --- | --- |
| pin | Reads the source Git SHA and contract fingerprint, plus non-secret image IDs from the Mac Docker context when available. |
| build | Full mode delegates the native, resumable \`scripts/stand/move-to-server100.sh\`; code-only fetches that SHA into a target worktree and runs the target compose build. |
| state | Full mode uses the existing \`pg_dump -Fc\` and resumable rsync transfer; code-only records a skip. |
| restore | Restores dumps before app startup in full mode; code-only records a skip. |
| reconcile | Runs \`STAND_SYNC_RECONCILE_CMD\` on the target to re-register apps, re-apply grants bound to app version and restart source watches. |
| health | Checks the HTTPS health endpoint. |
| smoke | Checks comma-separated URLs and optionally runs \`STAND_SYNC_SCREENSHOT_CMD\` in the receipt directory. |

Code-only pinning uses \`refs/heads/main\` by default; set \`STAND_SYNC_SOURCE_REF\` for a controlled ref. Full mode pins HEAD/running source. Useful target overrides are \`STAND_SYNC_TARGET_HOST\`, \`STAND_SYNC_REMOTE_ROOT\`, \`STAND_SYNC_GIT_URL\`, \`STAND_SYNC_BUILD_CMD\`, \`STAND_SYNC_RECONCILE_CMD\`, \`STAND_SYNC_PUBLIC_HOST\`, \`STAND_SYNC_HEALTH_URL\`, \`STAND_SYNC_SMOKE_URLS\`, and \`STAND_SYNC_SCREENSHOT_CMD\`. Hooks read existing env files and never accept credentials as CLI arguments.

## Traps encoded

* Personal image tags identify deployed source; \`main\` may contain different code. Keep the pinned SHA and contract fingerprint in the receipt.
* \`check_source_watch\` v1 and v2 are different contracts.
* Airlock grants are version-bound; re-registering without grants causes 403 reads.
* Restore custom-format dumps with the PostgreSQL major that created them, then run only migrations from the pinned commit.
* Keep retry loops in one remote shell so variables survive.
* Use HTTPS Caddy and its loopback health URL; do not send proxy_protocol bytes to the TLS listener on 8444.

Never commit \`.runtime/stand-sync\`, screenshots, secrets or env files.
