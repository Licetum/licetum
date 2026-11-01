-- +goose Up
ALTER TABLE log_settings DROP COLUMN salt;

-- +goose Down
ALTER TABLE log_settings ADD COLUMN salt text NOT NULL DEFAULT '';
