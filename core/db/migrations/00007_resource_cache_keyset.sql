-- +goose NO TRANSACTION
-- +goose Up
-- The keyset scan of the AuthZEN resource search (ADR 0001, 7.6) orders and compares the external ids bytewise, with
-- COLLATE "C", so that the order of the pages does not depend on the collation of the database. The unique key of the table
-- (type_resource_id, external_id) is in that collation and cannot serve the scan, so a page of a large type would sort the
-- whole type. Additive: nothing reads this index but that scan, and the previous release does not know it.
--
-- The index is built CONCURRENTLY (it holds no lock that the pods of the previous release wait on), which a transaction does
-- not allow, hence NO TRANSACTION above. A run that is cut short (the deadline of the migration Job, a lost connection) leaves
-- the index INVALID, which the planner never uses, and CREATE INDEX IF NOT EXISTS would accept it as it is. goose records the
-- version only after the statements ran, so a run that starts again meets that leftover (or, if only the record was lost, a
-- valid index): the first statement drops it, whichever it is, and the second builds it.
DROP INDEX CONCURRENTLY IF EXISTS ix_resource_cache_type_external_c;
CREATE INDEX CONCURRENTLY ix_resource_cache_type_external_c
    ON resource_cache USING btree (type_resource_id, external_id COLLATE "C") WHERE active;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS ix_resource_cache_type_external_c;
