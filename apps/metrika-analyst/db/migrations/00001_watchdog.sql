-- +goose Up
CREATE TABLE watchdog_owners (
    owner_id uuid PRIMARY KEY,
    snapshot_id uuid NOT NULL,
    product_key text,
    control_product_key text,
    metric_key text,
    segment text,
    next_job_id uuid,
    last_job_id uuid,
    last_result jsonb
);

-- +goose Down
DROP TABLE watchdog_owners;
