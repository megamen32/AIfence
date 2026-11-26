# Parallel tracks

## Worktrees

| Track | Branch | Assignment |
| --- | --- | --- |
| A | `codex/kes-e2e` | [KES end-to-end acceptance](integration-qa.md) |
| B | `codex/sales-pulse` | Sales Pulse |
| C | `codex/submission-kit` | [Submission kit](../submission/README.md) |

Commit only assigned paths in your worktree and hand the SHA to the integration
owner. The integration owner applies reviewed commits one at a time to a clean,
synchronized `main` and checks each integration before pushing to private
`origin/main`.

Use the Mac for development. Run acceptance on the server stand. Version stand
configuration in `scripts/stand/` and deploy the exact verified `main` commit.
See [deployment](../submission/deployment.md).

## Repository ownership

Every agent may read the whole repository and edits only its assigned paths:

| Track | Owned paths | Assignment |
|---|---|---|
| Market Watch | apps/market-watch/ | market-watch.md |
| Metrika Analyst | apps/metrika-analyst/ | metrika-analyst.md |
| Product Manager | apps/product-manager/ | product-manager.md |
| Research & Data | data-tools/, fixtures/synthetic/ | research-data.md |
| Integration & QA | tests/, docs/qa/ | integration-qa.md |

The integration owner coordinates changes to `contracts/`, root docs,
`platform/` and `scripts/`. See [AGENTS.md](../../AGENTS.md) for
path-scoped commits.
Private inputs under материалы/ are shared read-only; normalized local outputs
belong in ignored data/. No private materials in Git.

The existing collector implementation belongs to apps/market-watch/collector/.
Coordinate its ownership with any already-running catalog extraction session.
