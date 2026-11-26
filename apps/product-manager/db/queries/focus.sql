-- name: PMReadFocus :one
SELECT product_key, snapshot_id, hypothesis_id FROM pm_product_focus WHERE owner_id = $1;

-- name: PMSetFocus :execrows
INSERT INTO pm_product_focus (owner_id, product_key, snapshot_id, hypothesis_id) VALUES ($1, $2, $3, $4)
ON CONFLICT (owner_id) DO UPDATE SET
  product_key = EXCLUDED.product_key, snapshot_id = EXCLUDED.snapshot_id, hypothesis_id = EXCLUDED.hypothesis_id
WHERE (pm_product_focus.product_key, pm_product_focus.snapshot_id, pm_product_focus.hypothesis_id)
  IS DISTINCT FROM (EXCLUDED.product_key, EXCLUDED.snapshot_id, EXCLUDED.hypothesis_id);
