-- name: StoreEvidence :exec
INSERT INTO pm_evidence (user_id, snapshot_id, as_of, observations, document)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, snapshot_id) DO NOTHING;

-- name: GetEvidence :one
SELECT document, imported_at FROM pm_evidence WHERE user_id=$1 AND snapshot_id=$2;

-- name: EvidenceIndex :many
SELECT snapshot_id, as_of, observations FROM pm_evidence WHERE user_id=sqlc.arg(user_id) AND (COALESCE(document->>'source_policy','') = '' OR (sqlc.arg(local_allowed)::boolean AND document->>'source_policy' = 'local_project_only'))
ORDER BY imported_at DESC, snapshot_id LIMIT 100;

-- name: EvidencePolicy :one
SELECT COALESCE(document->>'source_policy','')::text FROM pm_evidence WHERE user_id=$1 AND snapshot_id=$2;

-- name: WatchdogSnapshotIDs :many
SELECT snapshot_id FROM pm_evidence
WHERE user_id = sqlc.arg(user_id)
  AND (COALESCE(document->>'source_policy', '') = '' OR
       (sqlc.arg(local_allowed)::boolean AND document->>'source_policy' = 'local_project_only'))
  AND document @> '{"batches":[{"observations":[{"metric_key":"watchdog.alert"}]}]}'::jsonb
ORDER BY imported_at DESC, snapshot_id;
