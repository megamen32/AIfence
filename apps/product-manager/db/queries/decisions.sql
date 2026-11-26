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

-- name: PMClaimTaskKey :one
INSERT INTO pm_backlog_replay_keys AS keys (owner_id, request_key, hypothesis_id)
VALUES ($1, $2, $3)
ON CONFLICT (owner_id, request_key) DO UPDATE SET hypothesis_id = keys.hypothesis_id
RETURNING hypothesis_id;

-- name: PMClaimSignalNotification :execrows
INSERT INTO pm_signal_notifications (owner_id, signal_key) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: PMReleaseSignalNotification :exec
DELETE FROM pm_signal_notifications WHERE owner_id = $1 AND signal_key = $2;
