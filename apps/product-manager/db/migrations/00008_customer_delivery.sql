-- +goose Up
-- One Redmine issue per owner and producer signal. The signal is the
-- idempotency key, so a repeated sync returns the same issue.
CREATE TABLE pm_redmine_issues (
    owner_id text NOT NULL,
    signal_id text NOT NULL,
    hypothesis_id text NOT NULL,
    issue_id integer NOT NULL,
    receipt jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, signal_id)
);
CREATE INDEX pm_redmine_issues_hypothesis ON pm_redmine_issues (owner_id, hypothesis_id);

-- One morning Dion note per owner and local day.
CREATE TABLE pm_dion_drafts (
    owner_id text NOT NULL,
    day text NOT NULL,
    document jsonb NOT NULL,
    PRIMARY KEY (owner_id, day)
);

-- The scheduler keeps one durable next slot for each owner and job kind.
CREATE TABLE pm_delivery_schedule (
    owner_id text NOT NULL,
    kind text NOT NULL,
    next_at timestamptz NOT NULL,
    PRIMARY KEY (owner_id, kind)
);

-- +goose Down
DROP TABLE pm_delivery_schedule;
DROP TABLE pm_dion_drafts;
DROP TABLE pm_redmine_issues;
