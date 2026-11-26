# Parallel tracks

One shared Git checkout: /Users/user/AIfence, branch main.
No separate worktrees are required. Every agent may read the whole repository
and edits only its assigned directories:

| Track | Owned paths | Assignment |
|---|---|---|
| Market Watch | apps/market-watch/ | market-watch.md |
| Metrika Analyst | apps/metrika-analyst/ | metrika-analyst.md |
| Product Manager | apps/product-manager/ | product-manager.md |
| Research & Data | data-tools/, fixtures/synthetic/ | research-data.md |
| Integration & QA | tests/, docs/qa/ | integration-qa.md |

The integration owner maintains contracts/, root docs, platform/ and scripts/.
Use explicit path-limited commits as specified in AGENTS.md. Do not switch
branches, create worktrees, stage all files or modify another track's files.
Private inputs under материалы/ are shared read-only; normalized local outputs
belong in ignored data/. No private materials in Git.

The existing collector implementation belongs to apps/market-watch/collector/.
Coordinate its ownership with any already-running catalog extraction session.
