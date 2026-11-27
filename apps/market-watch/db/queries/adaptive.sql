-- name: AdaptiveStoreIDs :many
SELECT id FROM adaptive_stores ORDER BY id;
-- name: AdaptiveStoreMeta :one
SELECT metadata,revision FROM adaptive_stores WHERE id=$1;
-- name: AdaptiveTaskRows :many
SELECT payload FROM adaptive_tasks WHERE store_id=$1 ORDER BY url;
-- name: AdaptiveResponseRows :many
SELECT payload FROM adaptive_responses WHERE store_id=$1 ORDER BY captured_at,url,sha256;
-- name: CreateAdaptiveStore :one
INSERT INTO adaptive_stores(id,host,root_url,metadata)
VALUES (sqlc.arg(id),sqlc.arg(host),sqlc.arg(root_url),sqlc.arg(metadata)::jsonb)
ON CONFLICT DO NOTHING RETURNING revision;
-- name: UpdateAdaptiveStore :one
UPDATE adaptive_stores SET metadata=sqlc.arg(metadata)::jsonb,revision=revision+1,updated_at=now()
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision) RETURNING revision;
-- name: UpsertAdaptiveTasks :exec
INSERT INTO adaptive_tasks(store_id,url,state,attempts,next_attempt,payload)
SELECT sqlc.arg(store_id),j->>'url',j->>'state',(j->>'attempts')::int,(j->>'next_attempt')::timestamptz,j
FROM jsonb_array_elements(sqlc.arg(tasks)::jsonb) AS t(j)
ON CONFLICT(store_id,url) DO UPDATE SET state=excluded.state,attempts=excluded.attempts,next_attempt=excluded.next_attempt,payload=excluded.payload
WHERE adaptive_tasks.payload IS DISTINCT FROM excluded.payload;
-- name: InsertAdaptiveResponses :exec
INSERT INTO adaptive_responses(store_id,url,captured_at,sha256,payload)
SELECT sqlc.arg(store_id),j->>'url',(j->>'at')::timestamptz,j->>'sha256',j
FROM jsonb_array_elements(sqlc.arg(receipts)::jsonb) AS t(j)
ON CONFLICT DO NOTHING;
-- name: GetAdaptiveVersion :one
SELECT payload FROM adaptive_versions WHERE store_id=$1 AND version=$2;
-- name: InsertAdaptiveVersion :exec
INSERT INTO adaptive_versions(store_id,version,payload) VALUES(sqlc.arg(store_id),sqlc.arg(version),sqlc.arg(payload)::jsonb)
ON CONFLICT DO NOTHING;
-- name: ClaimAdaptiveLease :one
INSERT INTO adaptive_store_leases(store_id,job_id,attempt,expires_at)
VALUES(sqlc.arg(store_id),sqlc.arg(job_id),sqlc.arg(attempt),sqlc.arg(expires_at))
ON CONFLICT(store_id) DO UPDATE SET job_id=excluded.job_id,attempt=excluded.attempt,expires_at=excluded.expires_at
WHERE adaptive_store_leases.expires_at<=now()
 OR (adaptive_store_leases.job_id=excluded.job_id AND adaptive_store_leases.attempt<=excluded.attempt)
RETURNING job_id;
-- name: ReleaseAdaptiveLease :exec
DELETE FROM adaptive_store_leases WHERE store_id=$1 AND job_id=$2 AND attempt=$3;
-- name: CheckAdaptiveLease :one
SELECT job_id FROM adaptive_store_leases
WHERE store_id=$1 AND job_id=$2 AND attempt=$3 AND expires_at>now()
FOR UPDATE;
-- name: DueAdaptiveTasks :many
SELECT payload FROM adaptive_tasks
WHERE store_id=sqlc.arg(store_id) AND state IN ('queued','retry','done')
AND next_attempt<=sqlc.arg(as_of)::timestamptz
ORDER BY (state='done'),next_attempt,url LIMIT sqlc.arg(max_rows)::integer;
