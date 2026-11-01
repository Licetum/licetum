-- +goose Up
-- History is retained independently; outbox retention must not delete history.
ALTER TABLE change_log DROP CONSTRAINT change_log_revision_fkey;

-- +goose Down
ALTER TABLE change_log ADD CONSTRAINT change_log_revision_fkey
    FOREIGN KEY (revision) REFERENCES outbox(revision) NOT VALID;
