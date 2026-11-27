-- +goose Up
CREATE TABLE adaptive_migration_batches (
 store_id text NOT NULL REFERENCES adaptive_stores(id) ON DELETE CASCADE,
 snapshot text NOT NULL,
 digest text NOT NULL,
 row_count integer NOT NULL CHECK (row_count > 0),
 kind text NOT NULL CHECK (kind IN ('tasks','responses')),
 imported_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (store_id,snapshot,digest)
);
-- +goose Down
DROP TABLE adaptive_migration_batches;
