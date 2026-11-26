# Baserow for Airlock

Native Baserow 2.4.3 (ARM64 on Mac M1) runs in its own persisted Docker
container. Airlock remains unchanged. The Go SDK app registers Baserow's
existing MCP endpoint and opens a configured native Baserow page. No
replacement database UI, no custom MCP implementation.

## Local run

```sh
docker --context colima-aifence compose -p aifence-baserow -f compose.yml up -d
```

Only loopback port 42081 is published. The internal service is attached to
the existing private Airlock backend network. Use the instance's normal
authentication. Configure a workspace-scoped Baserow MCP endpoint key through
the Airlock credentials UI (never place keys in source or URL). Review MCP
permissions; keep public-price observations read-only where supported.

For data, use the current Market Watch CSV as a one-way projection, not a second
authoritative price source. Preserve exact SKU, URL, captured time and quantity
basis. Baserow UI edits may not alter the source's prices. Use user-created views
and notes for review. Native embedding depends on Baserow's frame policy;
the open-in-Baserow link remains available.
