-- +goose Up
ALTER TABLE assignment_rule ADD COLUMN service_id uuid;
UPDATE assignment_rule a SET service_id=COALESCE(
 (SELECT service_id FROM type_resource WHERE id=a.type_resource_id),
 (SELECT service_id FROM role WHERE id=a.role_id));
ALTER TABLE assignment_rule ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE assignment_rule DROP CONSTRAINT assignment_rule_type_resource_id_fkey,
 DROP CONSTRAINT assignment_rule_role_id_fkey,
 DROP CONSTRAINT assignment_rule_member_of_group_id_fkey,
 DROP CONSTRAINT assignment_rule_requires_group_id_fkey;
ALTER TABLE assignment_rule ADD FOREIGN KEY(service_id) REFERENCES service(id),
 ADD FOREIGN KEY(type_resource_id,service_id) REFERENCES type_resource(id,service_id) ON DELETE CASCADE,
 ADD FOREIGN KEY(role_id,service_id) REFERENCES role(id,service_id),
 ADD FOREIGN KEY(member_of_group_id,service_id) REFERENCES user_group(id,service_id) ON DELETE CASCADE,
 ADD FOREIGN KEY(requires_group_id,service_id) REFERENCES user_group(id,service_id);

-- +goose Down
ALTER TABLE assignment_rule DROP COLUMN service_id;
ALTER TABLE assignment_rule ADD FOREIGN KEY(type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE,
 ADD FOREIGN KEY(role_id) REFERENCES role(id),
 ADD FOREIGN KEY(member_of_group_id) REFERENCES user_group(id) ON DELETE CASCADE,
 ADD FOREIGN KEY(requires_group_id) REFERENCES user_group(id);
