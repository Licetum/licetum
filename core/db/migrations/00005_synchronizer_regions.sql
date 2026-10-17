-- The regions a synchronizer's source can be reached from (F6). Empty: from anywhere. A worker started with WORKER__REGION claims
-- the synchronization jobs of the types whose source it can reach, and leaves the others to the workers of the allowed regions;
-- a worker with no region claims every job. Additive: the previous release ignores the column and its inserts take the default.

-- +goose Up
ALTER TABLE synchronizer ADD COLUMN regions text[] DEFAULT '{}'::text[] NOT NULL;

-- +goose Down
ALTER TABLE synchronizer DROP COLUMN regions;
