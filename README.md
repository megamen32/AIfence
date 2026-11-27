# AIfence

AIfence is a product manager for an online store. A director can ask a vague
question such as «по Касперскому продажи просели?», and the system collects
available market and analytics evidence, compares a product with an appropriate
reference, separates a signal from a proven cause, and proposes the next
check. The contest build uses synthetic metrics and public catalog products;
private customer files stay outside the repository.

## Jury links and delivery status

- **Stand:** [aifence.bezrabotnyi.com](https://aifence.bezrabotnyi.com/) · [open
  Airlock chat](https://aifence.bezrabotnyi.com/chat). Interactive access
  requires an invitation to the demonstration workspace; the exact tunnel
  URL from the final `sol-stand` report must be checked before sending it to a
  jury member.
- **Health check:** [stand and deployment notes](docs/submission/deployment.md).
  An anonymous HTTP check is only a service check; it does not grant access to
  the applications.
- **Source repository:** [github.com/megamen32/AIfence](https://github.com/megamen32/AIfence).
  On 27 November 2026 an unauthenticated web and API check both returned
  HTTP 404, so the repository was not public at that point. The owner decides
  whether and how to publish a contest copy.
- **Confluence:** a possible post-submission handoff only; no Confluence URL
  is part of the current jury package.

The submission kit is in [docs/submission/README.md](docs/submission/README.md):
economics, model and license declarations, jury access, and the video script.

## Architecture in ten lines

1. Airlock supplies authentication, the assistant loop, storage, scheduling and file access.
2. Market Watch is an independent source for dated, comparable public offers and price changes.
3. Metrika Analyst is an independent source for authorized reports and imported historical aggregates.
4. Product Manager is the only application that orchestrates an investigation.
5. `workflows/collect-evidence.js` calls source tools under the current user's grants.
6. The workflow saves a versioned evidence snapshot in the shared Airlock file catalog.
7. Product Manager imports that snapshot or an explicitly authorized historical snapshot.
8. Code calculates windows, changes, sample sizes and missing data; the model explains bounded evidence.
9. A hypothesis records evidence for, evidence against, unknowns, a cheap distinguishing check and a success rule.
10. Background delivery can create an idempotent Redmine task and an owner notification through Dion.

## Native M1 build and deployment

The installed M1 uses the approved native profile pinned in `platform/personal-release.lock.json`. Its application source is canonical `main`. Build a committed integration candidate with `/opt/homebrew/bin/python3 scripts/personal-build-apps.py --commit <sha> --output <private-output-directory>` and Go 1.26.11 first in PATH. The script validates the exact source bundle with the matching pinned SDK and Sol. See [native builds and releases](docs/personal-releases.md) for cache configuration and prerequisites.

After integration, set `AIFENCE_DEPLOY_COMMIT` to the verified `main` SHA and run `./scripts/deploy-apps`. It uses the same native builder, checks `main` again before upload, and preserves SDK source CAS. Deployment locks and bindings are shared across worktrees in the Git common directory. `./scripts/deploy-apps --dry-run` only stages committed sources. Record the source SHA and target runtime for acceptance. [Stand and deployment instructions](docs/submission/deployment.md).

### Original platform bootstrap for a separate installation

The following bootstrap uses the original v0.8.15 platform pin for a separate installation. The existing M1 native runtime and its app releases use the pinned native profile above.

```sh
./scripts/up
```

This starts the authorized private Airlock upstream at the exact revision in platform/airlock.lock.json, compiled locally, with isolated local state. On Mac with Colima it selects/starts profile `aifence`, not the existing default profile. The upstream builder has amd64-specific toolchain assets; builds use AMD64 emulation on Apple Silicon. On Linux use an already running Docker/Compose. Open http://localhost:42080 . First-time activation and model setup are interactive, as required by upstream. To see the local activation code, execute `./scripts/activation-code` in your own terminal; never commit the code.

For another authorized machine, set AIRLOCK_UPSTREAM_URL before the first run. The checkout is fetched at the exact lock revision; local enterprise images are built from that source. The private source and all activation/model credentials remain ignored.

## Architecture

Product Manager is the only application orchestrating investigations. On native M1, its scheduled jobs call authorized read-only tools in Market Watch and Metrika Analyst through native app bindings. Market Watch and Metrika Analyst supply evidence as source modules. The collection workflow writes a versioned snapshot to the shared Airlock file catalog in the hosted user context. The pinned stand rejects the saved-file executor’s structured arguments. See the [workflow invocation and import instructions](workflows/README.md). The conversation reads that snapshot and passes its JSON to `Product Manager.import_evidence`. Product Manager can also work with authorized imported snapshots.

- `platform/personal-release.lock.json`: exact installed native M1 dependencies; app source and release recipes live in `main`.
- `platform/airlock.lock.json`: original bootstrap pin; authorized private checkout is in ignored .runtime/airlock. Do not modify platform code.
- `apps/market-watch/`: standalone market research/monitoring.
- `apps/metrika-analyst/`: standalone analytics and measurement.
- `apps/product-manager/`: hypothesis lifecycle, cross-source synthesis, prioritization, decision and feedback.
- `data-tools/`: offline import/normalization only; no service.
- `contracts/`: versioned JSON contracts; no shared runtime service.
- `fixtures/synthetic/`: public synthetic data (not supplied dataset).
- `tests/`: evidence contracts and end-to-end QA.
- `docs/tracks/`: parallel assignments and acceptance gates.

Private inputs: `материалы/`, `materials/`, `data/`, `private/`; all ignored by Git. Keep them local and read-only. Import only within authorized closed workspace and configured non-training model provider. Never send raw customer data or credentials to third parties or publish them. The repository remains private until the owner decides how to share the contest version.

Start with [architecture](docs/architecture.md), [contracts](contracts/README.md), and [track assignments](docs/tracks/README.md). The [submission pack](docs/submission/README.md) contains the jury access instructions, video script, economics, licenses and verified limits. The code freeze is **28 November 2026, 00:00 UTC+5** and the submission deadline is **28 November 2026, 12:00 UTC+5**. The internal plan is to check access in a clean browser at 09:00 and have the form ready by 10:00. External publication and submission require separate owner approval.

## Run locally

The original Airlock bootstrap uses an isolated Colima profile and the pinned
private platform source:

```sh
./scripts/up
# open http://localhost:42080 and complete the first-time activation/model setup
```

For the installed native M1 profile, build a committed candidate with the
pinned SDK/Sol cache and then deploy the exact verified commit:

```sh
export AIFENCE_PERSONAL_CACHE="$HOME/AIfence-worktrees/personal-integration/.runtime/personal-release"
export GOPROXY=off
/opt/homebrew/bin/python3 scripts/personal-build-apps.py --commit <sha> --output <private-output-directory>
export AIRLOCK_URL=https://aifence.bezrabotnyi.com
export AIFENCE_DEPLOY_COMMIT=<verified-main-sha>
./scripts/deploy-apps --dry-run
```

The full release and stand procedure is [documented here](docs/submission/deployment.md).

### Optional authorized image cache

Set `AIFENCE_CACHE_HOST` to an SSH host that already holds the exact five images from the release lock. The local ignored `.runtime/cache-host` can remember that host. `./scripts/up` imports missing images using private resumable archives, verifies SHA-256, architecture, OCI revision and exact image ID, then starts the upstream Compose stack. On an interrupted transfer, rerun the same command. No private registry credentials or source are copied into Git. Without a cache configured, it builds from the pinned private source.
