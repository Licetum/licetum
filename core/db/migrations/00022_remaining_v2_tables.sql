-- +goose Up
CREATE TABLE admin_binding (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 scope_kind text NOT NULL CHECK(scope_kind IN ('platform','service','group')),
 service_id uuid REFERENCES service(id),
 group_id uuid,
 role text NOT NULL,
 subject text,
 predicate jsonb,
 enabled boolean NOT NULL DEFAULT true,
 valid_from timestamptz,
 expires_at timestamptz,
 created_by text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT ck_admin_binding_selector CHECK((subject IS NULL) <> (predicate IS NULL)),
 CONSTRAINT ck_admin_binding_subject CHECK(subject IS NULL OR subject<>''),
 CONSTRAINT ck_admin_binding_predicate CHECK(predicate IS NULL OR jsonb_typeof(predicate)='array'),
 CONSTRAINT ck_admin_binding_scope CHECK(
  (scope_kind='platform' AND service_id IS NULL AND group_id IS NULL) OR
  (scope_kind='service' AND service_id IS NOT NULL AND group_id IS NULL) OR
  (scope_kind='group' AND service_id IS NOT NULL AND group_id IS NOT NULL)),
 CONSTRAINT ck_admin_binding_role CHECK(
  (scope_kind='platform' AND role IN ('platform_admin','platform_viewer','service_creator')) OR
  (scope_kind='service' AND role IN ('service_owner','service_editor','policy_editor','member_manager','service_viewer','directory_writer')) OR
  (scope_kind='group' AND role='group_manager')),
 CONSTRAINT ck_admin_binding_validity CHECK(valid_from IS NULL OR expires_at IS NULL OR valid_from<expires_at),
 CONSTRAINT fk_admin_binding_group_service FOREIGN KEY(group_id,service_id) REFERENCES user_group(id,service_id) ON DELETE CASCADE
);
CREATE TABLE platform_state (
 id smallint PRIMARY KEY CHECK(id=1),
 bootstrapped_at timestamptz,
 bootstrapped_by text,
 CONSTRAINT ck_platform_state_bootstrap CHECK((bootstrapped_at IS NULL)=(bootstrapped_by IS NULL))
);
INSERT INTO platform_state(id) VALUES(1);
CREATE TABLE pep_issuer_keys (
 issuer text PRIMARY KEY REFERENCES pep_issuer(issuer) ON DELETE CASCADE,
 key_set jsonb NOT NULL CHECK(jsonb_typeof(key_set)='object'),
 digest bytea NOT NULL CHECK(octet_length(digest)=32),
 fetched_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE resource_unresolved (
 descendant_id uuid NOT NULL REFERENCES resource_cache(id) ON DELETE CASCADE,
 parent_type_id uuid NOT NULL REFERENCES type_resource(id) ON DELETE CASCADE,
 inherit varchar[],
 PRIMARY KEY(descendant_id,parent_type_id)
);
CREATE INDEX ix_resource_unresolved_parent_type ON resource_unresolved(parent_type_id);
ALTER TABLE synchronizer ADD COLUMN refresh_interval interval NOT NULL DEFAULT interval '5 minutes' CHECK(refresh_interval>=interval '10 seconds');
ALTER TABLE synchronizer ADD COLUMN refresh_interval_max interval CHECK(refresh_interval_max IS NULL OR refresh_interval_max>=refresh_interval);
SELECT licetum_cover_table('admin_binding'::regclass);
SELECT licetum_cover_table('pep_issuer_keys'::regclass);

-- +goose Down
DROP TABLE resource_unresolved;
DROP TABLE pep_issuer_keys;
DROP TABLE platform_state;
DROP TABLE admin_binding;
ALTER TABLE synchronizer DROP COLUMN refresh_interval_max;
ALTER TABLE synchronizer DROP COLUMN refresh_interval;
