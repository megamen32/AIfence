-- +goose Up
CREATE TABLE pm_product_focus (
    owner_id text PRIMARY KEY,
    product_key text NOT NULL,
    snapshot_id text NOT NULL,
    hypothesis_id text NOT NULL
);

-- +goose Down
DROP TABLE pm_product_focus;
