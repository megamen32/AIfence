-- name: StoreEvidence :exec
INSERT INTO pm_evidence (user_id, snapshot_id, as_of, observations, document)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, snapshot_id) DO NOTHING;

-- name: GetEvidence :one
SELECT document FROM pm_evidence WHERE user_id=$1 AND snapshot_id=$2;

-- name: EvidenceIndex :many
SELECT snapshot_id, as_of, observations FROM pm_evidence WHERE user_id=sqlc.arg(user_id) AND (sqlc.arg(local_allowed)::boolean OR COALESCE(document->>'source_policy','') <> 'local_project_only')
ORDER BY imported_at DESC, snapshot_id LIMIT 100;

-- name: EvidencePolicy :one
SELECT COALESCE(document->>'source_policy','')::text FROM pm_evidence WHERE user_id=$1 AND snapshot_id=$2;
