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

-- name: PMPendingMeasurements :many
SELECT d.body FROM pm_decisions AS d
JOIN pm_evidence AS basis ON basis.user_id::text = d.owner_id AND basis.snapshot_id = d.body->>'evidence_snapshot_id'
JOIN pm_evidence AS incoming ON incoming.user_id = basis.user_id AND incoming.snapshot_id = sqlc.arg(snapshot_id)
WHERE d.owner_id = sqlc.arg(owner_id)
  AND COALESCE(basis.document->>'source_policy','') = COALESCE(incoming.document->>'source_policy','')
  AND d.body->'task'->>'id' IS NOT NULL
  AND d.body->'hypothesis'->>'status' IN ('in_backlog','awaiting_measurement')
  AND (d.hypothesis_id = ANY(sqlc.arg(hypothesis_ids)::text[])
       OR (d.body->'assessment'->>'release_id' IS NOT NULL
           AND (d.body->'hypothesis'->>'product_key' = ANY(sqlc.arg(product_keys)::text[])
                OR d.body->'hypothesis'->'success_criterion'->>'control_product_key' = ANY(sqlc.arg(product_keys)::text[]))))
ORDER BY d.hypothesis_id;

-- name: PMLinkedMeasurementSnapshots :many
SELECT e.snapshot_id FROM pm_evidence AS e
JOIN pm_evidence AS incoming ON incoming.user_id=e.user_id AND incoming.snapshot_id=sqlc.arg(incoming_snapshot_id)
WHERE e.user_id::text=sqlc.arg(owner_id)::text
  AND COALESCE(e.document->>'source_policy','') = COALESCE(incoming.document->>'source_policy','')
  AND (e.document @> jsonb_build_object('batches',jsonb_build_array(jsonb_build_object('observations',
      jsonb_build_array(jsonb_build_object('details',jsonb_build_object('hypothesis_id',sqlc.arg(hypothesis_id)::text))))))
    OR jsonb_path_exists(e.document,
       '$.batches[*] ? (@.source == "metrika" || @.source == "sales").observations[*] ? (
          @.kind == "metric" && @.status == "observed" && @.metric_key == $metric &&
          (@.product_key == $product || @.product_key == $control) &&
          !exists(@.details.hypothesis_id) && @.details.measurement_kind == "product_outcome" &&
          @.details.complete == true && @.details.quality == "comparable" &&
          (@.period.from == $before_from && @.period.to == $before_to ||
           @.period.from == $after_from && @.period.to == $after_to) &&
          @.source_ref.source_id == $sources[*])',
       jsonb_build_object('metric',sqlc.arg(metric_key)::text,'product',sqlc.arg(product_key)::text,
         'control',sqlc.arg(control_key)::text,'before_from',sqlc.arg(before_from)::text,
         'before_to',sqlc.arg(before_to)::text,'after_from',sqlc.arg(after_from)::text,
         'after_to',sqlc.arg(after_to)::text,'sources',to_jsonb(sqlc.arg(source_ids)::text[]))))

ORDER BY e.imported_at,e.snapshot_id;
