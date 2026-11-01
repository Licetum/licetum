-- +goose Up
-- subject_kind was an informational label; it must not become a decision filter.
ALTER TABLE assignment_rule DROP COLUMN subject_kind;
ALTER TABLE assignment_rule ADD COLUMN subject_type text;
ALTER TABLE assignment_rule ADD CONSTRAINT ck_assignment_rule_subject_type CHECK (
 subject_type IS NULL OR (subject IS NOT NULL AND char_length(subject_type) BETWEEN 1 AND 64 AND subject_type<>'group')
);

-- +goose Down
ALTER TABLE assignment_rule DROP COLUMN subject_type;
ALTER TABLE assignment_rule ADD COLUMN subject_kind text CHECK(subject_kind IN('user','service','agent','group'));
