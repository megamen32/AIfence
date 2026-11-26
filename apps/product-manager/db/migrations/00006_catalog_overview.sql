-- +goose Up
CREATE TABLE pm_catalog_overview (
    owner_id text PRIMARY KEY,
    as_of timestamptz NOT NULL,
    document jsonb NOT NULL
);

-- +goose Down
DROP TABLE pm_catalog_overview;
