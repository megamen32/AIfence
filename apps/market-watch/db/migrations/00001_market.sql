-- +goose Up
CREATE TABLE observations (
  source text NOT NULL,
  sku text NOT NULL,
  url text NOT NULL,
  basis text NOT NULL,
  quantity bigint NOT NULL,
  max_quantity bigint NOT NULL,
  observed_at timestamptz NOT NULL,
  offer jsonb NOT NULL,
  PRIMARY KEY (source,sku,url,basis,quantity,max_quantity,observed_at)
);
CREATE TABLE latest_offers (
  source text NOT NULL,
  sku text NOT NULL,
  url text NOT NULL,
  basis text NOT NULL,
  quantity bigint NOT NULL,
  max_quantity bigint NOT NULL,
  observed_at timestamptz NOT NULL,
  offer jsonb NOT NULL,
  PRIMARY KEY (source,sku,url,basis,quantity,max_quantity)
);
CREATE INDEX latest_sku ON latest_offers (upper(sku));
CREATE INDEX observation_dates ON observations (observed_at);

-- +goose Down
DROP TABLE latest_offers;
DROP TABLE observations;
