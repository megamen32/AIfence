-- +goose Up
ALTER TABLE watchdog_owners ADD COLUMN blind_selected boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE watchdog_owners DROP COLUMN blind_selected;
