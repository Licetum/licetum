-- +goose Up
ALTER TABLE assignment_rule RENAME COLUMN resource_from_claim TO resource_from_property;
ALTER TABLE role_permission RENAME COLUMN resource_from_claim TO resource_from_property;
ALTER TABLE assignment_rule RENAME CONSTRAINT ck_assignment_rule_resource_id_xor_from_claim TO ck_assignment_rule_resource_id_xor_from_property;
ALTER TABLE role_permission RENAME CONSTRAINT ck_role_permission_resource_id_xor_from_claim TO ck_role_permission_resource_id_xor_from_property;
UPDATE assignment_rule SET resource_from_property=(resource_from_property-'claim') || jsonb_build_object('property',resource_from_property->'claim') WHERE resource_from_property ? 'claim';
UPDATE role_permission SET resource_from_property=(resource_from_property-'claim') || jsonb_build_object('property',resource_from_property->'claim') WHERE resource_from_property ? 'claim';

-- +goose Down
UPDATE assignment_rule SET resource_from_property=(resource_from_property-'property') || jsonb_build_object('claim',resource_from_property->'property') WHERE resource_from_property ? 'property';
UPDATE role_permission SET resource_from_property=(resource_from_property-'property') || jsonb_build_object('claim',resource_from_property->'property') WHERE resource_from_property ? 'property';
ALTER TABLE assignment_rule RENAME CONSTRAINT ck_assignment_rule_resource_id_xor_from_property TO ck_assignment_rule_resource_id_xor_from_claim;
ALTER TABLE role_permission RENAME CONSTRAINT ck_role_permission_resource_id_xor_from_property TO ck_role_permission_resource_id_xor_from_claim;
ALTER TABLE assignment_rule RENAME COLUMN resource_from_property TO resource_from_claim;
ALTER TABLE role_permission RENAME COLUMN resource_from_property TO resource_from_claim;
