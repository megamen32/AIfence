# Server-100 personal stand copy

This track keeps an exact, reproducible copy of the Mac personal stand (\`colima-aifence\`, owner port 42080) on server-100. The copy is an operator-managed migration and is not a jury route.

- Dashboard: use the approved private hostname from protected operator runtime
- Caddy loopback: 127.0.0.1:44380
- Data root: /home/roomhacker/aifence-move

The route uses the existing server-100 Nginx TLS edge and the Airlock Caddy proxy profile behind it. The hostname and certificate lineage stay in protected operator runtime.

## One-command redeploy

Run on the Mac checkout with the \`server100\` SSH alias available:

\`\`\`sh
cd ~/AIfence
./scripts/stand/move-to-server100.sh
\`\`\`

The script is idempotent for its managed root. It refuses to adopt an unrelated directory, then refreshes versioned compose/Caddy/app sources, protected runtime files, PostgreSQL dumps, images and volumes. Secrets are copied from protected runtime paths and are never printed or committed.

The destination uses the amd64 image stream. It saves/loads the running personal images, pulls \`baserow/baserow:2.4.3\` for amd64, and builds Redmine MCP, Metrika MCP and UserIO from checked-out sources with \`--platform linux/amd64\`.

## What is moved

- Airlock state, Postgres, RustFS and Caddy data/config volumes.
- Baserow data; Redmine database/files; UserIO state.
- Per-database custom-format PostgreSQL dumps for Airlock, Redmine and Baserow under the managed destination \`dumps/\` directory.
- Airlock agent builder/base/JS executor and all images referenced by live personal agent containers.
- Compose/Caddy files, Redmine SSO plugin and secrets, Redmine MCP, Metrika gateway and UserIO/Dion source.

The live database volumes are transferred byte-for-byte to preserve the working stand. The dumps are retained for restore/audit and can be restored into a temporary database before any destructive recovery. Database names and dump paths are included in the deployment receipt without credentials.

## Checks

The script runs:

\`\`\`sh
curl -kfsS -H 'Host: $PUBLIC_HOST' https://127.0.0.1/health
curl -kfsS -H 'Host: $PUBLIC_HOST' https://127.0.0.1/
docker ps
\`\`\`

After the script completes, verify in a clean browser:

1. Airlock login at the dashboard.
2. Product Manager, Market Watch and Metrika Analyst open from the app picker.
3. Product Manager panel shows the migrated data.
4. Redmine/MCP and UserIO/Dion endpoints remain healthy.

The exact Airlock source revision, timestamp, hostname and loopback port are recorded in \`/home/roomhacker/aifence-move/MOVE_RECEIPT\`. The script does not alter the Mac stand, its containers, or the jury tunnel.
