-- +goose Up
CREATE INDEX observation_sku_time ON observations (upper(sku), observed_at);
CREATE INDEX latest_matrix_cursor ON latest_offers
  ((CASE WHEN sku='' THEN 'url:'||source||':'||url ELSE 'sku:'||upper(sku) END));

-- +goose Down
DROP INDEX latest_matrix_cursor;
DROP INDEX observation_sku_time;
