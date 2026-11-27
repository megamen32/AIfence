# Durable price collection and repair

The existing LaunchAgents still own one crawler per store. No additional
crawler or request identity is introduced. Softline's family and standalone
phases and the other two stores' family/variant phases share the local
`crawl.sqlite` (WAL) and content-addressed `raw-html/<prefix>/<sha256>.html.gz`
in the catalog directory. The original JSON/JSONL checkpoints and exports
remain compatible. Each successfully read HTTP body, including redirects and
non-200 responses, has a separate URL, response URL, status, capture time,
byte count and digest receipt. A storage error stops collection instead of
publishing an observation with missing evidence.

Each host starts with 8 request slots and can increase to 16 after successful
responses. Fetch workers and 4 parse workers are separate. The existing
one-second minimum start spacing and longer robots Crawl-delay still apply.
5xx/timeouts halve concurrency and increase spacing; a healthy run recovers
gradually. 403, 429, 503 and 5xx with a future Retry-After pause admission;
the existing persistent source pause is retained. The archive is local and
private; the worker stops at a 2 GiB disk reserve rather than deleting raw
evidence silently.

`crawl_tasks` is the persistent work ledger. `failed_tasks` records source,
URL, phase, generation, error type, attempts, next attempt, evidence receipt
and state. Backoff is 1, 2, 4 and 8 minutes after the first four failures;
the fifth is exhausted. A parse retry reads the archived response with its
original timestamp, not the network. Legacy unresolved checkpoints without
a body become retry tasks; a legacy checkpoint is not raw evidence.
Known unavailable, no-public-price and hardware observations are not parse
failures. Demo-family URL hints run first, then the comparison-first Softline
queue. Hints never identify or price a SKU.

The separate `marketrepair` maintenance process does not crawl sites. It
selects 10–20 exhausted parse failures grouped by store and error type.
Its model request uses bounded excerpts of the archived HTML plus existing
rules. Full responses stay on disk. The existing bound Market Watch
application's admin-only `propose_parser_repair` calls its configured
Airlock text model. It returns a declarative CSS selector proposal or
no_change, never executable code. A candidate requires exact canonical
product identity, confirmed software scope where relevant, explicit SKU,
currency and two independent agreeing price fields. It is replayed against
the archived corpus; activation requires improvement on a page held out
from the model, an increased number of priced observations and zero
regressions. Activation and requeue of proven improvements are atomic.
Retries then use the stored HTML with the original capture time.

The repair process requires a reachable, authenticated native Airlock
application with the proposal tool installed. A model outage is recorded
as waiting_model; the crawler does not stop and no untested rule is enabled.
Raw HTML is untrusted input, never executable instructions. Only actual
public price evidence is admitted, not inferred prices or invented terms.

Local checks:
```sh
go test ./collector/...
go test -race ./collector ./collector/cmd/softlinefull ./collector/cmd/fullcatalog
```
The SDK application build/deploy is separate from the local Go collectors.

## Independent scheduling (second revision)

The journal is the scheduler. Both family and standalone URL inventories are
restored before publishing a new snapshot. Ready work is selected from SQLite
in bounded mixed windows: family/variant, new/retry and archived-body lanes
each have a share, with the demo/competition preference inside each lane.
The existing per-store writer locks remain the ownership boundary. An
interrupted running task can be resumed after the owner releases its lock.

Network failures retain their original attempts and exponentially delayed
next attempt. A failure to parse a retained successful response moves directly
to `repair`; the same body/rule pair is not retried. Old unresolved rows with
no raw body remain network work. Repair batches are selected by source,
error type and cached structural HTML fingerprint, with 10–20 examples and
held-out validation. Only an accepted rule can reopen those archived tasks.
A rejected batch leaves the missing prices explicitly unresolved.

SoftMagazin category discovery proceeds in slices of at most eight network
requests between price windows. It can still report incomplete scope or
category count discrepancies; those are not permission to guess software
classification. No listing audit blocks the existing exact URL inventory.

The host start interval and robots/Retry-After restrictions are unchanged.
An isolated transport timeout is local to the task; repeated transport
failures reduce concurrency. Healthy responses, including 404/410, allow
the host limiter to recover gradually. A source pause still stops new HTTP.

This revision is not a publisher change-feed implementation: independent
refresh of already verified prices remains a separate follow-up. The existing
featured-offer refresh, export and import services are not replaced.
