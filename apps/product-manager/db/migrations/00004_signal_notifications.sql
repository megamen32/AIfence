-- +goose Up
CREATE TABLE pm_signal_notifications (
    owner_id text NOT NULL,
    signal_key text NOT NULL,
    PRIMARY KEY (owner_id, signal_key)
);

-- +goose Down
DROP TABLE pm_signal_notifications;
