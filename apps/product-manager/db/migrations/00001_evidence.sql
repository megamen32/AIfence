-- +goose Up
CREATE TABLE pm_evidence (
    user_id uuid NOT NULL,
    snapshot_id text NOT NULL,
    as_of text NOT NULL,
    observations integer NOT NULL,
    document jsonb NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, snapshot_id)
);
-- +goose Down
DROP TABLE pm_evidence;
