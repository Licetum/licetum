-- +goose Up
-- Schema v2 is installed into a new database. There is no upgrade from v0.1.x.
-- Generated from the schema-only dump of the verified P3 chain; its logical
-- schema, function bodies, ACLs and comments are retained exactly.
-- Future schema changes add numbered migrations; this applied initializer is immutable.


-- Types, tables, defaults, constraints and indexes.


CREATE TYPE synchronizer_kind AS ENUM (
    'SQL',
    'HTTP'
);

CREATE TABLE access_group (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid NOT NULL,
    name text NOT NULL,
    levels text[] NOT NULL,
    service_id uuid NOT NULL,
    CONSTRAINT access_group_levels_check CHECK ((cardinality(levels) > 0)),
    CONSTRAINT access_group_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 64)))
);

CREATE TABLE admin_binding (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    scope_kind text NOT NULL,
    service_id uuid,
    group_id uuid,
    role text NOT NULL,
    subject text,
    predicate jsonb,
    enabled boolean DEFAULT true NOT NULL,
    valid_from timestamp with time zone,
    expires_at timestamp with time zone,
    created_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT admin_binding_scope_kind_check CHECK ((scope_kind = ANY (ARRAY['platform'::text, 'service'::text, 'group'::text]))),
    CONSTRAINT ck_admin_binding_predicate CHECK (((predicate IS NULL) OR (jsonb_typeof(predicate) = 'array'::text))),
    CONSTRAINT ck_admin_binding_role CHECK ((((scope_kind = 'platform'::text) AND (role = ANY (ARRAY['platform_admin'::text, 'platform_viewer'::text, 'service_creator'::text]))) OR ((scope_kind = 'service'::text) AND (role = ANY (ARRAY['service_owner'::text, 'service_editor'::text, 'policy_editor'::text, 'member_manager'::text, 'service_viewer'::text, 'directory_writer'::text]))) OR ((scope_kind = 'group'::text) AND (role = 'group_manager'::text)))),
    CONSTRAINT ck_admin_binding_scope CHECK ((((scope_kind = 'platform'::text) AND (service_id IS NULL) AND (group_id IS NULL)) OR ((scope_kind = 'service'::text) AND (service_id IS NOT NULL) AND (group_id IS NULL)) OR ((scope_kind = 'group'::text) AND (service_id IS NOT NULL) AND (group_id IS NOT NULL)))),
    CONSTRAINT ck_admin_binding_selector CHECK (((subject IS NULL) <> (predicate IS NULL))),
    CONSTRAINT ck_admin_binding_subject CHECK (((subject IS NULL) OR (subject <> ''::text))),
    CONSTRAINT ck_admin_binding_validity CHECK (((valid_from IS NULL) OR (expires_at IS NULL) OR (valid_from < expires_at)))
);

CREATE TABLE assignment_rule (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid,
    resource_id character varying,
    resource_from_property jsonb,
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
    service_id uuid NOT NULL,
    subject_group_id uuid,
    subject_type text,
    CONSTRAINT ck_assignment_rule_apply_needs_deny CHECK (((on_missing_attribute = 'skip'::text) OR ((effect)::text = 'deny'::text))),
    CONSTRAINT ck_assignment_rule_description_length CHECK ((char_length(description) <= 2000)),
    CONSTRAINT ck_assignment_rule_effect CHECK (((effect)::text = ANY (ARRAY[('allow'::character varying)::text, ('deny'::character varying)::text]))),
    CONSTRAINT ck_assignment_rule_exception_is_allow CHECK (((NOT exception) OR ((effect)::text = 'allow'::text))),
    CONSTRAINT ck_assignment_rule_name_length CHECK ((char_length(name) <= 200)),
    CONSTRAINT ck_assignment_rule_on_missing CHECK ((on_missing_attribute = ANY (ARRAY['skip'::text, 'apply'::text]))),
    CONSTRAINT ck_assignment_rule_resource_id_xor_from_property CHECK (((resource_id IS NULL) OR (resource_from_property IS NULL))),
    CONSTRAINT ck_assignment_rule_resource_where_exclusive CHECK (((resource_where IS NULL) OR ((resource_id IS NULL) AND (resource_from_property IS NULL)))),
    CONSTRAINT ck_assignment_rule_role_xor_inline CHECK ((((role_id IS NOT NULL) AND (type_resource_id IS NULL) AND (resource_id IS NULL) AND (resource_from_property IS NULL) AND (access IS NULL)) OR ((role_id IS NULL) AND (type_resource_id IS NOT NULL) AND (access IS NOT NULL)))),
    CONSTRAINT ck_assignment_rule_subject_reserved CHECK (((subject IS NULL) OR ((subject)::text !~~ 'group:%'::text))),
    CONSTRAINT ck_assignment_rule_subject_target CHECK (((((((subject IS NOT NULL))::integer + ((subject_group_id IS NOT NULL))::integer) + ((member_of_group_id IS NOT NULL))::integer) + (((predicate <> '[]'::jsonb) OR ((subject IS NULL) AND (subject_group_id IS NULL) AND (member_of_group_id IS NULL))))::integer) = 1)),
    CONSTRAINT ck_assignment_rule_subject_type CHECK (((subject_type IS NULL) OR ((subject IS NOT NULL) AND ((char_length(subject_type) >= 1) AND (char_length(subject_type) <= 64)) AND (subject_type <> 'group'::text)))),
    CONSTRAINT ck_assignment_rule_validity CHECK (((valid_from IS NULL) OR (expires_at IS NULL) OR (expires_at > valid_from))),
    CONSTRAINT ck_assignment_rule_wildcard_is_deny CHECK ((((effect)::text = 'deny'::text) OR (NOT ('*'::text = ANY ((access)::text[])))))
);

CREATE TABLE change_log (
    revision bigint NOT NULL,
    seq integer NOT NULL,
    at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    actor text DEFAULT ''::text NOT NULL,
    service_id uuid,
    entity text NOT NULL,
    entity_id uuid,
    action text NOT NULL,
    before jsonb,
    after jsonb,
    CONSTRAINT change_log_action_check CHECK ((action = ANY (ARRAY['create'::text, 'update'::text, 'delete'::text]))),
    CONSTRAINT change_log_seq_check CHECK ((seq > 0))
);

CREATE TABLE group_member (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    group_id uuid NOT NULL,
    member_group_id uuid,
    predicate jsonb NOT NULL,
    enabled boolean NOT NULL,
    valid_from timestamp with time zone,
    expires_at timestamp with time zone,
    service_id uuid NOT NULL,
    CONSTRAINT ck_group_member_nested_no_predicate CHECK (((member_group_id IS NULL) OR (predicate = '[]'::jsonb))),
    CONSTRAINT ck_group_member_no_self_nest CHECK ((group_id <> member_group_id)),
    CONSTRAINT ck_group_member_validity CHECK (((valid_from IS NULL) OR (expires_at IS NULL) OR (expires_at > valid_from)))
);

CREATE TABLE identity_domain (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE log_settings (
    id smallint NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    settings jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by text DEFAULT ''::text NOT NULL,
    CONSTRAINT log_settings_id_check CHECK ((id = 1))
);

CREATE TABLE outbox (
    revision bigint NOT NULL,
    epoch integer NOT NULL,
    xid xid8 DEFAULT pg_current_xact_id() NOT NULL,
    kind text NOT NULL,
    committed_at timestamp with time zone NOT NULL,
    services uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    streams text[] DEFAULT '{}'::text[] NOT NULL,
    policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    payload uuid,
    chunk_count integer DEFAULT 0 NOT NULL,
    payload_sha256 bytea,
    published_at timestamp with time zone,
    CONSTRAINT outbox_chunk_count_check CHECK ((chunk_count >= 0)),
    CONSTRAINT outbox_epoch_check CHECK ((epoch > 0)),
    CONSTRAINT outbox_kind_check CHECK ((kind = ANY (ARRAY['change'::text, 'epoch_start'::text])))
);

CREATE TABLE outbox_chunk (
    payload uuid NOT NULL,
    stream text NOT NULL,
    n integer NOT NULL,
    body bytea NOT NULL,
    CONSTRAINT outbox_chunk_body_check CHECK ((octet_length(body) <= 1048576)),
    CONSTRAINT outbox_chunk_n_check CHECK ((n >= 0))
);

CREATE TABLE pep_client (
    id uuid NOT NULL,
    service_id uuid NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    issuer text,
    client_id text,
    operations text[] DEFAULT '{evaluate}'::text[] NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT ck_pep_client_api_key CHECK (((kind <> 'api_key'::text) OR ((issuer IS NULL) AND (client_id IS NULL)))),
    CONSTRAINT ck_pep_client_jwt CHECK (((kind <> 'jwt'::text) OR ((issuer IS NOT NULL) AND (client_id IS NOT NULL)))),
    CONSTRAINT pep_client_kind_check CHECK ((kind = ANY (ARRAY['jwt'::text, 'api_key'::text]))),
    CONSTRAINT pep_client_operations_check CHECK ((operations <@ ARRAY['evaluate'::text, 'explain'::text, 'search_resource'::text, 'search_action'::text, 'search_subject'::text]))
);

CREATE TABLE pep_credential (
    key_id text NOT NULL,
    client_id uuid NOT NULL,
    sha256 bytea NOT NULL,
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pep_credential_sha256_check CHECK ((octet_length(sha256) = 32))
);

CREATE TABLE pep_issuer (
    issuer text NOT NULL,
    jwks_url text NOT NULL,
    audiences text[] DEFAULT '{}'::text[] NOT NULL,
    algs text[] DEFAULT '{RS256,ES256,EdDSA}'::text[] NOT NULL,
    require_typ boolean DEFAULT false NOT NULL,
    CONSTRAINT ck_pep_issuer_algs CHECK (((cardinality(algs) > 0) AND (algs <@ ARRAY['RS256'::text, 'RS384'::text, 'RS512'::text, 'PS256'::text, 'PS384'::text, 'PS512'::text, 'ES256'::text, 'ES384'::text, 'ES512'::text, 'EdDSA'::text])))
);

CREATE TABLE pep_issuer_keys (
    issuer text NOT NULL,
    key_set jsonb NOT NULL,
    digest bytea NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT pep_issuer_keys_digest_check CHECK ((octet_length(digest) = 32)),
    CONSTRAINT pep_issuer_keys_key_set_check CHECK ((jsonb_typeof(key_set) = 'object'::text))
);

CREATE TABLE platform_state (
    id smallint NOT NULL,
    bootstrapped_at timestamp with time zone,
    bootstrapped_by text,
    CONSTRAINT ck_platform_state_bootstrap CHECK (((bootstrapped_at IS NULL) = (bootstrapped_by IS NULL))),
    CONSTRAINT platform_state_id_check CHECK ((id = 1))
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

CREATE TABLE publish_state (
    id integer NOT NULL,
    epoch integer DEFAULT 1 NOT NULL,
    last_published bigint DEFAULT 0 NOT NULL,
    CONSTRAINT publish_state_id_check CHECK ((id = 1))
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

CREATE TABLE resource_unresolved (
    descendant_id uuid NOT NULL,
    parent_type_id uuid NOT NULL,
    inherit character varying[]
);

CREATE TABLE revision_state (
    scope text NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    epoch integer DEFAULT 1 NOT NULL,
    CONSTRAINT revision_global CHECK ((scope = 'global'::text)),
    CONSTRAINT revision_state_epoch_check CHECK ((epoch > 0))
);

CREATE TABLE role (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL,
    service_id uuid NOT NULL
);

CREATE TABLE role_permission (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    role_id uuid NOT NULL,
    type_resource_id uuid NOT NULL,
    resource_id character varying,
    resource_from_property jsonb,
    access character varying[] NOT NULL,
    resource_where jsonb,
    service_id uuid NOT NULL,
    CONSTRAINT ck_role_permission_resource_id_xor_from_property CHECK (((resource_id IS NULL) OR (resource_from_property IS NULL))),
    CONSTRAINT ck_role_permission_resource_where_exclusive CHECK (((resource_where IS NULL) OR ((resource_id IS NULL) AND (resource_from_property IS NULL))))
);

CREATE TABLE service (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL,
    url character varying NOT NULL,
    icon character varying NOT NULL,
    deleted_at timestamp with time zone,
    slug character varying NOT NULL,
    identity_domain_id uuid DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d'::uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE subject_directory (
    service_id uuid NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    properties jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE subject_directory_seen (
    service_id uuid NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    seen_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE subject_directory_settings (
    service_id uuid NOT NULL,
    retention_days integer,
    CONSTRAINT subject_directory_settings_retention_days_check CHECK ((retention_days >= 1))
);

CREATE TABLE sync_demand (
    type_resource_id uuid NOT NULL,
    last_asked_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE sync_executor (
    executor_id text NOT NULL,
    heartbeat_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE sync_state (
    type_resource_id uuid NOT NULL,
    format integer DEFAULT 0 NOT NULL,
    data_hash text DEFAULT ''::text NOT NULL,
    graph_sig text DEFAULT ''::text NOT NULL,
    applied_at timestamp with time zone,
    fetched_at timestamp with time zone,
    next_due_at timestamp with time zone DEFAULT now() NOT NULL,
    invalidated_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text
);

CREATE TABLE synchronizer (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    type_resource_id uuid NOT NULL,
    kind synchronizer_kind NOT NULL,
    config jsonb NOT NULL,
    deleted_at timestamp with time zone,
    regions text[] DEFAULT '{}'::text[] NOT NULL,
    refresh_interval interval DEFAULT '00:05:00'::interval NOT NULL,
    refresh_interval_max interval,
    CONSTRAINT synchronizer_check CHECK (((refresh_interval_max IS NULL) OR (refresh_interval_max >= refresh_interval))),
    CONSTRAINT synchronizer_refresh_interval_check CHECK ((refresh_interval >= '00:00:10'::interval))
);

CREATE UNLOGGED TABLE tx_dirty (
    xid xid8 NOT NULL
);

CREATE TABLE type_resource (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    service_id uuid NOT NULL,
    name character varying NOT NULL,
    deleted_at timestamp with time zone,
    access_implies jsonb DEFAULT '{}'::jsonb NOT NULL,
    identity_domain_id uuid DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d'::uuid NOT NULL,
    exported boolean DEFAULT false NOT NULL,
    buckets smallint DEFAULT 16 NOT NULL,
    synced_at timestamp with time zone,
    CONSTRAINT ck_type_resource_buckets CHECK (((buckets > 0) AND (((buckets)::integer & (buckets - 1)) = 0)))
);

CREATE TABLE type_resource_parent (
    type_resource_id uuid NOT NULL,
    parent_id uuid NOT NULL,
    inherit character varying[],
    identity_domain_id uuid DEFAULT '09a5f963-232f-503e-9001-2ddf0d22e93d'::uuid NOT NULL,
    live boolean DEFAULT true NOT NULL,
    eff_inherit character varying[],
    eff_live boolean DEFAULT true NOT NULL,
    CONSTRAINT ck_type_resource_parent_no_self CHECK ((type_resource_id <> parent_id))
);

CREATE TABLE user_group (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying NOT NULL,
    service_id uuid NOT NULL
);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_pkey PRIMARY KEY (id);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_type_resource_id_name_key UNIQUE (type_resource_id, name);

ALTER TABLE ONLY admin_binding
    ADD CONSTRAINT admin_binding_pkey PRIMARY KEY (id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_pkey PRIMARY KEY (id);

ALTER TABLE ONLY change_log
    ADD CONSTRAINT change_log_pkey PRIMARY KEY (revision, seq);

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_pkey PRIMARY KEY (id);

ALTER TABLE ONLY identity_domain
    ADD CONSTRAINT identity_domain_name_key UNIQUE (name);

ALTER TABLE ONLY identity_domain
    ADD CONSTRAINT identity_domain_pkey PRIMARY KEY (id);

ALTER TABLE ONLY log_settings
    ADD CONSTRAINT log_settings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY outbox_chunk
    ADD CONSTRAINT outbox_chunk_pkey PRIMARY KEY (payload, stream, n);

ALTER TABLE ONLY outbox
    ADD CONSTRAINT outbox_pkey PRIMARY KEY (revision);

ALTER TABLE ONLY pep_client
    ADD CONSTRAINT pep_client_pkey PRIMARY KEY (id);

ALTER TABLE ONLY pep_credential
    ADD CONSTRAINT pep_credential_pkey PRIMARY KEY (key_id);

ALTER TABLE ONLY pep_issuer_keys
    ADD CONSTRAINT pep_issuer_keys_pkey PRIMARY KEY (issuer);

ALTER TABLE ONLY pep_issuer
    ADD CONSTRAINT pep_issuer_pkey PRIMARY KEY (issuer);

ALTER TABLE ONLY platform_state
    ADD CONSTRAINT platform_state_pkey PRIMARY KEY (id);

ALTER TABLE ONLY policy_scenario
    ADD CONSTRAINT policy_scenario_pkey PRIMARY KEY (id);

ALTER TABLE ONLY policy_scenario
    ADD CONSTRAINT policy_scenario_service_id_name_key UNIQUE (service_id, name);

ALTER TABLE ONLY publish_state
    ADD CONSTRAINT publish_state_pkey PRIMARY KEY (id);

ALTER TABLE ONLY resource_ancestor
    ADD CONSTRAINT resource_ancestor_pkey PRIMARY KEY (ancestor_id, descendant_id);

ALTER TABLE ONLY resource_cache
    ADD CONSTRAINT resource_cache_pkey PRIMARY KEY (id);

ALTER TABLE ONLY resource_edge
    ADD CONSTRAINT resource_edge_pkey PRIMARY KEY (child_id, parent_type_id, parent_external_id);

ALTER TABLE ONLY resource_unresolved
    ADD CONSTRAINT resource_unresolved_pkey PRIMARY KEY (descendant_id, parent_type_id);

ALTER TABLE ONLY revision_state
    ADD CONSTRAINT revision_state_pkey PRIMARY KEY (scope);

ALTER TABLE ONLY role
    ADD CONSTRAINT role_id_service_id_key UNIQUE (id, service_id);

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_pkey PRIMARY KEY (id);

ALTER TABLE ONLY role
    ADD CONSTRAINT role_pkey PRIMARY KEY (id);

ALTER TABLE ONLY service
    ADD CONSTRAINT service_pkey PRIMARY KEY (id);

ALTER TABLE ONLY subject_directory
    ADD CONSTRAINT subject_directory_pkey PRIMARY KEY (service_id, subject_type, subject_id);

ALTER TABLE ONLY subject_directory_seen
    ADD CONSTRAINT subject_directory_seen_pkey PRIMARY KEY (service_id, subject_type, subject_id);

ALTER TABLE ONLY subject_directory_settings
    ADD CONSTRAINT subject_directory_settings_pkey PRIMARY KEY (service_id);

ALTER TABLE ONLY sync_demand
    ADD CONSTRAINT sync_demand_pkey PRIMARY KEY (type_resource_id);

ALTER TABLE ONLY sync_executor
    ADD CONSTRAINT sync_executor_pkey PRIMARY KEY (executor_id);

ALTER TABLE ONLY sync_state
    ADD CONSTRAINT sync_state_pkey PRIMARY KEY (type_resource_id);

ALTER TABLE ONLY synchronizer
    ADD CONSTRAINT synchronizer_pkey PRIMARY KEY (id);

ALTER TABLE ONLY tx_dirty
    ADD CONSTRAINT tx_dirty_pkey PRIMARY KEY (xid);

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT type_resource_id_service_id_key UNIQUE (id, service_id);

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_pkey PRIMARY KEY (type_resource_id, parent_id);

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT type_resource_pkey PRIMARY KEY (id);

ALTER TABLE ONLY resource_cache
    ADD CONSTRAINT uq_resource_cache_type_resource_external_id UNIQUE (type_resource_id, external_id);

ALTER TABLE ONLY role
    ADD CONSTRAINT uq_role_name UNIQUE (service_id, name);

ALTER TABLE ONLY service
    ADD CONSTRAINT uq_service_id_identity_domain UNIQUE (id, identity_domain_id);

ALTER TABLE ONLY service
    ADD CONSTRAINT uq_service_slug UNIQUE (slug);

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT uq_type_resource_id_identity_domain UNIQUE (id, identity_domain_id);

ALTER TABLE ONLY user_group
    ADD CONSTRAINT uq_user_group_name UNIQUE (service_id, name);

ALTER TABLE ONLY user_group
    ADD CONSTRAINT user_group_id_service_id_key UNIQUE (id, service_id);

ALTER TABLE ONLY user_group
    ADD CONSTRAINT user_group_pkey PRIMARY KEY (id);

CREATE INDEX assignment_rule_subject_group_idx ON assignment_rule USING btree (subject_group_id) WHERE (subject_group_id IS NOT NULL);

CREATE INDEX change_log_service_revision ON change_log USING btree (service_id, revision, seq);

CREATE INDEX ix_assignment_rule_member_of_group_id ON assignment_rule USING btree (member_of_group_id);

CREATE INDEX ix_assignment_rule_role_id ON assignment_rule USING btree (role_id);

CREATE INDEX ix_assignment_rule_subject ON assignment_rule USING btree (subject);

CREATE INDEX ix_assignment_rule_type_resource_id ON assignment_rule USING btree (type_resource_id);

CREATE INDEX ix_group_member_group_id ON group_member USING btree (group_id);

CREATE INDEX ix_group_member_member_group_id ON group_member USING btree (member_group_id);

CREATE INDEX ix_pep_credential_client_id ON pep_credential USING btree (client_id);

CREATE INDEX ix_resource_ancestor_descendant ON resource_ancestor USING btree (descendant_id, ancestor_id);

CREATE INDEX ix_resource_cache_meta ON resource_cache USING gin (meta jsonb_path_ops);

CREATE INDEX ix_resource_cache_type_external_c ON resource_cache USING btree (type_resource_id, external_id COLLATE "C") WHERE active;

CREATE INDEX ix_resource_cache_type_name ON resource_cache USING btree (type_resource_id, name);

CREATE INDEX ix_resource_cache_type_resource_id ON resource_cache USING btree (type_resource_id);

CREATE INDEX ix_resource_edge_parent ON resource_edge USING btree (parent_id) WHERE (parent_id IS NOT NULL);

CREATE INDEX ix_resource_edge_unresolved ON resource_edge USING btree (parent_type_id) WHERE (parent_id IS NULL);

CREATE INDEX ix_resource_unresolved_parent_type ON resource_unresolved USING btree (parent_type_id);

CREATE INDEX ix_role_permission_role_id ON role_permission USING btree (role_id);

CREATE INDEX ix_role_permission_type_resource_id ON role_permission USING btree (type_resource_id);

CREATE INDEX ix_service_deleted_at ON service USING btree (deleted_at);

CREATE INDEX ix_subject_directory_scan ON subject_directory USING btree (service_id, subject_type, subject_id COLLATE "C");

CREATE INDEX ix_subject_directory_seen_at ON subject_directory_seen USING btree (service_id, seen_at);

CREATE INDEX ix_synchronizer_deleted_at ON synchronizer USING btree (deleted_at);

CREATE INDEX ix_synchronizer_type_resource_id ON synchronizer USING btree (type_resource_id);

CREATE INDEX ix_type_resource_deleted_at ON type_resource USING btree (deleted_at);

CREATE INDEX ix_type_resource_parent_parent ON type_resource_parent USING btree (parent_id);

CREATE INDEX outbox_unpublished ON outbox USING btree (revision) WHERE (published_at IS NULL);

CREATE INDEX outbox_xid ON outbox USING btree (xid);

CREATE UNIQUE INDEX uq_pep_client_identity ON pep_client USING btree (service_id, issuer, client_id) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX uq_pep_client_name ON pep_client USING btree (service_id, name) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX uq_synchronizer_live_type ON synchronizer USING btree (type_resource_id) WHERE (deleted_at IS NULL);

CREATE UNIQUE INDEX uq_type_resource_service_name ON type_resource USING btree (service_id, name) WHERE (deleted_at IS NULL);

ALTER TABLE ONLY access_group
    ADD CONSTRAINT access_group_type_resource_id_service_id_fkey FOREIGN KEY (type_resource_id, service_id) REFERENCES type_resource(id, service_id);

ALTER TABLE ONLY admin_binding
    ADD CONSTRAINT admin_binding_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_member_of_group_id_service_id_fkey FOREIGN KEY (member_of_group_id, service_id) REFERENCES user_group(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_requires_group_id_service_id_fkey FOREIGN KEY (requires_group_id, service_id) REFERENCES user_group(id, service_id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_role_id_service_id_fkey FOREIGN KEY (role_id, service_id) REFERENCES role(id, service_id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_subject_group_id_service_id_fkey FOREIGN KEY (subject_group_id, service_id) REFERENCES user_group(id, service_id);

ALTER TABLE ONLY assignment_rule
    ADD CONSTRAINT assignment_rule_type_resource_id_service_id_fkey FOREIGN KEY (type_resource_id, service_id) REFERENCES type_resource(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY admin_binding
    ADD CONSTRAINT fk_admin_binding_group_service FOREIGN KEY (group_id, service_id) REFERENCES user_group(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT fk_type_parent_child_domain FOREIGN KEY (type_resource_id, identity_domain_id) REFERENCES type_resource(id, identity_domain_id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT fk_type_parent_parent_domain FOREIGN KEY (parent_id, identity_domain_id) REFERENCES type_resource(id, identity_domain_id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT fk_type_resource_service_identity_domain FOREIGN KEY (service_id, identity_domain_id) REFERENCES service(id, identity_domain_id);

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_group_id_service_id_fkey FOREIGN KEY (group_id, service_id) REFERENCES user_group(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY group_member
    ADD CONSTRAINT group_member_member_group_id_service_id_fkey FOREIGN KEY (member_group_id, service_id) REFERENCES user_group(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY pep_client
    ADD CONSTRAINT pep_client_issuer_fkey FOREIGN KEY (issuer) REFERENCES pep_issuer(issuer);

ALTER TABLE ONLY pep_client
    ADD CONSTRAINT pep_client_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY pep_credential
    ADD CONSTRAINT pep_credential_client_id_fkey FOREIGN KEY (client_id) REFERENCES pep_client(id) ON DELETE CASCADE;

ALTER TABLE ONLY pep_issuer_keys
    ADD CONSTRAINT pep_issuer_keys_issuer_fkey FOREIGN KEY (issuer) REFERENCES pep_issuer(issuer) ON DELETE CASCADE;

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

ALTER TABLE ONLY resource_unresolved
    ADD CONSTRAINT resource_unresolved_descendant_id_fkey FOREIGN KEY (descendant_id) REFERENCES resource_cache(id) ON DELETE CASCADE;

ALTER TABLE ONLY resource_unresolved
    ADD CONSTRAINT resource_unresolved_parent_type_id_fkey FOREIGN KEY (parent_type_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_role_id_service_id_fkey FOREIGN KEY (role_id, service_id) REFERENCES role(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY role_permission
    ADD CONSTRAINT role_permission_type_resource_id_service_id_fkey FOREIGN KEY (type_resource_id, service_id) REFERENCES type_resource(id, service_id) ON DELETE CASCADE;

ALTER TABLE ONLY role
    ADD CONSTRAINT role_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id);

ALTER TABLE ONLY service
    ADD CONSTRAINT service_identity_domain_id_fkey FOREIGN KEY (identity_domain_id) REFERENCES identity_domain(id);

ALTER TABLE ONLY subject_directory_seen
    ADD CONSTRAINT subject_directory_seen_service_id_subject_type_subject_id_fkey FOREIGN KEY (service_id, subject_type, subject_id) REFERENCES subject_directory(service_id, subject_type, subject_id) ON DELETE CASCADE;

ALTER TABLE ONLY subject_directory
    ADD CONSTRAINT subject_directory_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY subject_directory_settings
    ADD CONSTRAINT subject_directory_settings_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY sync_demand
    ADD CONSTRAINT sync_demand_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY sync_state
    ADD CONSTRAINT sync_state_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY synchronizer
    ADD CONSTRAINT synchronizer_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_parent_fkey FOREIGN KEY (parent_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource_parent
    ADD CONSTRAINT type_resource_parent_type_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE;

ALTER TABLE ONLY type_resource
    ADD CONSTRAINT type_resource_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER TABLE ONLY user_group
    ADD CONSTRAINT user_group_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id);

-- Fixed initial state, before any publication trigger exists. Timestamps use
-- their defaults, so every installation records its actual creation time.
INSERT INTO identity_domain(id,name) VALUES('09a5f963-232f-503e-9001-2ddf0d22e93d','default');
INSERT INTO log_settings(id,settings) VALUES(1,
 '{"format":"json","level":"info","fields":[],"events":{"access":true,"decision":true,"sync":true},"subject":"hash","sample_allowed":0.1}'::jsonb);
INSERT INTO platform_state(id) VALUES(1);
INSERT INTO publish_state(id) VALUES(1);
INSERT INTO revision_state(scope,epoch,revision) VALUES('global',1,0);


-- Install SECURITY DEFINER functions with this schema as a fixed search path.


-- +goose StatementBegin
DO $install$
DECLARE ns text := current_schema();
BEGIN
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_mark_rows() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   IF EXISTS (SELECT 1 FROM licetum_rows) THEN
     INSERT INTO tx_dirty(xid) VALUES(pg_current_xact_id()) ON CONFLICT DO NOTHING;
   END IF;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_mark_truncate() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   INSERT INTO tx_dirty(xid) VALUES(pg_current_xact_id()) ON CONFLICT DO NOTHING;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_require_outbox() RETURNS trigger
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 BEGIN
   IF NOT EXISTS (SELECT 1 FROM outbox WHERE xid=NEW.xid) THEN
     RAISE EXCEPTION 'policy transaction %% requires an outbox row', NEW.xid
       USING ERRCODE='23514', CONSTRAINT='licetum_outbox_required';
   END IF;
   DELETE FROM tx_dirty WHERE xid=NEW.xid;
   RETURN NULL;
 END $body$ $fn$, ns);
 EXECUTE format($fn$
 CREATE FUNCTION %1$I.licetum_cover_table(target regclass) RETURNS void
 LANGUAGE plpgsql SECURITY DEFINER SET search_path TO %1$I, pg_catalog, pg_temp AS $body$
 DECLARE event text; trigger_name text; transition text;
 BEGIN
   FOREACH event IN ARRAY ARRAY['INSERT','UPDATE','DELETE'] LOOP
     trigger_name := 'licetum_outbox_' || lower(event);
     transition := CASE WHEN event='DELETE' THEN 'OLD' ELSE 'NEW' END;
     EXECUTE format('CREATE TRIGGER %%I AFTER %%s ON %%s REFERENCING %%s TABLE AS licetum_rows FOR EACH STATEMENT EXECUTE FUNCTION %1$I.licetum_mark_rows()',trigger_name,event,target,transition);
     EXECUTE format('ALTER TABLE %%s ENABLE ALWAYS TRIGGER %%I',target,trigger_name);
   END LOOP;
   EXECUTE format('CREATE TRIGGER licetum_outbox_truncate AFTER TRUNCATE ON %%s FOR EACH STATEMENT EXECUTE FUNCTION %1$I.licetum_mark_truncate()',target);
   EXECUTE format('ALTER TABLE %%s ENABLE ALWAYS TRIGGER licetum_outbox_truncate',target);
 END $body$ $fn$, ns);
END $install$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION licetum_immutable_service_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.slug IS DISTINCT FROM OLD.slug THEN
  RAISE EXCEPTION 'service slug cannot be changed' USING ERRCODE='23514',CONSTRAINT='ck_service_slug_immutable';
 END IF;
 IF NEW.identity_domain_id IS DISTINCT FROM OLD.identity_domain_id THEN
  RAISE EXCEPTION 'service identity_domain_id cannot be changed in P3' USING ERRCODE='23514',CONSTRAINT='ck_service_domain_immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Covered tables install statement transition-table triggers, all ALWAYS.


SELECT licetum_cover_table('access_group'::regclass);

SELECT licetum_cover_table('admin_binding'::regclass);

SELECT licetum_cover_table('assignment_rule'::regclass);

SELECT licetum_cover_table('group_member'::regclass);

SELECT licetum_cover_table('identity_domain'::regclass);

SELECT licetum_cover_table('log_settings'::regclass);

SELECT licetum_cover_table('pep_client'::regclass);

SELECT licetum_cover_table('pep_credential'::regclass);

SELECT licetum_cover_table('pep_issuer'::regclass);

SELECT licetum_cover_table('pep_issuer_keys'::regclass);

SELECT licetum_cover_table('role'::regclass);

SELECT licetum_cover_table('role_permission'::regclass);

SELECT licetum_cover_table('service'::regclass);

SELECT licetum_cover_table('subject_directory'::regclass);

SELECT licetum_cover_table('synchronizer'::regclass);

SELECT licetum_cover_table('type_resource'::regclass);

SELECT licetum_cover_table('type_resource_parent'::regclass);

SELECT licetum_cover_table('user_group'::regclass);

CREATE CONSTRAINT TRIGGER licetum_outbox_required AFTER INSERT ON tx_dirty DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION licetum_require_outbox();

ALTER TABLE tx_dirty ENABLE ALWAYS TRIGGER licetum_outbox_required;

CREATE TRIGGER service_identity_immutable BEFORE UPDATE ON service FOR EACH ROW EXECUTE FUNCTION licetum_immutable_service_identity();

ALTER TABLE service ENABLE ALWAYS TRIGGER service_identity_immutable;

-- Application schema documentation.


COMMENT ON TABLE access_group IS 'Named access-level sets owned by a resource type.';

COMMENT ON TABLE admin_binding IS 'Reserved scoped administrator bindings.';

COMMENT ON TABLE assignment_rule IS 'Service-owned allow and deny rules with explicit subject selectors.';

COMMENT ON TABLE change_log IS 'Audited policy history ordered by revision and sequence.';

COMMENT ON TABLE group_member IS 'Service-owned predicate and nested-group membership entries.';

COMMENT ON TABLE identity_domain IS 'Identity namespaces shared by services and type links.';

COMMENT ON TABLE log_settings IS 'Process logging settings; subject salt is held outside PostgreSQL.';

COMMENT ON TABLE outbox IS 'Atomic publication headers ordered by global revision within an epoch.';

COMMENT ON TABLE outbox_chunk IS 'Compressed record-aligned payload chunks per publication stream.';

COMMENT ON TABLE pep_client IS 'Service-owned PEP caller identities and permitted operations.';

COMMENT ON TABLE pep_credential IS 'Hashed PEP API keys and their expiry and revocation state.';

COMMENT ON TABLE pep_issuer IS 'Trusted asymmetric JWT issuers for PEP clients.';

COMMENT ON TABLE pep_issuer_keys IS 'Cached public signing keys per issuer.';

COMMENT ON TABLE platform_state IS 'Singleton platform bootstrap state reserved for binding administration.';

COMMENT ON TABLE policy_scenario IS 'Stored policy test scenarios, excluded from policy publications.';

COMMENT ON TABLE publish_state IS 'Publisher delivery progress.';

COMMENT ON TABLE resource_ancestor IS 'Derived resource reachability closure.';

COMMENT ON TABLE resource_cache IS 'Local resource rows applied by synchronization.';

COMMENT ON TABLE resource_edge IS 'Resolved direct resource parent edges.';

COMMENT ON TABLE resource_unresolved IS 'Reserved unresolved parent links for component rebuilds.';

COMMENT ON TABLE revision_state IS 'Singleton global publication epoch and revision.';

COMMENT ON TABLE role IS 'Service-owned named roles.';

COMMENT ON TABLE role_permission IS 'Service-owned permission items of roles.';

COMMENT ON TABLE service IS 'Owning applications with immutable slugs and identity domains.';

COMMENT ON TABLE subject_directory IS 'Published subject-directory content per service.';

COMMENT ON TABLE subject_directory_seen IS 'Directory liveness timestamps separate from published content.';

COMMENT ON TABLE subject_directory_settings IS 'Per-service subject-directory retention settings.';

COMMENT ON TABLE sync_demand IS 'Per-type demand used by the synchronization scheduler.';

COMMENT ON TABLE sync_executor IS 'Synchronization executor heartbeats.';

COMMENT ON TABLE sync_state IS 'Applied source digests, scheduling, invalidation and retry state.';

COMMENT ON TABLE synchronizer IS 'Resource sources; private configuration is excluded from the outbox.';

COMMENT ON TABLE tx_dirty IS 'Transaction marks checked by deferred mandatory-outbox enforcement.';

COMMENT ON TABLE type_resource IS 'Service-owned resource types with first-apply synchronization state.';

COMMENT ON TABLE type_resource_parent IS 'Desired and effective parent links; moved together in P3.';

COMMENT ON TABLE user_group IS 'Service-owned groups of subjects.';

-- Trigger installation and mark functions are not callable by PUBLIC.


REVOKE ALL ON FUNCTION licetum_cover_table(target regclass) FROM PUBLIC;

REVOKE ALL ON FUNCTION licetum_mark_rows() FROM PUBLIC;

REVOKE ALL ON FUNCTION licetum_mark_truncate() FROM PUBLIC;

REVOKE ALL ON FUNCTION licetum_require_outbox() FROM PUBLIC;

-- +goose Down
-- Remove application objects, retaining the provider-owned version table.


DROP TABLE IF EXISTS access_group CASCADE;

DROP TABLE IF EXISTS admin_binding CASCADE;

DROP TABLE IF EXISTS assignment_rule CASCADE;

DROP TABLE IF EXISTS change_log CASCADE;

DROP TABLE IF EXISTS group_member CASCADE;

DROP TABLE IF EXISTS identity_domain CASCADE;

DROP TABLE IF EXISTS log_settings CASCADE;

DROP TABLE IF EXISTS outbox CASCADE;

DROP TABLE IF EXISTS outbox_chunk CASCADE;

DROP TABLE IF EXISTS pep_client CASCADE;

DROP TABLE IF EXISTS pep_credential CASCADE;

DROP TABLE IF EXISTS pep_issuer CASCADE;

DROP TABLE IF EXISTS pep_issuer_keys CASCADE;

DROP TABLE IF EXISTS platform_state CASCADE;

DROP TABLE IF EXISTS policy_scenario CASCADE;

DROP TABLE IF EXISTS publish_state CASCADE;

DROP TABLE IF EXISTS resource_ancestor CASCADE;

DROP TABLE IF EXISTS resource_cache CASCADE;

DROP TABLE IF EXISTS resource_edge CASCADE;

DROP TABLE IF EXISTS resource_unresolved CASCADE;

DROP TABLE IF EXISTS revision_state CASCADE;

DROP TABLE IF EXISTS role CASCADE;

DROP TABLE IF EXISTS role_permission CASCADE;

DROP TABLE IF EXISTS service CASCADE;

DROP TABLE IF EXISTS subject_directory CASCADE;

DROP TABLE IF EXISTS subject_directory_seen CASCADE;

DROP TABLE IF EXISTS subject_directory_settings CASCADE;

DROP TABLE IF EXISTS sync_demand CASCADE;

DROP TABLE IF EXISTS sync_executor CASCADE;

DROP TABLE IF EXISTS sync_state CASCADE;

DROP TABLE IF EXISTS synchronizer CASCADE;

DROP TABLE IF EXISTS tx_dirty CASCADE;

DROP TABLE IF EXISTS type_resource CASCADE;

DROP TABLE IF EXISTS type_resource_parent CASCADE;

DROP TABLE IF EXISTS user_group CASCADE;

DROP FUNCTION IF EXISTS licetum_cover_table(regclass);

DROP FUNCTION IF EXISTS licetum_mark_rows();

DROP FUNCTION IF EXISTS licetum_mark_truncate();

DROP FUNCTION IF EXISTS licetum_require_outbox();

DROP FUNCTION IF EXISTS licetum_immutable_service_identity();

DROP TYPE IF EXISTS synchronizer_kind;
