-- name: CurrentCrawlCoverage :one
SELECT version, report FROM crawl_coverage WHERE singleton = true;

-- name: ReplaceCrawlCoverage :execrows
UPDATE crawl_coverage SET report = sqlc.arg(new_report)::jsonb, version = version + 1
WHERE singleton = true AND version = sqlc.arg(expected_version)::bigint;
