-- +goose Up
ALTER TABLE revision_state ADD COLUMN epoch integer NOT NULL DEFAULT 1 CHECK (epoch > 0);
INSERT INTO revision_state (scope, revision)
SELECT 'global', COALESCE((SELECT revision FROM revision_state WHERE scope = 'rules'), 0);
DELETE FROM revision_state WHERE scope <> 'global';
ALTER TABLE revision_state ADD CONSTRAINT revision_global CHECK (scope = 'global');

-- +goose Down
ALTER TABLE revision_state DROP CONSTRAINT revision_global;
UPDATE revision_state SET scope = 'rules' WHERE scope = 'global';
ALTER TABLE revision_state DROP COLUMN epoch;
