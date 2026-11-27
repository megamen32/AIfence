# Adaptive store profiles
One explicit new public HTTPS store URL starts a bounded study in the existing
Market Watch app. Existing built-in stores and local bulk workers are not
replaced or duplicated. Discovery samples published same-host links and
robots-announced sitemaps. The native app retains the original successful
responses as gzip objects with SHA-256 and capture timestamps, plus durable
URL tasks and model candidates. The model sees training excerpts only.

The declarative AdapterSpec supports multiple matching templates, structured
Product/Offer data, exact standalone cards, repeated family rows, explicit
status markers, currency and quantity terms, and same-host link discovery
with path filters. It cannot execute generated code or access other hosts.
Actual DNS addresses are checked on each new connection; private/loopback/
special addresses, credentials, non-HTTPS, cross-host redirects and proxies
are excluded. The normal robots policy, pacing and control-response pauses
are retained.

The Go validator checks selectors, exact product/link identity, price and
currency evidence, independent confirmations, old offers/statuses and
URL-disjoint holdouts. Each candidate is replayed against all distinct
archived HTML receipts. Up to three proposal rounds use deterministic
feedback; holdout content is never sent to the model. An improvement must
also be demonstrated on holdout pages. A passing immutable version is stored
before changing the active pointer. Repeated template failures trigger
another study from archived evidence; accepted versions replay old pages
locally. Rollback selects an existing validated version without rewriting
historical price observations.

Native tools: add_store, list_adaptive_stores, repair_store_adapter,
rollback_store_adapter, propose_store_adapter (admin diagnostic).
Adaptive profiles participate in the existing refresh_catalog job and
catalog_status/list_offers. New-source onboarding and refresh are bounded;
this version does not claim complete sitemap coverage or migrate the three
existing specialized bulk crawlers to the DSL. Native archive validation is
streaming but subject to the SDK execution deadline. A failed study preserves
the prior profile. No new app or public route is needed.

Tests: go test ./collector/... ./market; go test -race ./collector ./market.
