-- name: UpsertCatalogOverview :execrows
INSERT INTO pm_catalog_overview (owner_id, as_of, document)
VALUES ($1, $2, $3)
ON CONFLICT (owner_id) DO UPDATE SET as_of = EXCLUDED.as_of, document = EXCLUDED.document
WHERE pm_catalog_overview.as_of <= EXCLUDED.as_of;

-- name: GetCatalogOverview :one
SELECT document FROM pm_catalog_overview WHERE owner_id = $1;
