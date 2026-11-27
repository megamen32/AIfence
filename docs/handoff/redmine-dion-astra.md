# Handoff: Redmine + Dion background delivery (PR #7)

Branch `claude/project-thread-wv9xzh`, PR https://github.com/megamen32/AIfence/pull/7. Local worktree on the Mac: `/Users/user/AIfence-worktrees/redmine-dion`.

## Owner decision (27.11, thread with Nikita)
The customer asked at kick-off (transcript 12:44–12:45, ТЗ §5 p.3, Q&A 13:03): the agent itself files described tasks into the backlog with the flag "сгенерирована ИИ-помощником", after checking whether the backlog already has a task for the problem; timely notifications to the product owner are preferred over missed problems. So everything runs in the background, no confirmation button. Redmine is reached through MCP. The "Ask before acting" line in AGENTS.md must be narrowed to chat answers (not yet edited).

## What is in the code (apps/product-manager)
- `internal/delivery/redmine.go` — `Redmine{MCP, DB, Project, Tracker, PublicURL}`. `Deliver(owner, record)`: local receipt `pm_redmine_issues` → Redmine search by custom field "ID сигнала" (`list_redmine_issues` filter `cf_<id>`) → open issue for the same product gets a comment (`update_redmine_issue` notes) instead of a duplicate → otherwise `create_redmine_issue` with tracker "Улучшение", fields "Сгенерирована ИИ-помощником"=1 and "ID сигнала", description with before/after numbers from `SignalContrast`, control, criterion and all evidence IDs. `Eligible()` = card has a verified signal window.
- `internal/delivery/dion.go` — `Dion.Prepare` (`userio.messages.prepare`, one per owner/day, only to `dion_test_chat`) and `Dion.Send` (`userio.messages.send` with exact snapshot; refuses another chat; never sends twice).
- `customer_delivery.go` (package main) — MCPs `redmine` (`http://redmine-mcp:8000/mcp`, token injected as `X-Redmine-API-Key`, shared) and `userio` (`https://msg.bezrabotnyi.com/mcp`, per-user token). Jobs `sync_backlog` v1 (hourly, self re-queue) and `morning_dion` v1 (04:00 UTC = 09:00 UTC+5: sync, then prepare+send). `Ensure()` queues both for the owner; called from `/delivery` and after a manual card approval. Page `/delivery` shows issues, next runs, last Dion note, buttons "Сверить сейчас" / "Отправить сейчас". Env vars `redmine_project` (personal), `redmine_public_url`, `dion_test_chat`.
- `handlers/home.go` — `WithDelivery`; card shows `Redmine #N` link instead of `PM-…` when an issue exists; manual "Создать эту задачу" also delivers.
- Migration `00008_customer_delivery.sql` (`pm_redmine_issues`, `pm_dion_drafts`, `pm_delivery_schedule`).
- Tests `internal/delivery/delivery_test.go` (fake MCPs + in-memory SQL driver).

## Status
- First commit (connection-based) built green with `scripts/personal-build-apps.py --commit` (all PM tests ok).
- The MCP/background rewrite (last commit) is NOT compiled or tested yet. Run: `go test ./internal/delivery/` and the native build.
- Not deployed. `scripts/deploy-apps` requires the commit to be `main`.

## Why MCP for Redmine (verified in core source)
- Job app-tool bindings accept only `ReadOnly` tools (`service/execution/job_tool_read.go`), so the Redmine Airlock app cannot be written from PM.
- Airlock connections require HTTPS for non-localhost (`networkpolicy.ParseURL`), so an http Redmine connection is refused. MCP calls use the policy client with `AGENT_HTTP_PRIVATE_CIDRS` (set on the Mac Airlock), so http in the private network works.

## Remaining steps
1. Fix compile/test of the rewrite; add a call to `Ensure` from the panel Home handler so the loop starts whenever the owner opens PM.
2. Start Redmine MCP: `docker --context colima-aifence compose -f scripts/stand/redmine-mcp/compose.yaml up -d --build` (files exist only in the Mac worktree, not committed yet — commit them). It joins networks `aifence` (Airlock) and `aifence-personal-redmine_backend` (Redmine). My attempt to start it was blocked by the auto-mode classifier; needs owner OK.
3. Redmine setup is DONE on the Mac Redmine: user `ai-assistant` («ИИ помощник», id 17, Developer in project `personal`), custom fields id 1 "Сгенерирована ИИ-помощником" (bool) and id 2 "ID сигнала" (string). API key: `~/.aifence-private/redmine-ai-assistant.key` (do not print).
4. After merge + deploy: in Airlock → Product Manager set MCP credential `redmine` = that key; MCP `userio` = owner's personal UserIO bearer token (UserIO on server-100, msg.bezrabotnyi.com; local UserIO on the Mac is crash-looping and has no Dion account); `dion_test_chat` = one of the owner's Dion test chats (all three are test chats per owner): `conv_21969b7f6d7f3d0d3c3cb7e9`, `conv_a8f3dc34b832d6d597478c88`, `conv_7e7129f62f8c1d6b2b14724e`.
5. Live check: open PM → `/delivery` → "Сверить сейчас"; record Redmine issue IDs and the Dion receipt in `docs/qa/`, screenshots for the video; run sync twice to show no duplicates.
6. Update AGENTS.md "Ask before acting" to: chat asks before changing anything; the background backlog loop files AI-flagged tasks itself after checking for an existing one.
