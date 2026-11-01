-- +goose Up
ALTER TABLE type_resource ADD COLUMN exported boolean NOT NULL DEFAULT false;
ALTER TABLE type_resource ADD COLUMN buckets smallint NOT NULL DEFAULT 16;
ALTER TABLE type_resource ADD CONSTRAINT ck_type_resource_buckets CHECK(buckets>0 AND (buckets & (buckets-1))=0);
ALTER TABLE type_resource_parent ADD COLUMN identity_domain_id uuid NOT NULL DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d';
ALTER TABLE type_resource_parent ADD COLUMN live boolean NOT NULL DEFAULT true;
ALTER TABLE type_resource_parent ADD COLUMN eff_inherit varchar[];
ALTER TABLE type_resource_parent ADD COLUMN eff_live boolean NOT NULL DEFAULT true;
UPDATE type_resource_parent p SET identity_domain_id=t.identity_domain_id,eff_inherit=p.inherit FROM type_resource t WHERE t.id=p.type_resource_id;
ALTER TABLE type_resource_parent ADD CONSTRAINT fk_type_parent_child_domain FOREIGN KEY(type_resource_id,identity_domain_id) REFERENCES type_resource(id,identity_domain_id) ON DELETE CASCADE;
ALTER TABLE type_resource_parent ADD CONSTRAINT fk_type_parent_parent_domain FOREIGN KEY(parent_id,identity_domain_id) REFERENCES type_resource(id,identity_domain_id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE type_resource_parent DROP CONSTRAINT fk_type_parent_parent_domain;
ALTER TABLE type_resource_parent DROP CONSTRAINT fk_type_parent_child_domain;
ALTER TABLE type_resource_parent DROP COLUMN eff_live;
ALTER TABLE type_resource_parent DROP COLUMN eff_inherit;
ALTER TABLE type_resource_parent DROP COLUMN live;
ALTER TABLE type_resource_parent DROP COLUMN identity_domain_id;
ALTER TABLE type_resource DROP CONSTRAINT ck_type_resource_buckets;
ALTER TABLE type_resource DROP COLUMN buckets;
ALTER TABLE type_resource DROP COLUMN exported;
