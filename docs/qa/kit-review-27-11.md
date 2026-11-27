# Submission kit audit · 27.11.2026

Read-only audit of `README.md`, `docs/submission/*`, the linked QA records,
the current repository, the local KES workbook and package manifests. The only
content change made during this audit is the grammatical typo fixed in
`docs/submission/README.md:10`.

## Checklist

- [x] Placeholder-name lint passes. Command `python3 scripts/lint-placeholder-names.py` returned `No placeholder product names found.` The same result holds for the submission directory.
- [x] Local Markdown links in `README.md` and `docs/submission/*.md` resolve to existing files. The scene image paths named by `docs/submission/demo.md:19-22` also exist.
- [x] Economics values for Kaspersky Endpoint Security match `/Users/user/Downloads/Датасет_ИИ-помощник_PM_v17.xlsx`, sheet `Продажи`, columns `week_start`, `product_family`, `orders_index`, and channel `Самостоятельные заказы клиентов`. The calculated means are 165.846 for the 13-week baseline, 113.000 for the first four post-release weeks, and 111.571 for the seven-week post-release window. The percentages and index deficits in `docs/submission/economics.md:67-82` recalculate correctly.
- [x] The Astra Linux control value in `docs/submission/economics.md:84-86` also matches the workbook when the same 13-week baseline and seven-week post-release window are used. The means are 98.538 and 96.000, with a −2.576% change.
- [x] The example arithmetic in `docs/submission/economics.md:134-145` is correct for its stated assumptions. It is visibly labelled as an example, so it is not presented as measured customer economics.
- [x] Listed Go and Node versions match the manifests checked. `agentsdk` is v0.11.0, `goai` is v0.3.4, `templ` is v0.3.1020, `mcp-remote` is 0.1.43, `yandex-metrica-mcp` is 1.0.1, `mcp-yandex-wordstat` is 2.5.0, and `supergateway` is 4.3.0.

## Findings

- [ ] **P0 · open-weights compliance is unverified.** `docs/submission/form.md:13` and `docs/submission/models-and-prior-work.md:27-37` identify the effective route as `combo/airlock-personal-exec-terra61-safe` with a MiniMax failover, while `models-and-prior-work.md:12` says the Terra checkpoint and license still require confirmation. The kit cannot establish that every model used by the route is open weights or that the exact checkpoint permits contest use. The latest live QA also records Gemma 4 64K rather than Terra fast in `docs/qa/kes-contest-2026-11-27.md:13-18`.
- [ ] **P1 · excluded public stand is reachable from the repository.** `docs/submission/deployment.md:21` links to `docs/qa/stand.md`, whose `docs/qa/stand.md:5` contains `https://aifence.bezrabotnyi.com/`. The same public chat origin appears directly in `docs/qa/kes-contest-2026-11-27.md:15-16`. Both URLs returned HTTP 200 during this audit. The submission instructions require excluding this public stand.
- [ ] **P1 · jury URL is still incomplete.** `README.md:12-16`, `docs/submission/README.md:17`, `docs/submission/deployment.md:3-11`, and `docs/submission/form.md:15` still point to a future `sol-stand` read-back or leave the jury URL as a TODO. `/Users/user/AIfence/.runtime/sol-sessions/sol-stand/REPORT.md` exists, but its content does not supply a submission-ready tunnel URL. The kit therefore does not contain an opening jury link.
- [ ] **P1 · live KES acceptance failed.** `docs/qa/kes-contest-2026-11-27.md:15-18` records one cancelled run and one run that requested clarification without answering the price question or naming the Astra change. `docs/qa/e2e-27-11.md:7-18` records incomplete source-watch checks, no new Redmine issue, and no Dion receipt. `docs/submission/demo.md:21-22` correctly leaves final results conditional, but the script cannot be treated as an evidence-backed final take yet.
- [ ] **P1 · third-party notices are incomplete for shipped Market Watch imports.** `docs/submission/models-and-prior-work.md:59-70` lists `github.com/PuerkitoBio/goquery` v1.14.0 and `github.com/temoto/robotstxt` v1.1.2, and the code imports both from `apps/market-watch`. Their blocks are absent from `apps/market-watch/THIRD_PARTY_NOTICES.generated.md`, as the document itself acknowledges.
- [ ] **P2 · repository link does not open anonymously.** `README.md:20-23` and `docs/submission/README.md:23` link to GitHub, but the GitHub web/API checks returned HTTP 404. The documents disclose the private-repository decision, so this is an unresolved delivery condition rather than a hidden claim.
- [ ] **P2 · submission form still contains freeze placeholders.** `docs/submission/form.md:15-17` leaves the exact stand URL, final results, and release SHA for later insertion. `docs/submission/demo.md:22` contains `[N из M]`, `[K]`, `[U]`, and `[значение или честно “не рассчитана”]`. These values must be resolved before submission.

## Scope decisions

- No stand, browser session, deployment, restart, or live configuration was changed.
- No substantive submission claim was rewritten. The only source edit was the obvious grammar correction in `docs/submission/README.md:10`.
- The audit used the local workbook in Downloads for the economic cross-check. Customer credentials, tokens, cookies, and private access values were not read or recorded.
