-- +goose Up
CREATE TABLE adaptive_stores (
 id text PRIMARY KEY,
 host text NOT NULL UNIQUE,
 root_url text NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 metadata jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE adaptive_tasks (
 store_id text NOT NULL REFERENCES adaptive_stores(id) ON DELETE CASCADE,
 url text NOT NULL,
 state text NOT NULL,
 attempts integer NOT NULL CHECK (attempts >= 0),
 next_attempt timestamptz NOT NULL,
 payload jsonb NOT NULL,
 PRIMARY KEY (store_id,url)
);
CREATE INDEX adaptive_tasks_due ON adaptive_tasks (store_id,state,next_attempt);
CREATE TABLE adaptive_responses (
 store_id text NOT NULL REFERENCES adaptive_stores(id) ON DELETE CASCADE,
 url text NOT NULL,
 captured_at timestamptz NOT NULL,
 sha256 text NOT NULL,
 payload jsonb NOT NULL,
 PRIMARY KEY (store_id,url,captured_at,sha256)
);
CREATE TABLE adaptive_versions (
 store_id text NOT NULL REFERENCES adaptive_stores(id) ON DELETE CASCADE,
 version text NOT NULL,
 payload jsonb NOT NULL,
 PRIMARY KEY (store_id,version)
);
-- A domain lease is not a second delivery queue. Airlock owns job delivery.
-- This prevents duplicate effects from different jobs operating on one store.
CREATE TABLE adaptive_store_leases (
 store_id text PRIMARY KEY,
 job_id text NOT NULL,
 attempt integer NOT NULL,
 expires_at timestamptz NOT NULL
);
-- +goose Down
DROP TABLE adaptive_store_leases;
DROP TABLE adaptive_versions;
DROP TABLE adaptive_responses;
DROP TABLE adaptive_tasks;
DROP TABLE adaptive_stores;
