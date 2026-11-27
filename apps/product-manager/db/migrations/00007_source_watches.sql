-- +goose Up
CREATE TABLE pm_source_watches (
    owner_id text NOT NULL,
    watch_id text NOT NULL,
    config jsonb NOT NULL,
    active boolean NOT NULL DEFAULT true,
    last_monday text NOT NULL DEFAULT '',
    last_status text NOT NULL DEFAULT '',
    analysis_run_id text NOT NULL DEFAULT '',
    PRIMARY KEY (owner_id, watch_id)
);

CREATE TABLE pm_watch_signals (
    owner_id text NOT NULL,
    signal_id text NOT NULL,
    document jsonb NOT NULL,
    analysis_run_id text NOT NULL DEFAULT '',
    PRIMARY KEY (owner_id, signal_id)
);

-- One confirmed local Redmine draft per owner and signal. The idempotency key
-- is the signal itself, so a repeated confirmation returns the same draft.
CREATE TABLE pm_signal_drafts (
    owner_id text NOT NULL,
    signal_id text NOT NULL,
    issue_id text NOT NULL,
    document jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, signal_id),
    UNIQUE (owner_id, issue_id),
    FOREIGN KEY (owner_id, signal_id) REFERENCES pm_watch_signals(owner_id, signal_id)
);

-- +goose Down
DROP TABLE pm_signal_drafts;
DROP TABLE pm_watch_signals;
DROP TABLE pm_source_watches;
