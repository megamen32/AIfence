-- name: AddSourceWatch :exec
INSERT INTO pm_source_watches(owner_id,watch_id,config) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING;

-- name: GetSourceWatch :one
SELECT * FROM pm_source_watches WHERE owner_id=$1 AND watch_id=$2;

-- name: ListSourceWatches :many
SELECT * FROM pm_source_watches WHERE owner_id=$1 ORDER BY active DESC, watch_id;

-- name: StopSourceWatch :execrows
UPDATE pm_source_watches SET active=false WHERE owner_id=$1 AND watch_id=$2;

-- name: ReportSourceWatch :exec
UPDATE pm_source_watches SET last_monday=$3,last_status=$4,analysis_run_id=CASE WHEN sqlc.arg(analysis_run_id)::text <> '' THEN sqlc.arg(analysis_run_id)::text ELSE analysis_run_id END
WHERE owner_id=$1 AND watch_id=$2 AND last_monday <= $3;

-- name: PinSourceWatchSignal :one
INSERT INTO pm_watch_signals (owner_id,signal_id,document)
SELECT sqlc.arg(owner_id), sqlc.arg(signal_id), sqlc.arg(document)::jsonb
WHERE EXISTS (SELECT 1 FROM pm_source_watches WHERE owner_id=sqlc.arg(owner_id) AND watch_id=sqlc.arg(watch_id) AND active)
ON CONFLICT (owner_id,signal_id) DO UPDATE SET signal_id=EXCLUDED.signal_id
RETURNING document, analysis_run_id;

-- name: RecordSourceWatchRun :execrows
UPDATE pm_watch_signals SET analysis_run_id=$3
WHERE owner_id=$1 AND signal_id=$2 AND (analysis_run_id='' OR analysis_run_id=$3);

-- name: GetSourceWatchSignal :one
SELECT * FROM pm_watch_signals WHERE owner_id=$1 AND signal_id=$2;

-- name: GetSourceWatchSignalByRun :one
SELECT * FROM pm_watch_signals WHERE owner_id=$1 AND analysis_run_id=$2;

-- name: AddSignalDraft :one
INSERT INTO pm_signal_drafts (owner_id,signal_id,issue_id,document)
VALUES (sqlc.arg(owner_id),sqlc.arg(signal_id),sqlc.arg(issue_id),sqlc.arg(document)::jsonb)
ON CONFLICT (owner_id,signal_id) DO UPDATE SET signal_id=EXCLUDED.signal_id
RETURNING issue_id, document AS document;

-- name: ReportInitialWatchEnqueue :exec
UPDATE pm_source_watches SET last_status=$3 WHERE owner_id=$1 AND watch_id=$2 AND last_monday='';
