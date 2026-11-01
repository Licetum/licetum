-- +goose Up
ALTER TABLE type_resource ADD COLUMN synced_at timestamptz;

-- +goose Down
ALTER TABLE type_resource DROP COLUMN synced_at;
