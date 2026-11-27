-- name: AdaptiveStoreIDs :many
SELECT id FROM adaptive_stores WHERE coalesce(metadata->>'legacy_source','')='' ORDER BY id;
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
-- name: LockMigrationStore :one
SELECT metadata FROM adaptive_stores WHERE id=$1 FOR UPDATE;
-- name: InsertMigrationBatch :execrows
INSERT INTO adaptive_migration_batches(store_id,snapshot,digest,row_count,kind)
VALUES(sqlc.arg(store_id),sqlc.arg(snapshot),sqlc.arg(digest),sqlc.arg(row_count),sqlc.arg(kind))
ON CONFLICT DO NOTHING;
-- name: LegacyMigrationCounts :many
SELECT s.id,
 coalesce(s.metadata->>'legacy_source','')::text AS source,
 coalesce(s.metadata->>'migration_snapshot','')::text AS snapshot,
 (SELECT count(*) FROM adaptive_tasks t WHERE t.store_id=s.id)::bigint AS tasks,
 (SELECT count(*) FROM adaptive_tasks t WHERE t.store_id=s.id AND t.payload ? 'failure')::bigint AS failures,
 (SELECT count(*) FROM adaptive_responses r WHERE r.store_id=s.id)::bigint AS responses,
 (SELECT count(*) FROM adaptive_responses r WHERE r.store_id=s.id AND r.payload->>'archive_state'='local_pending')::bigint AS raw_pending,
 (SELECT count(*) FROM adaptive_migration_batches b WHERE b.store_id=s.id)::bigint AS batches,
 (SELECT coalesce(sum(b.row_count),0) FROM adaptive_migration_batches b WHERE b.store_id=s.id AND b.kind='tasks')::bigint AS task_receipts,
 (SELECT coalesce(sum(b.row_count),0) FROM adaptive_migration_batches b WHERE b.store_id=s.id AND b.kind='responses')::bigint AS response_receipts
FROM adaptive_stores s WHERE coalesce(s.metadata->>'legacy_source','')<>'' ORDER BY source;
