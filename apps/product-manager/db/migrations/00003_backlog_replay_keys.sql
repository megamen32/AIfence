-- +goose Up
CREATE TABLE pm_backlog_replay_keys (
    owner_id text NOT NULL,
    request_key text NOT NULL,
    hypothesis_id text NOT NULL,
    PRIMARY KEY (owner_id, request_key),
    FOREIGN KEY (owner_id, hypothesis_id) REFERENCES pm_decisions(owner_id, hypothesis_id)
);

INSERT INTO pm_backlog_replay_keys (owner_id, request_key, hypothesis_id)
SELECT owner_id, request_key, hypothesis_id FROM pm_backlog;

-- +goose Down
DROP TABLE pm_backlog_replay_keys;
