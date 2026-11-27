# Google Trends for AIfence

Standalone source application in apps/google-trends. It does not change AIfence core, register global MCP servers, or run a separate orchestration service.

Two entry points share the same provider:

- **Mac raw MCP:** a stdio launcher for the ready HasData Google Trends MCP. It preserves the provider tool and protocol.
- **Airlock app:** an SDK v0.11.0 UI and three typed tools with validation, explicit source states, and a consistent comparison scale. It uses a native resource binding and the SDK MCP proxy.

## What a product director can investigate

1. Compare a product phrase and a market phrase in one request to form a hypothesis about external demand. Internal traffic and conversion data are needed to investigate the funnel separately.
2. Compare 2–5 product or competitor phrases with identical dates, geography, category, and search property.
3. Examine regional interest to choose where to investigate demand next.
4. For a single phrase, inspect popular and rising related queries.

Only public search terms should be sent to the provider. This application contains no private competition materials, interviews, customer records, or financial data.

## Mac raw MCP

Requirements: Node 22 or later; the existing Mac installation is /opt/homebrew/bin/node.

Install the exact lock without lifecycle scripts:

```sh
cd /Users/user/AIfence-worktrees/external-demand/apps/google-trends
npm ci --ignore-scripts --no-audit --no-fund --cache .runtime/npm-cache
```

The owner-managed credential file is .runtime/hasdata.env, mode 0600, inside an owner-only .runtime directory. It contains exactly one HASDATA_API_KEY assignment. Do not place the key in MCP configuration, command arguments, source control, or command history. The file is already provisioned on the current Mac; setup does not overwrite it.

Preflight, without a provider call:

```sh
/opt/homebrew/bin/node scripts/raw-mcp.mjs --check-config
```

Register only the command and absolute launcher path:

```json
{
  "command": "/opt/homebrew/bin/node",
  "args": [
    "/Users/user/AIfence-worktrees/external-demand/apps/google-trends/scripts/raw-mcp.mjs"
  ]
}
```

The launcher is independent of the caller's working directory. Its default private file is resolved relative to the app. An explicit alternative is supported with --credential-file /absolute/path. No key is accepted on argv.

The launcher runs locked mcp-remote 0.1.43 against https://mcp.hasdata.com/mcp?apis=google_trends using Streamable HTTP and x-api-key header injection. The key is passed only through the child environment. The argv header uses a literal environment placeholder. Missing, empty, quoted-whitespace, or unsafe credential files fail before spawning the bridge. Debug environment variables are not inherited.

Exactly one provider tool is exposed:

```text
hasdata_google_trends_search_getTrendsData
```

Parameters are q, geo, region, dataType, tz, cat, gprop, date. q is a comma-separated list for comparison. dataType accepts timeseries, geoMap, relatedTopics, relatedQueries. gprop accepts youtube, news, images, froogle; omit it for web search. Full observed schema is recorded in the private runtime validation receipt.

Mac registration and the one real data canary are summarized in validation/mac-registration.json and validation/canary.json. The full provider response is private at .runtime/validation/canary-payload.json. No repeated canary or retry is part of startup.

## Normalized app contract

| Tool | Terms | Purpose |
| --- | --- | --- |
| compare_interest | 2–5 | One timeseries request and one common scale |
| regional_interest | 1–5 | One geoMap request |
| related_queries | 1 | Popular and rising related searches |

All tools accept terms, geo, from, to, property, category. regional_interest also accepts region (country, region, dma, city). Dates use YYYY-MM-DD, timezone is UTC. Omitting both dates selects the last 12 complete calendar months; omitting only one is invalid. A future date returns unavailable_period and preserves the requested period.

Each result includes request, source, status and message. source contains the exact request fingerprint, response-specific comparison_id, fetch timestamps, provider request ID if available, and an Explore link. It does not publish provider artifact URLs or authentication details.

Data rules:

- Indices 0–100 are relative within this response, not query volumes, customers, revenue, market share, or additive totals.
- All phrases for a comparison are sent together. Results from separate calls must not be stitched into a common scale.
- hasData=false is missing data, including when the provider's numeric field is zero.
- hasData=true with value 0 remains a numeric zero.
- Missing hasData/isPartial flags stay unknown. Numeric values with unknown flags are preserved but excluded from comparative means.
- <1 stays censored and is not converted to zero.
- Means use the same set of explicitly complete intervals with confirmed, uncensored values for every term. The included and excluded counts are returned.
- Weekly source buckets may cross the requested date boundaries. Both requested dates and returned labels are retained.
- Top related queries use an index. Rising uses growth percent or Breakout; Breakout is not coerced into the provider's numerical extractedValue.
- No automatic retries, CAPTCHA workarounds, alternate IPs, or cookie harvesting are implemented.

Statuses: ok, credentials_required, rate_limited, quota_exhausted, access_denied, invalid_input, unavailable_period, insufficient_data, upstream_error, invalid_provider_response. Errors have fixed public messages and do not echo upstream payloads or secrets.

The UI and typed tools call the same trends.Service. The UI uses SDK-bundled htmx, templ, DaisyUI, and Lucide. It makes no provider requests on GET or automatic refresh.

## SDK build and tests

Go 1.26.11 is selected by go.mod. On the current Mac it is already cached at /Users/user/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.11.darwin-arm64/bin/go.

```sh
go tool air toolchain install
go tool air build
node --test mcp/runtime-config.test.mjs
```

Build runs the matching generator, CSS build, tests, compilation, and offline manifest validation. Generated files, binaries, dependencies, credentials, and runtime receipts are ignored. The tests use offline fixtures and never consume provider credits.

Do not use air run --local. Runtime UI validation belongs in an authorized Airlock app or preview.

## Native Airlock resource

The declaration is fixed:

```text
slug: hasdata_google_trends
URL: https://mcp.hasdata.com/mcp?apis=google_trends
auth: MCPAuthToken
binding: BindingShared
injection: api_key_header, name x-api-key
```

A native resource must be created and bound through the authorized platform UI/API/CLI. The Mac private file is not a native app environment variable. The standard platform proxy injects the credential at runtime. No All-Users or public grants are requested; routes and typed tools require AccessUser. App definition and offline manifest generation do not read credentials or call the network.

## Provider account and quotas

At onboarding on 2026-11-27, the actual portal displayed Trial, 1,000 credits, and concurrency 5. Google sign-in completed without phone or payment-card entry. This is an observation of the current account, not a promise of its renewal terms.

Current official documentation lists 10 credits per successful Google Trends request. Do not infer remaining credits from this app unless the provider returns them. Check the provider dashboard for the current balance.

Sources:

- https://hasdata.com/mcp/google-trends
- https://docs.hasdata.com/mcp-server
- https://docs.hasdata.com/apis/google-trends/search
- https://hasdata.com/prices
- https://support.google.com/trends/answer/4365533
- https://developers.google.com/search/apis/trends
