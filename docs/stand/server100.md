# Server-100 personal stand

This runbook redeploys the personal stand from the Mac colima-aifence stack (Caddy port 42080) to native x86_64 server-100. The move is state-only: images are built on server-100 from pinned Git sources and small signed release overlays; the script never uses docker save, docker load, or a single image stream.

## One command

Run on the Mac from the AIfence checkout:

    ./scripts/stand/move-to-server100.sh

The script is idempotent and resumable. It records phase markers in /home/roomhacker/aifence-move/.phases/:

pin -> build -> state -> restore -> up -> reconcile -> health.

Remove a phase marker only after inspecting the corresponding logs and destination state. The destination guard /home/roomhacker/aifence-move/.move-managed prevents accidental use of an unrelated directory.

## Pinned inputs

The exact Mac capture is in server100-versions.md. The AIfence app source bundle is pinned to d47e5103b383783f9590b9fbc2887be0a87e8db8. The public Airlock source checkout is pinned to be0f9ee03be31ee15dacd5f6ebcc0f28acea6468; the private release overlay is 2026.11.27.6, core revision 0c5951ebb5d32f666d94c7ef909dd8ebb13097ad. The overlay archives are transferred with resumable rsync and then used in native Docker builds on server-100.

Individual app source_ref values are Airlock source revisions captured from the Mac database. They are preserved by the database dump and are not treated as commits in the AIfence repository.

## What is transferred

The script transfers only:

- compose files, Caddy routes, sidecar source and host-bound configuration;
- PostgreSQL custom-format dumps for Airlock, Redmine and Baserow;
- environment and secret files with mode 600;
- non-DB Docker volumes as resumable tar archives. Baserow's postgres directory is excluded because that database is restored from its dump.

Airlock, frontend, agent base/builder/executor, sidecars and committed app packages are built natively on server-100. The Airlock database is restored before the application containers start, so app generations, manifests, source refs and grants remain aligned.

## Destination layout and HTTPS

The isolated stack uses Docker Compose project names beginning with aifence-move and loopback port 44380. The existing edge receives move.aifence.bezrabotnyi.com over HTTPS and proxies to that loopback port. The script writes a receipt to /home/roomhacker/aifence-move/MOVE_RECEIPT and does not modify server-88.

## Re-running state sync

To copy newer state after a successful deployment, remove only state, restore, up, reconcile and health markers after stopping the isolated destination stack, then rerun the command. Keep pin and build when the same release is still wanted. rsync --partial resumes interrupted archives and database dumps.

Never copy or commit .env, BYOK values, OAuth tokens, private keys, .wt-diag/, media captures or generated local artifacts.