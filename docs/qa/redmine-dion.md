# Redmine and Dion integration QA

Date: 2026-11-27, UTC+5
Branch: claude/project-thread-wv9xzh
Scope: Product Manager background delivery only.

## Verified
- The authorized UserIO Dion account exposed the owner-approved test conversation conv_21969b7f6d7f3d0d3c3cb7e9.
- A prepared test draft was sent with supported UserIO operations. Delivery returned delivered=true and a durable Dion receipt. The receipt value is intentionally omitted here.
- The delivery implementation uses one configured conversation, stores one draft per owner and local day, sends the exact stored snapshot, and returns the existing receipt on repeat calls.
- Focused Go checks pass for delivery, decision, evidence, handlers and path-check packages.
- The Redmine connector image built from redmine-mcp-server 2.23.0. Its initial launch failed because REDMINE_PER_USER_TRUST_PROXY was absent. The stand compose file now sets the required startup attestation. Final container health and a real issue receipt remain pending until the restart completes.
- A full Product Manager build remains blocked by the pre-existing agentsdk.JobContext.ReadAppTool compile error in internal/sourcewatch/schedule.go. That file is outside this integration scope.

## Expected live acceptance

Run scheduled sync_backlog twice for one owner and one verified signal. Record the first Redmine issue ID, AI flag and signal field, then confirm the second run returns the same issue without a duplicate. For an existing relevant open issue, record one journal ID and confirm the second run adds no journal.

Run morning_dion twice in the owner timezone. Record the Dion receipt and confirm the second run replays the receipt without another message.
