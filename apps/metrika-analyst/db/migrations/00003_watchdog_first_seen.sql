-- +goose Up
CREATE TABLE watchdog_first_seen (
    owner_id uuid NOT NULL,
    dataset_id text NOT NULL,
    signal_id text NOT NULL,
    detected_at timestamptz NOT NULL,
    detected_at_source text NOT NULL CHECK (detected_at_source IN ('agent_observed', 'first_registered')),
    PRIMARY KEY (owner_id, dataset_id, signal_id)
);

-- +goose Down
DROP TABLE watchdog_first_seen;
