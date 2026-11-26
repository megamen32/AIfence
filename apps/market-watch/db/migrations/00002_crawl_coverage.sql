-- +goose Up
CREATE TABLE crawl_coverage (
  singleton boolean PRIMARY KEY CHECK (singleton),
  version bigint NOT NULL,
  report jsonb NOT NULL
);
INSERT INTO crawl_coverage (singleton, version, report) VALUES (true, 0, '{}'::jsonb);

-- +goose Down
DROP TABLE crawl_coverage;
