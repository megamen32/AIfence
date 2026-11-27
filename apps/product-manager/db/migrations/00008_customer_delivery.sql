-- +goose Up
-- One Redmine issue per owner and producer signal. The signal is the
-- idempotency key, so a repeated confirmation returns the same issue.
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

-- One morning Dion draft per owner and local day; sent only after the
-- owner's explicit confirmation in the panel.
CREATE TABLE pm_dion_drafts (
    owner_id text NOT NULL,
    day text NOT NULL,
    document jsonb NOT NULL,
    PRIMARY KEY (owner_id, day)
);

-- +goose Down
DROP TABLE pm_dion_drafts;
DROP TABLE pm_redmine_issues;
