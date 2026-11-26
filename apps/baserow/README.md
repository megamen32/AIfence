# Baserow for Airlock

The existing personal Airlock Baserow adapter is reused on SDK v0.11.0.
Baserow's original web UI is served through the authenticated app routes,
without a nested iframe or separate visible login. The caller's platform
user ID selects its own native Baserow identity. The identity seed lives
in the application's PostgreSQL schema, not in Git. Each user's API calls
use the same native identity as the web UI; there is no shared MCP token.

The Baserow container is reachable only on the app's private runtime
network. Native UI and API routes reject users without Airlock membership
and reject requests with another origin; the upstream Baserow session
is selected server-side. Never expose the internal service publicly.
The first native initialization must follow the application's authorized
account provisioning flow. Preserve the identity seed and Baserow data
volume together; do not reset one while retaining the other.

## Deployment
Deploy with the pinned official CLI and start the app to allocate its private
runtime network. Set BASEROW_AIRLOCK_NETWORK to that network and
BASEROW_PUBLIC_URL to the deployed app's routeBaseUrl. On Apple Silicon
use linux/arm64; set BASEROW_PLATFORM=linux/amd64 on x86.

Run:
    docker --context colima-aifence compose -p aifence-baserow -f compose.yml up -d

The app's private network has the native Baserow DNS alias. No host port
is published. Only Airlock's app URL is a supported user entry point.
A local Airlock deployment must use a browser-compatible same-site domain
and the correct port in frame policies so its own native relay completes.
No weakening of session verification or global CSP is required.

Tools: list_workspaces, list_fields, list_rows, get_row, create_row.
A separate user-owned view can represent Market Watch data; observed
prices must remain a one-way projection of the source.
