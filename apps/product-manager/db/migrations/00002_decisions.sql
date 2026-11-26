-- +goose Up
CREATE TABLE pm_decisions (
    owner_id text NOT NULL,
    hypothesis_id text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    body jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, hypothesis_id)
);
CREATE INDEX pm_decisions_recent ON pm_decisions(owner_id, updated_at DESC, hypothesis_id);

CREATE TABLE pm_decision_revisions (
    owner_id text NOT NULL,
    hypothesis_id text NOT NULL,
    version bigint NOT NULL,
    body jsonb NOT NULL,
    PRIMARY KEY (owner_id, hypothesis_id, version),
    FOREIGN KEY (owner_id, hypothesis_id) REFERENCES pm_decisions(owner_id, hypothesis_id)
);

CREATE TABLE pm_backlog (
    owner_id text NOT NULL,
    hypothesis_id text NOT NULL,
    request_key text NOT NULL,
    task_id text NOT NULL,
    receipt jsonb NOT NULL,
    PRIMARY KEY (owner_id, hypothesis_id),
    UNIQUE (owner_id, request_key),
    UNIQUE (owner_id, task_id),
    FOREIGN KEY (owner_id, hypothesis_id) REFERENCES pm_decisions(owner_id, hypothesis_id)
);

-- +goose Down
DROP TABLE pm_backlog;
DROP TABLE pm_decision_revisions;
DROP TABLE pm_decisions;
