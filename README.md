# AIfence

An AI product manager for an online store: market observation + web analytics + research + decisions + feedback. Hackathon project. The contest version uses synthetic metrics and may include public SKUs and prices.

## Local start

```sh
./scripts/up
```

This starts the authorized private Airlock upstream at the exact revision in platform/airlock.lock.json, compiled locally, with isolated local state. On Mac with Colima it selects/starts profile `aifence`, not the existing default profile. The upstream builder has amd64-specific toolchain assets; builds use AMD64 emulation on Apple Silicon. On Linux use an already running Docker/Compose. Open http://localhost:42080 . First-time activation and model setup are interactive, as required by upstream. To see the local activation code, execute `./scripts/activation-code` in your own terminal; never commit the code.

After activation and normal CLI login, run `./scripts/deploy-apps --dry-run` to stage sources from the selected commit, then use `./scripts/deploy-apps` for local development. Iteration checks run on the Mac. See the submission schedule for fixed-revision checks and the jury browser check. [Stand and deployment instructions](docs/submission/deployment.md).

For another authorized machine, set AIRLOCK_UPSTREAM_URL before the first run. The checkout is fetched at the exact lock revision; local enterprise images are built from that source. The private source and all activation/model credentials remain ignored.

## Architecture

Product Manager is the only application orchestrating investigations. Market Watch and Metrika Analyst supply evidence as source modules. The collection workflow writes a versioned snapshot to the shared Airlock file catalog in the hosted user context. The pinned stand rejects the saved-file executor’s structured arguments. See the [workflow invocation and import instructions](workflows/README.md). The conversation reads that snapshot and passes its JSON to `Product Manager.import_evidence`. Product Manager can also work with authorized imported snapshots.

- `platform/airlock.lock.json`: exact upstream pin; authorized private checkout is in ignored .runtime/airlock. Do not modify platform code.
- `apps/market-watch/`: standalone market research/monitoring.
- `apps/metrika-analyst/`: standalone analytics and measurement.
- `apps/product-manager/`: hypothesis lifecycle, cross-source synthesis, prioritization, decision and feedback.
- `data-tools/`: offline import/normalization only; no service.
- `contracts/`: versioned JSON contracts; no shared runtime service.
- `fixtures/synthetic/`: public synthetic data (not supplied dataset).
- `tests/`: evidence contracts and end-to-end QA.
- `docs/tracks/`: parallel assignments and acceptance gates.

Private inputs: `материалы/`, `materials/`, `data/`, `private/`; all ignored by Git. Keep them local and read-only. Import only within authorized closed workspace and configured non-training model provider. Never send raw customer data or credentials to third parties or publish them. The repository remains private until the owner decides how to share the contest version.

Start with [architecture](docs/architecture.md), [contracts](contracts/README.md), and [track assignments](docs/tracks/README.md). The [submission pack](docs/submission/README.md) contains the jury access instructions, video script, economics, licenses and verified limits. On 27 November, KT1 finishes model-free reads by 11:00, and KT5 checks 3–5 cases on a fixed revision from 18:00 to 21:30. Record after 22:00 using the final results table. On 28 November 2026, check access in a clean browser at 09:00. Planned submission is at 10:00. All times are UTC+5. External publication and submission require separate owner approval.

### Optional authorized image cache

Set `AIFENCE_CACHE_HOST` to an SSH host that already holds the exact five images from the release lock. The local ignored `.runtime/cache-host` can remember that host. `./scripts/up` imports missing images using private resumable archives, verifies SHA-256, architecture, OCI revision and exact image ID, then starts the upstream Compose stack. On an interrupted transfer, rerun the same command. No private registry credentials or source are copied into Git. Without a cache configured, it builds from the pinned private source.
