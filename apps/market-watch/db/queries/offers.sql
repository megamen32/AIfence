-- name: ImportObservations :exec
WITH incoming AS (
  SELECT j.offer->>'source' AS source, j.offer->>'sku' AS sku, j.offer->>'url' AS url,
    j.offer->>'price_basis' AS basis, coalesce((j.offer->>'quantity')::bigint,0) AS quantity, coalesce((j.offer->>'max_quantity')::bigint,0) AS max_quantity,
    (j.offer->>'captured_at')::timestamptz AS observed_at, j.offer AS offer
  FROM jsonb_array_elements(sqlc.arg(batch)::jsonb) AS j(offer)
), saved AS (
  INSERT INTO observations (source,sku,url,basis,quantity,max_quantity,observed_at,offer)
  SELECT i.source,i.sku,i.url,i.basis,i.quantity,i.max_quantity,i.observed_at,i.offer FROM incoming i
  ON CONFLICT DO NOTHING
  RETURNING *
)
INSERT INTO latest_offers (source,sku,url,basis,quantity,max_quantity,observed_at,offer)
SELECT DISTINCT ON (s.source,s.sku,s.url,s.basis,s.quantity,s.max_quantity) s.source,s.sku,s.url,s.basis,s.quantity,s.max_quantity,s.observed_at,s.offer
FROM saved s ORDER BY s.source,s.sku,s.url,s.basis,s.quantity,s.max_quantity,s.observed_at DESC
ON CONFLICT (source,sku,url,basis,quantity,max_quantity) DO UPDATE SET
observed_at=excluded.observed_at, offer=excluded.offer
WHERE excluded.observed_at > latest_offers.observed_at;

-- name: SearchOffers :many
SELECT l.offer FROM latest_offers l
WHERE (sqlc.arg(query)::text='' OR upper(l.sku)=upper(sqlc.arg(query)) OR l.url=sqlc.arg(query) OR strpos(lower(l.offer->>'name'),lower(sqlc.arg(query)))>0)
  AND (sqlc.arg(source)::text='' OR l.source=sqlc.arg(source))
  AND (sqlc.arg(basis)::text='' OR l.basis=sqlc.arg(basis))
  AND (sqlc.arg(min_qty)::bigint<0 OR l.quantity=sqlc.arg(min_qty))
  AND (sqlc.arg(max_qty)::bigint<0 OR l.max_quantity=sqlc.arg(max_qty))
ORDER BY (upper(l.sku)=upper(sqlc.arg(query))) DESC,l.sku,l.source,l.url,l.basis,l.quantity,l.max_quantity
LIMIT sqlc.arg(maxrows)::int;

-- name: CatalogCounts :many
SELECT l.source,count(*) AS offers,max(l.observed_at)::timestamptz AS observed_at
FROM latest_offers l GROUP BY l.source ORDER BY l.source;

-- name: OfferChanges :many
SELECT prior.offer::jsonb AS old_offer, cur.offer::jsonb AS new_offer
FROM observations cur
JOIN LATERAL (
  SELECT p.offer FROM observations p
  WHERE (p.source,p.sku,p.url,p.basis,p.quantity,p.max_quantity)=
        (cur.source,cur.sku,cur.url,cur.basis,cur.quantity,cur.max_quantity)
    AND p.observed_at < cur.observed_at
  ORDER BY p.observed_at DESC LIMIT 1
) prior ON true
WHERE cur.observed_at>=sqlc.arg(start_at)::timestamptz AND cur.observed_at<sqlc.arg(end_at)::timestamptz
  AND (sqlc.arg(query)::text='' OR upper(cur.sku)=upper(sqlc.arg(query)) OR cur.url=sqlc.arg(query) OR strpos(lower(cur.offer->>'name'),lower(sqlc.arg(query)))>0)
  AND (prior.offer->>'price_minor')::bigint<>(cur.offer->>'price_minor')::bigint
ORDER BY cur.observed_at DESC,cur.sku,cur.source,cur.url,cur.basis,cur.quantity,cur.max_quantity
LIMIT 101;

-- name: CatalogExport :many
SELECT l.offer FROM latest_offers l
WHERE (l.source,l.sku,l.url,l.basis,l.quantity,l.max_quantity)>(sqlc.arg(after_source)::text,sqlc.arg(after_sku)::text,sqlc.arg(after_url)::text,sqlc.arg(after_basis)::text,sqlc.arg(after_quantity)::bigint,sqlc.arg(after_max_quantity)::bigint)
ORDER BY l.source,l.sku,l.url,l.basis,l.quantity,l.max_quantity LIMIT 500;
