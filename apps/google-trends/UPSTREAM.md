# Pinned upstream dependencies

## HasData hosted MCP

- Provider: HasData.
- URL: https://mcp.hasdata.com/mcp?apis=google_trends
- Actual initialize server: hasdata-mcp-api-server, version 1.0.0.
- Actual catalog: hasdata_google_trends_search_getTrendsData.
- Authentication: x-api-key.
- Protocol: Streamable HTTP.
- Hosted service version is provider-controlled. The local bridge and dependencies are pinned; no claim of a fixed remote implementation is made.

Primary documents:

- https://hasdata.com/mcp/google-trends
- https://docs.hasdata.com/mcp-server
- https://github.com/HasData/google-trends-mcp
- https://docs.hasdata.com/apis/google-trends/search

## mcp-remote 0.1.43

- License: MIT.
- npm SHA-1: 577562c2e0eb121cb7911a62205c088faa53a23a
- npm integrity: sha512-N2pGvTAPlHSH64iftgaVsR9J2+QUgCkTbaFb8XMxRbOFvJTCFONJOUNOxfnxPc0FUKyWUE/jpdhabfBI3g/gPw==
- The downloaded tarball matched both registry digests.
- package-lock.json pins the transitive dependency graph.
- Installation uses --ignore-scripts.
- API key interpolation happens in the bridge environment, not command arguments. Debug flags are not enabled.

The inspected official @hasdata/google-trends-mcp 1.0.1 wrapper is MIT, but is not installed as a dependency. Its pin was independently verified:

- npm SHA-1: 6b4c06e1b0cb02aa80e039ef7c208662e2f65353
- npm integrity: sha512-719cwYC8sB03pt2RRMII2vdWf4MDoUOgK/8ap4dmJ0LAf4gNLTyduXThYSxxwIVgT/gBgVEW4C0FB7wRMbO26g==

## Airlock SDK

- github.com/airlockrun/agentsdk v0.11.0.
- UI generation and third-party notices are managed by the matching SDK toolchain.
- Go module checksums are recorded in go.sum.
