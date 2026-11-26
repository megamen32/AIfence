-- +goose Up
CREATE TABLE native_identity_seed (
 id integer PRIMARY KEY CHECK (id=1),
 seed bytea NOT NULL CHECK (octet_length(seed)=32)
);

-- +goose Down
DROP TABLE native_identity_seed;
