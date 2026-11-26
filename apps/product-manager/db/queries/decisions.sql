-- name: PMRead :one
SELECT body FROM pm_decisions WHERE owner_id = $1 AND hypothesis_id = $2;

-- name: PMLock :one
SELECT body FROM pm_decisions WHERE owner_id = $1 AND hypothesis_id = $2 FOR UPDATE;

-- name: PMList :many
SELECT body FROM pm_decisions WHERE owner_id = $1 ORDER BY updated_at DESC, hypothesis_id LIMIT 100;

-- name: PMCreate :execrows
INSERT INTO pm_decisions (owner_id, hypothesis_id, version, body) VALUES ($1, $2, $3, $4)
ON CONFLICT (owner_id,hypothesis_id) DO NOTHING;

-- name: PMUpdate :execrows
UPDATE pm_decisions SET version = sqlc.arg(new_version), body = sqlc.arg(body), updated_at = now()
WHERE owner_id = sqlc.arg(owner_id) AND hypothesis_id = sqlc.arg(hypothesis_id) AND version = sqlc.arg(old_version);

-- name: PMRemember :exec
INSERT INTO pm_decision_revisions (owner_id, hypothesis_id, version, body) VALUES ($1,$2,$3,$4);

-- name: PMAddTask :execrows
INSERT INTO pm_backlog (owner_id,hypothesis_id,request_key,task_id,receipt) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT DO NOTHING;

-- name: PMReadTask :one
SELECT receipt FROM pm_backlog WHERE owner_id = $1 AND hypothesis_id = $2;

-- name: PMTaskKeyOwner :one
SELECT hypothesis_id FROM pm_backlog WHERE owner_id = $1 AND request_key = $2;
