# AIfence — shared evidence workflow

One native Airlock saved async JavaScript body, not a new app or service. For a hosted user conversation: it collects read-only, normalized evidence from Market Watch and Metrika Analyst, adds selected authorized evidence documents, links the previous snapshot, and creates a versioned shared file. Product Manager reads the file ID/contentId and applies its existing hypothesis and task lifecycle. Personal or client data and credentials are not included here.

## Install

1. In the target Airlock, use the normal Files interface to create/upload `/home/Documents/AIfence-collect-evidence.js` from `collect-evidence.js`. The directory must exist and the current user must have write permission. If updating an existing file, use its current contentId; never silently replace another script.
2. In a hosted Product Manager conversation, inspect the actual app tool schemas. Invoke top-level `run_js_file` with the following path, description and args. The query objects are passed directly to the native tool calls; the example illustrates the intended AIfence contract, not the separate personal Metrika Analyst API. Adjust them to the inspected schemas of the deployed apps.

```json
{
  "path": "/home/Documents/AIfence-collect-evidence.js",
  "description": "Collect and save product investigation evidence",
  "args": {
    "run_id": "synthetic-a-20261126-1",
    "product_key": "synthetic:keyboard-a",
    "market_query": {"product_key": "synthetic:keyboard-a", "limit": 8},
    "metrics_query": {"product_key":"synthetic:keyboard-a","metric_keys":["checkout_rate"],"baseline_from":"2026-10-01","baseline_to":"2026-10-07","current_from":"2026-10-08","current_to":"2026-10-14","segment":"all"},
    "evidence_file_ids": [],
    "document_file_ids": []
  }
}
```

The normalized input files must use the repository's `aifence.v1` evidence contract (including research, sales and unit economics when available). Select only authorized sources. Ordinary document IDs are references, not automatically analyzed facts. An optional `previous_snapshot_id` links the preceding investigation for the same product. The script saves `/home/Documents/AIfence-evidence-<run_id>.json`, reads it back and returns its stable file ID/contentId. Repeating the identical run returns the same file without fetching again; intentional new observations require a new run ID. Missing evidence stays unavailable, not zero. The script does not modify prices, send tasks or invent profit estimates.

## Product Manager hand-off

Read the returned snapshot by ID. Use source references and observed/missing/incomparable states. In the app's existing hypothesis record store the linked evidence snapshot ID/contentId, proposed action, criterion, and eventual release. Only send an approved task to the configured backlog, using the native idempotency contract and the required tag. Once the approved measurement period is complete, collect a fresh snapshot with the same product and previous_snapshot_id, then evaluate the hypothesis using actual comparable observations. Without actual post-release data the hypothesis remains awaiting measurement.

## Scheduling

A hosted `run_js_file` runs under the current user's file and app grants. An SDK app job runs under its own scoped app context and cannot assume the human's home identity. Reuse native Product Manager job/schedule for post-release follow-up, with app-local state, approved source references and authorized connections. Do not build a second orchestration loop or copy personal credentials into a job. Scheduled cross-app execution is a separate live integration check; this script does not claim to implement it.

## Test

`node --test workflows/collect-evidence.test.mjs` from the repository root. Tests use mocked native capability contracts and synthetic inputs; they are not a live Airlock integration test. For live acceptance verify actual deployed tool schemas and one hosted `run_js_file` invocation, read the returned file via the user's catalog, and repeat the identical request to confirm idempotency.
