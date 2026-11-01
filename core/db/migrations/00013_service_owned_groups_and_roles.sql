-- +goose Up
ALTER TABLE user_group ADD COLUMN service_id uuid REFERENCES service(id);
ALTER TABLE role ADD COLUMN service_id uuid REFERENCES service(id);
UPDATE user_group SET service_id=(SELECT id FROM service WHERE slug='licetum');
UPDATE role SET service_id=(SELECT id FROM service WHERE slug='licetum');
ALTER TABLE user_group ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE role ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE user_group DROP CONSTRAINT uq_user_group_name;
ALTER TABLE role DROP CONSTRAINT uq_role_name;
ALTER TABLE user_group ADD CONSTRAINT uq_user_group_name UNIQUE(service_id,name), ADD UNIQUE(id,service_id);
ALTER TABLE role ADD CONSTRAINT uq_role_name UNIQUE(service_id,name), ADD UNIQUE(id,service_id);
ALTER TABLE type_resource ADD UNIQUE(id,service_id);
ALTER TABLE group_member ADD COLUMN service_id uuid;
UPDATE group_member m SET service_id=g.service_id FROM user_group g WHERE g.id=m.group_id;
ALTER TABLE group_member ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE group_member DROP CONSTRAINT group_member_group_id_fkey, DROP CONSTRAINT group_member_member_group_id_fkey;
ALTER TABLE group_member ADD FOREIGN KEY(group_id,service_id) REFERENCES user_group(id,service_id) ON DELETE CASCADE,
 ADD FOREIGN KEY(member_group_id,service_id) REFERENCES user_group(id,service_id) ON DELETE CASCADE;
ALTER TABLE role_permission ADD COLUMN service_id uuid;
UPDATE role_permission p SET service_id=r.service_id FROM role r WHERE r.id=p.role_id;
ALTER TABLE role_permission ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE role_permission DROP CONSTRAINT role_permission_role_id_fkey, DROP CONSTRAINT role_permission_type_resource_id_fkey;
ALTER TABLE role_permission ADD FOREIGN KEY(role_id,service_id) REFERENCES role(id,service_id) ON DELETE CASCADE,
 ADD FOREIGN KEY(type_resource_id,service_id) REFERENCES type_resource(id,service_id) ON DELETE CASCADE;
ALTER TABLE access_group ADD COLUMN service_id uuid;
UPDATE access_group g SET service_id=t.service_id FROM type_resource t WHERE t.id=g.type_resource_id;
ALTER TABLE access_group ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE access_group DROP CONSTRAINT access_group_type_resource_id_fkey;
ALTER TABLE access_group ADD FOREIGN KEY(type_resource_id,service_id) REFERENCES type_resource(id,service_id);

-- +goose Down
ALTER TABLE access_group DROP COLUMN service_id;
ALTER TABLE access_group ADD FOREIGN KEY(type_resource_id) REFERENCES type_resource(id);
ALTER TABLE role_permission DROP COLUMN service_id;
ALTER TABLE role_permission ADD FOREIGN KEY(role_id) REFERENCES role(id) ON DELETE CASCADE,
 ADD FOREIGN KEY(type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;
ALTER TABLE group_member DROP COLUMN service_id;
ALTER TABLE group_member ADD FOREIGN KEY(group_id) REFERENCES user_group(id) ON DELETE CASCADE,
 ADD FOREIGN KEY(member_group_id) REFERENCES user_group(id) ON DELETE CASCADE;
ALTER TABLE user_group DROP CONSTRAINT uq_user_group_name, DROP COLUMN service_id;
ALTER TABLE role DROP CONSTRAINT uq_role_name, DROP COLUMN service_id;
ALTER TABLE user_group ADD CONSTRAINT uq_user_group_name UNIQUE(name);
ALTER TABLE role ADD CONSTRAINT uq_role_name UNIQUE(name);
ALTER TABLE type_resource DROP CONSTRAINT type_resource_id_service_id_key;
