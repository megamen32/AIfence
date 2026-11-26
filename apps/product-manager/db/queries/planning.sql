-- name: PMDraftsForReview :many
SELECT d.body FROM pm_decisions d
JOIN pm_evidence e ON e.user_id::text = d.owner_id AND e.snapshot_id = d.body->>'evidence_snapshot_id'
WHERE d.owner_id = sqlc.arg(owner_id) AND d.body->'hypothesis'->>'status' = 'proposed'
  AND d.body->'task'->>'id' IS NULL
  AND (COALESCE(e.document->>'source_policy','') = '' OR (sqlc.arg(local_allowed)::boolean AND e.document->>'source_policy' = 'local_project_only'))
ORDER BY d.hypothesis_id;
