-- +goose Up
ALTER TABLE assignment_rule ADD COLUMN subject_group_id uuid;
UPDATE assignment_rule SET subject_group_id=substr(subject,7)::uuid,subject=NULL WHERE subject LIKE 'group:%';
ALTER TABLE assignment_rule ADD FOREIGN KEY(subject_group_id,service_id) REFERENCES user_group(id,service_id);
ALTER TABLE assignment_rule DROP CONSTRAINT ck_assignment_rule_subject_no_member_of_group,
 DROP CONSTRAINT ck_assignment_rule_subject_no_predicate,
 DROP CONSTRAINT ck_assignment_rule_member_of_group_no_predicate;
-- An empty predicate is the explicit predicate mode matching everyone.
ALTER TABLE assignment_rule ADD CONSTRAINT ck_assignment_rule_subject_target CHECK (
 (subject IS NOT NULL)::integer+(subject_group_id IS NOT NULL)::integer+(member_of_group_id IS NOT NULL)::integer+
 (predicate<>'[]'::jsonb OR (subject IS NULL AND subject_group_id IS NULL AND member_of_group_id IS NULL))::integer=1
), ADD CONSTRAINT ck_assignment_rule_subject_reserved CHECK(subject IS NULL OR subject NOT LIKE 'group:%');
CREATE INDEX assignment_rule_subject_group_idx ON assignment_rule(subject_group_id) WHERE subject_group_id IS NOT NULL;

-- +goose Down
ALTER TABLE assignment_rule DROP CONSTRAINT ck_assignment_rule_subject_reserved, DROP CONSTRAINT ck_assignment_rule_subject_target;
UPDATE assignment_rule SET subject='group:'||subject_group_id::text WHERE subject_group_id IS NOT NULL;
ALTER TABLE assignment_rule DROP COLUMN subject_group_id;
ALTER TABLE assignment_rule ADD CONSTRAINT ck_assignment_rule_subject_no_member_of_group CHECK(NOT(subject IS NOT NULL AND member_of_group_id IS NOT NULL)),
 ADD CONSTRAINT ck_assignment_rule_subject_no_predicate CHECK(NOT(subject IS NOT NULL AND predicate<>'[]'::jsonb)),
 ADD CONSTRAINT ck_assignment_rule_member_of_group_no_predicate CHECK(member_of_group_id IS NULL OR predicate='[]'::jsonb);
