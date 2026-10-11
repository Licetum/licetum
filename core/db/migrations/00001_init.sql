-- Licetum schema: the whole database of a fresh install, in one migration.
--
-- Before the first public release every earlier migration was folded into this file; the upgrade path between
-- development snapshots is not kept. Idempotence is not required: goose runs this once per database. To change the schema
-- from now on, add 00002_*.sql and so on.
--
-- The platform self-service (the Licetum service within itself, its policy types and their sources, the admin group) is
-- not here: it depends on the environment, so `licetum migrate` seeds it (see core/db/migrate.go).

-- +goose Up
CREATE TYPE synchronizer_kind AS ENUM (
    'SQL',
    'HTTP'
);

CREATE TABLE access_group (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid NOT NULL,
    name text NOT NULL,
    levels text[] NOT NULL,
    CONSTRAINT access_group_levels_check CHECK ((cardinality(levels) > 0)),
    CONSTRAINT access_group_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 64)))
);

CREATE TABLE assignment_rule (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid,
    resource_id character varying,
    resource_from_claim jsonb,
    access character varying[],
    subject character varying,
    predicate jsonb NOT NULL,
    member_of_group_id uuid,
    enabled boolean NOT NULL,
    effect character varying DEFAULT 'allow'::character varying NOT NULL,
    exception boolean DEFAULT false NOT NULL,
    valid_from timestamp with time zone,
    expires_at timestamp with time zone,
    role_id uuid,
    name text,
    description text,
    on_missing_attribute text DEFAULT 'skip'::text NOT NULL,
    requires_predicate jsonb DEFAULT '[]'::jsonb NOT NULL,
    requires_group_id uuid,
    resource_where jsonb,
    subject_kind text,
    CONSTRAINT ck_assignment_rule_apply_needs_deny CHECK (((on_missing_attribute = 'skip'::text) OR ((effect)::text = 'deny'::text))),
    CONSTRAINT ck_assignment_rule_description_length CHECK ((char_length(description) <= 2000)),
    CONSTRAINT ck_assignment_rule_effect CHECK (((effect)::text = ANY ((ARRAY['allow'::character varying, 'deny'::character varying])::text[]))),
    CONSTRAINT ck_assignment_rule_exception_is_allow CHECK (((NOT exception) OR ((effect)::text = 'allow'::text))),
    CONSTRAINT ck_assignment_rule_member_of_group_no_predicate CHECK (((member_of_group_id IS NULL) OR (predicate = '[]'::jsonb))),
    CONSTRAINT ck_assignment_rule_name_length CHECK ((char_length(name) <= 200)),
    CONSTRAINT ck_assignment_rule_on_missing CHECK ((on_missing_attribute = ANY (ARRAY['skip'::text, 'apply'::text]))),
    CONSTRAINT ck_assignment_rule_resource_id_xor_from_claim CHECK (((resource_id IS NULL) OR (resource_from_claim IS NULL))),
    CONSTRAINT ck_assignment_rule_resource_where_exclusive CHECK (((resource_where IS NULL) OR ((resource_id IS NULL) AND (resource_from_claim IS NULL)))),
    CONSTRAINT ck_assignment_rule_role_xor_inline CHECK ((((role_id IS NOT NULL) AND (type_resource_id IS NULL) AND (resource_id IS NULL) AND (resource_from_claim IS NULL) AND (access IS NULL)) OR ((role_id IS NULL) AND (type_resource_id IS NOT NULL) AND (access IS NOT NULL)))),
    CONSTRAINT ck_assignment_rule_subject_kind CHECK ((subject_kind = ANY (ARRAY['user'::text, 'service'::text, 'agent'::text, 'group'::text]))),
    CONSTRAINT ck_assignment_rule_subject_no_member_of_group CHECK ((NOT ((subject IS NOT NULL) AND (member_of_group_id IS NOT NULL)))),
    CONSTRAINT ck_assignment_rule_subject_no_predicate CHECK ((NOT ((subject IS NOT NULL) AND (predicate <> '[]'::jsonb)))),
    CONSTRAINT ck_assignment_rule_validity CHECK (((valid_from IS NULL) OR (expires_at IS NULL) OR (expires_at > valid_from))),
    CONSTRAINT ck_assignment_rule_wildcard_is_deny CHECK ((((effect)::text = 'deny'::text) OR (NOT ('*'::text = ANY ((access)::text[])))))
);

CREATE TABLE authz_change (
    id bigint NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL,
    actor text DEFAULT ''::text NOT NULL,
    service_id uuid,
    entity text NOT NULL,
    entity_id uuid,
    action text NOT NULL,
    before jsonb,
    after jsonb,
    CONSTRAINT authz_change_action_check CHECK ((action = ANY (ARRAY['create'::text, 'update'::text, 'delete'::text])))
);

CREATE SEQUENCE authz_change_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE delegated_call (
    id bigint NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL,
    service_id uuid NOT NULL,
    caller_client text NOT NULL,
    caller_subject text,
    subject text NOT NULL,
    operation text NOT NULL,
    questions integer DEFAULT 0 NOT NULL,
    allowed integer DEFAULT 0 NOT NULL,
    request_id text,
    CONSTRAINT delegated_call_operation_check CHECK ((operation = ANY (ARRAY['check'::text, 'permission'::text])))
);

CREATE SEQUENCE delegated_call_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE group_member (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    group_id uuid NOT NULL,
    member_group_id uuid,
    predicate jsonb NOT NULL,
    enabled boolean NOT NULL,
    valid_from timestamp with time zone,
    expires_at timestamp with time zone,
    CONSTRAINT ck_group_member_nested_no_predicate CHECK (((member_group_id IS NULL) OR (predicate = '[]'::jsonb))),
    CONSTRAINT ck_group_member_no_self_nest CHECK ((group_id <> member_group_id)),
    CONSTRAINT ck_group_member_validity CHECK (((valid_from IS NULL) OR (expires_at IS NULL) OR (expires_at > valid_from)))
);

CREATE TABLE known_subject (
    subject text NOT NULL,
    display_name text,
    claims jsonb DEFAULT '{}'::jsonb NOT NULL,
    first_seen timestamp with time zone DEFAULT now() NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL,
    seen_count bigint DEFAULT 1 NOT NULL
);

CREATE TABLE log_settings (
    id smallint NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    settings jsonb NOT NULL,
    salt text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by text DEFAULT ''::text NOT NULL,
    CONSTRAINT log_settings_id_check CHECK ((id = 1))
);

CREATE TABLE policy_scenario (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    service_id uuid NOT NULL,
    name text NOT NULL,
    scenario jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT policy_scenario_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 200)))
);

CREATE TABLE resource_ancestor (
    ancestor_id uuid NOT NULL,
    descendant_id uuid NOT NULL,
    inherit character varying[],
    descendant_type_id uuid NOT NULL,
    descendant_external_id character varying NOT NULL,
    descendant_name character varying NOT NULL
);

CREATE TABLE resource_cache (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid NOT NULL,
    external_id character varying NOT NULL,
    name character varying NOT NULL,
    synced_at timestamp without time zone DEFAULT now() NOT NULL,
    active boolean DEFAULT true NOT NULL,
    meta jsonb DEFAULT '{}'::jsonb NOT NULL
);

CREATE TABLE resource_edge (
    child_id uuid NOT NULL,
    parent_type_id uuid NOT NULL,
    parent_external_id character varying NOT NULL,
    parent_id uuid
);

CREATE TABLE role (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL
);

CREATE TABLE role_permission (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    role_id uuid NOT NULL,
    type_resource_id uuid NOT NULL,
    resource_id character varying,
    resource_from_claim jsonb,
    access character varying[] NOT NULL,
    resource_where jsonb,
    CONSTRAINT ck_role_permission_resource_id_xor_from_claim CHECK (((resource_id IS NULL) OR (resource_from_claim IS NULL))),
    CONSTRAINT ck_role_permission_resource_where_exclusive CHECK (((resource_where IS NULL) OR ((resource_id IS NULL) AND (resource_from_claim IS NULL))))
);

CREATE TABLE service (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL,
    url character varying NOT NULL,
    icon character varying NOT NULL,
    deleted_at timestamp with time zone,
    authorization_url character varying,
    token_url character varying,
    jwks_url character varying,
    issuer character varying,
    client_id character varying,
    client_secret character varying,
    slug character varying NOT NULL,
    trusted_clients text[] DEFAULT '{}'::text[] NOT NULL
);

CREATE TABLE synchronizer (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid NOT NULL,
    kind synchronizer_kind NOT NULL,
    config jsonb NOT NULL,
    deleted_at timestamp with time zone
);

CREATE TABLE type_resource (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    service_id uuid NOT NULL,
    name character varying NOT NULL,
    deleted_at timestamp with time zone,
    access_implies jsonb DEFAULT '{}'::jsonb NOT NULL
);

CREATE TABLE type_resource_parent (
    type_resource_id uuid NOT NULL,
    parent_id uuid NOT NULL,
    inherit character varying[],
    CONSTRAINT ck_type_resource_parent_no_self CHECK ((type_resource_id <> parent_id))
);

CREATE TABLE user_group (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL
);

ALTER TABLE ONLY authz_change ALTER COLUMN id SET DEFAULT nextval('authz_change_id_seq'::regclass);

ALTER TABLE ONLY delegated_call ALTER COLUMN id SET DEFAULT nextval('delegated_call_id_seq'::regclass);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_pkey PRIMARY KEY (id);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_type_resource_id_name_key UNIQUE (type_resource_id, name);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_pkey PRIMARY KEY (id);

ALTER TABLE ONLY authz_change
    ADD CONSTRAINT authz_change_pkey PRIMARY KEY (id);

ALTER TABLE ONLY delegated_call
    ADD CONSTRAINT delegated_call_pkey PRIMARY KEY (id);

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_pkey PRIMARY KEY (id);

ALTER TABLE ONLY known_subject
    ADD CONSTRAINT known_subject_pkey PRIMARY KEY (subject);

ALTER TABLE ONLY log_settings
    ADD CONSTRAINT log_settings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY policy_scenario
    ADD CONSTRAINT policy_scenario_pkey PRIMARY KEY (id);

ALTER TABLE ONLY policy_scenario
    ADD CONSTRAINT policy_scenario_service_id_name_key UNIQUE (service_id, name);

ALTER TABLE ONLY resource_ancestor
    ADD CONSTRAINT resource_ancestor_pkey PRIMARY KEY (ancestor_id, descendant_id);

ALTER TABLE ONLY resource_cache
    ADD CONSTRAINT resource_cache_pkey PRIMARY KEY (id);

ALTER TABLE ONLY resource_edge
    ADD CONSTRAINT resource_edge_pkey PRIMARY KEY (child_id, parent_type_id, parent_external_id);

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_pkey PRIMARY KEY (id);

ALTER TABLE ONLY role
    ADD CONSTRAINT role_pkey PRIMARY KEY (id);

ALTER TABLE ONLY service
    ADD CONSTRAINT service_pkey PRIMARY KEY (id);

ALTER TABLE ONLY synchronizer
    ADD CONSTRAINT synchronizer_pkey PRIMARY KEY (id);

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_pkey PRIMARY KEY (type_resource_id, parent_id);

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT type_resource_pkey PRIMARY KEY (id);

ALTER TABLE ONLY resource_cache
    ADD CONSTRAINT uq_resource_cache_type_resource_external_id UNIQUE (type_resource_id, external_id);

ALTER TABLE ONLY role
    ADD CONSTRAINT uq_role_name UNIQUE (name);

ALTER TABLE ONLY user_group
    ADD CONSTRAINT uq_user_group_name UNIQUE (name);

ALTER TABLE ONLY user_group
    ADD CONSTRAINT user_group_pkey PRIMARY KEY (id);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_member_of_group_id_fkey FOREIGN KEY (member_of_group_id) REFERENCES user_group(id) ON DELETE CASCADE;

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_requires_group_id_fkey FOREIGN KEY (requires_group_id) REFERENCES user_group(id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_role_id_fkey FOREIGN KEY (role_id) REFERENCES role(id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY delegated_call
    ADD CONSTRAINT delegated_call_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_group_id_fkey FOREIGN KEY (group_id) REFERENCES user_group(id) ON DELETE CASCADE;

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_member_group_id_fkey FOREIGN KEY (member_group_id) REFERENCES user_group(id) ON DELETE CASCADE;

ALTER TABLE ONLY policy_scenario
    ADD CONSTRAINT policy_scenario_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_ancestor
    ADD CONSTRAINT resource_ancestor_ancestor_fkey FOREIGN KEY (ancestor_id) REFERENCES resource_cache(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_ancestor
    ADD CONSTRAINT resource_ancestor_descendant_fkey FOREIGN KEY (descendant_id) REFERENCES resource_cache(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_cache
    ADD CONSTRAINT resource_cache_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_edge
    ADD CONSTRAINT resource_edge_child_fkey FOREIGN KEY (child_id) REFERENCES resource_cache(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_edge
    ADD CONSTRAINT resource_edge_parent_fkey FOREIGN KEY (parent_id) REFERENCES resource_cache(id) ON DELETE SET NULL;

ALTER TABLE ONLY resource_edge
    ADD CONSTRAINT resource_edge_parent_type_fkey FOREIGN KEY (parent_type_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_role_id_fkey FOREIGN KEY (role_id) REFERENCES role(id) ON DELETE CASCADE;

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY synchronizer
    ADD CONSTRAINT synchronizer_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_parent_fkey FOREIGN KEY (parent_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_type_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT type_resource_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER SEQUENCE authz_change_id_seq OWNED BY authz_change.id;

ALTER SEQUENCE delegated_call_id_seq OWNED BY delegated_call.id;

CREATE INDEX ix_assignment_rule_member_of_group_id ON assignment_rule USING btree (member_of_group_id);

CREATE INDEX ix_assignment_rule_role_id ON assignment_rule USING btree (role_id);

CREATE INDEX ix_assignment_rule_subject ON assignment_rule USING btree (subject);

CREATE INDEX ix_assignment_rule_type_resource_id ON assignment_rule USING btree (type_resource_id);

CREATE INDEX ix_authz_change_entity ON authz_change USING btree (entity, entity_id, id);

CREATE INDEX ix_authz_change_service ON authz_change USING btree (service_id, id);

CREATE INDEX ix_delegated_call_at ON delegated_call USING btree (at);

CREATE INDEX ix_delegated_call_service ON delegated_call USING btree (service_id, id DESC);

CREATE INDEX ix_group_member_group_id ON group_member USING btree (group_id);

CREATE INDEX ix_group_member_member_group_id ON group_member USING btree (member_group_id);

CREATE INDEX ix_known_subject_last_seen ON known_subject USING btree (last_seen);

CREATE INDEX ix_resource_ancestor_descendant ON resource_ancestor USING btree (descendant_id, ancestor_id);

CREATE INDEX ix_resource_cache_meta ON resource_cache USING gin (meta jsonb_path_ops);

CREATE INDEX ix_resource_cache_type_name ON resource_cache USING btree (type_resource_id, name);

CREATE INDEX ix_resource_cache_type_resource_id ON resource_cache USING btree (type_resource_id);

CREATE INDEX ix_resource_edge_parent ON resource_edge USING btree (parent_id) WHERE (parent_id IS NOT NULL);

CREATE INDEX ix_resource_edge_unresolved ON resource_edge USING btree (parent_type_id) WHERE (parent_id IS NULL);

CREATE INDEX ix_role_permission_role_id ON role_permission USING btree (role_id);

CREATE INDEX ix_role_permission_type_resource_id ON role_permission USING btree (type_resource_id);

CREATE INDEX ix_service_deleted_at ON service USING btree (deleted_at);

CREATE INDEX ix_synchronizer_deleted_at ON synchronizer USING btree (deleted_at);

CREATE INDEX ix_synchronizer_type_resource_id ON synchronizer USING btree (type_resource_id);

CREATE INDEX ix_type_resource_deleted_at ON type_resource USING btree (deleted_at);

CREATE INDEX ix_type_resource_parent_parent ON type_resource_parent USING btree (parent_id);

CREATE UNIQUE INDEX uq_service_slug ON service USING btree (slug) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX uq_synchronizer_live_type ON synchronizer USING btree (type_resource_id) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX uq_type_resource_service_name ON type_resource USING btree (service_id, name) WHERE (deleted_at IS NULL);

INSERT INTO log_settings (id, settings, salt)
VALUES (
    1,
    '{"format": "json", "level": "info", "fields": [],
      "events": {"access": true, "decision": true, "sync": true},
      "subject": "hash", "sample_allowed": 0.1}'::jsonb,
    replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', '')
)
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS access_group CASCADE;
DROP TABLE IF EXISTS assignment_rule CASCADE;
DROP TABLE IF EXISTS authz_change CASCADE;
DROP TABLE IF EXISTS delegated_call CASCADE;
DROP TABLE IF EXISTS group_member CASCADE;
DROP TABLE IF EXISTS known_subject CASCADE;
DROP TABLE IF EXISTS log_settings CASCADE;
DROP TABLE IF EXISTS policy_scenario CASCADE;
DROP TABLE IF EXISTS resource_ancestor CASCADE;
DROP TABLE IF EXISTS resource_cache CASCADE;
DROP TABLE IF EXISTS resource_edge CASCADE;
DROP TABLE IF EXISTS role CASCADE;
DROP TABLE IF EXISTS role_permission CASCADE;
DROP TABLE IF EXISTS service CASCADE;
DROP TABLE IF EXISTS synchronizer CASCADE;
DROP TABLE IF EXISTS type_resource CASCADE;
DROP TABLE IF EXISTS type_resource_parent CASCADE;
DROP TABLE IF EXISTS user_group CASCADE;
DROP TYPE IF EXISTS synchronizer_kind;
