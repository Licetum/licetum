-- +goose Up
CREATE TABLE identity_domain (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name text NOT NULL UNIQUE,
 description text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now()
);
-- Seed before registering the mandatory publication triggers.
INSERT INTO identity_domain(id,name) VALUES ('09a5f963-232f-503e-9001-2ddf0d22e93d','default');
ALTER TABLE service ADD COLUMN identity_domain_id uuid NOT NULL DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d' REFERENCES identity_domain(id);
ALTER TABLE service ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE service ADD CONSTRAINT uq_service_id_identity_domain UNIQUE(id,identity_domain_id);
ALTER TABLE type_resource ADD COLUMN identity_domain_id uuid NOT NULL DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d';
UPDATE type_resource t SET identity_domain_id=s.identity_domain_id FROM service s WHERE s.id=t.service_id AND t.identity_domain_id IS DISTINCT FROM s.identity_domain_id;
ALTER TABLE type_resource ADD CONSTRAINT uq_type_resource_id_identity_domain UNIQUE(id,identity_domain_id);
ALTER TABLE type_resource ADD CONSTRAINT fk_type_resource_service_identity_domain FOREIGN KEY(service_id,identity_domain_id) REFERENCES service(id,identity_domain_id);
SELECT licetum_cover_table('identity_domain'::regclass);

-- +goose Down
ALTER TABLE type_resource DROP CONSTRAINT fk_type_resource_service_identity_domain;
ALTER TABLE type_resource DROP CONSTRAINT uq_type_resource_id_identity_domain;
ALTER TABLE type_resource DROP COLUMN identity_domain_id;
ALTER TABLE service DROP CONSTRAINT uq_service_id_identity_domain;
ALTER TABLE service DROP COLUMN identity_domain_id;
ALTER TABLE service DROP COLUMN created_at;
DROP TABLE identity_domain;
