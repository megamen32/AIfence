-- name: ObservedSKUHistory :many
SELECT o.offer FROM observations o
WHERE upper(o.sku) = upper(sqlc.arg(sku)::text)
  AND o.observed_at >= sqlc.arg(from_time)::timestamptz
  AND o.observed_at < sqlc.arg(to_time)::timestamptz
ORDER BY o.observed_at DESC,o.source,o.url,o.basis,o.quantity,o.max_quantity
LIMIT 4001;

-- name: SKUWindowObservations :many
-- Every stored observation of one exact SKU inside the union of the two
-- requested weeks. Picking the latest reading per week and series happens in
-- Go, where the series identity and its comparability rules already live.
SELECT o.offer FROM observations o
WHERE upper(o.sku) = upper(sqlc.arg(sku)::text)
  AND o.observed_at >= sqlc.arg(from_time)::timestamptz
  AND o.observed_at < sqlc.arg(to_time)::timestamptz
ORDER BY o.observed_at,o.source,o.url,o.basis,o.quantity,o.max_quantity
LIMIT 4001;

-- name: MatrixPage :many
WITH page AS (
    SELECT DISTINCT CASE WHEN l.sku='' THEN 'url:'||l.source||':'||l.url ELSE 'sku:'||upper(l.sku) END AS cursor_key
    FROM latest_offers l
    WHERE (CASE WHEN l.sku='' THEN 'url:'||l.source||':'||l.url ELSE 'sku:'||upper(l.sku) END) > sqlc.arg(after_key)::text
    ORDER BY cursor_key LIMIT sqlc.arg(product_limit)::int
)
SELECT p.cursor_key::text AS cursor_key, l.offer,
       coalesce(b.offer,'null'::jsonb)::jsonb AS baseline
FROM page p JOIN latest_offers l
 ON (CASE WHEN l.sku='' THEN 'url:'||l.source||':'||l.url ELSE 'sku:'||upper(l.sku) END) = p.cursor_key
LEFT JOIN LATERAL (
  SELECT o.offer FROM observations o
  WHERE (o.source,o.sku,o.url,o.basis,o.quantity,o.max_quantity) =
        (l.source,l.sku,l.url,l.basis,l.quantity,l.max_quantity)
    AND o.observed_at >= sqlc.arg(start_at)::timestamptz AND o.observed_at < l.observed_at
  ORDER BY o.observed_at LIMIT 1
) b ON true
ORDER BY p.cursor_key,l.source,l.url,l.basis,l.quantity,l.max_quantity;
