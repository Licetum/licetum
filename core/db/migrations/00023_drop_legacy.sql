-- +goose Up
-- Schema-v2 cleanup is applied only to empty development databases. Released
-- v0.1 databases have no upgrade path; P3-42 installs its refusal boundary.
DROP TABLE authz_change;
DROP SEQUENCE IF EXISTS authz_change_id_seq;
DROP TABLE delegated_call;
DROP SEQUENCE IF EXISTS delegated_call_id_seq;
DROP TABLE known_subject;
DROP TABLE sync_job;
DROP SEQUENCE sync_job_token_seq;
ALTER TABLE service
 DROP COLUMN authorization_url, DROP COLUMN token_url, DROP COLUMN jwks_url,
 DROP COLUMN issuer, DROP COLUMN client_id, DROP COLUMN client_secret,
 DROP COLUMN trusted_clients;
DELETE FROM revision_state WHERE scope <> 'global';

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

-- +goose Down
COMMENT ON TABLE access_group IS NULL;
COMMENT ON TABLE admin_binding IS NULL;
COMMENT ON TABLE assignment_rule IS NULL;
COMMENT ON TABLE change_log IS NULL;
COMMENT ON TABLE group_member IS NULL;
COMMENT ON TABLE identity_domain IS NULL;
COMMENT ON TABLE log_settings IS NULL;
COMMENT ON TABLE outbox IS NULL;
COMMENT ON TABLE outbox_chunk IS NULL;
COMMENT ON TABLE pep_client IS NULL;
COMMENT ON TABLE pep_credential IS NULL;
COMMENT ON TABLE pep_issuer IS NULL;
COMMENT ON TABLE pep_issuer_keys IS NULL;
COMMENT ON TABLE platform_state IS NULL;
COMMENT ON TABLE policy_scenario IS NULL;
COMMENT ON TABLE publish_state IS NULL;
COMMENT ON TABLE resource_ancestor IS NULL;
COMMENT ON TABLE resource_cache IS NULL;
COMMENT ON TABLE resource_edge IS NULL;
COMMENT ON TABLE resource_unresolved IS NULL;
COMMENT ON TABLE revision_state IS NULL;
COMMENT ON TABLE role IS NULL;
COMMENT ON TABLE role_permission IS NULL;
COMMENT ON TABLE service IS NULL;
COMMENT ON TABLE subject_directory IS NULL;
COMMENT ON TABLE subject_directory_seen IS NULL;
COMMENT ON TABLE subject_directory_settings IS NULL;
COMMENT ON TABLE sync_demand IS NULL;
COMMENT ON TABLE sync_executor IS NULL;
COMMENT ON TABLE sync_state IS NULL;
COMMENT ON TABLE synchronizer IS NULL;
COMMENT ON TABLE tx_dirty IS NULL;
COMMENT ON TABLE type_resource IS NULL;
COMMENT ON TABLE type_resource_parent IS NULL;
COMMENT ON TABLE user_group IS NULL;
ALTER TABLE service ADD COLUMN authorization_url character varying,
 ADD COLUMN token_url character varying, ADD COLUMN jwks_url character varying,
 ADD COLUMN issuer character varying, ADD COLUMN client_id character varying,
 ADD COLUMN client_secret character varying,
 ADD COLUMN trusted_clients text[] NOT NULL DEFAULT '{}'::text[];
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

CREATE TABLE known_subject (
    subject text NOT NULL,
    display_name text,
    claims jsonb DEFAULT '{}'::jsonb NOT NULL,
    first_seen timestamp with time zone DEFAULT now() NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL,
    seen_count bigint DEFAULT 1 NOT NULL
);

ALTER TABLE ONLY authz_change ALTER COLUMN id SET DEFAULT nextval('authz_change_id_seq'::regclass);

ALTER TABLE ONLY delegated_call ALTER COLUMN id SET DEFAULT nextval('delegated_call_id_seq'::regclass);

ALTER TABLE ONLY authz_change
    ADD CONSTRAINT authz_change_pkey PRIMARY KEY (id);

ALTER TABLE ONLY delegated_call
    ADD CONSTRAINT delegated_call_pkey PRIMARY KEY (id);

ALTER TABLE ONLY known_subject
    ADD CONSTRAINT known_subject_pkey PRIMARY KEY (subject);

ALTER TABLE ONLY delegated_call
    ADD CONSTRAINT delegated_call_service_id_fkey FOREIGN KEY (service_id) REFERENCES service(id) ON DELETE CASCADE;

ALTER SEQUENCE authz_change_id_seq OWNED BY authz_change.id;

ALTER SEQUENCE delegated_call_id_seq OWNED BY delegated_call.id;

CREATE INDEX ix_authz_change_entity ON authz_change USING btree (entity, entity_id, id);

CREATE INDEX ix_authz_change_service ON authz_change USING btree (service_id, id);

CREATE INDEX ix_delegated_call_at ON delegated_call USING btree (at);

CREATE INDEX ix_delegated_call_service ON delegated_call USING btree (service_id, id DESC);

CREATE INDEX ix_known_subject_last_seen ON known_subject USING btree (last_seen);

CREATE SEQUENCE sync_job_token_seq;

CREATE TABLE sync_job (
    id bigserial NOT NULL,
    -- dedup_key: the sorted type ids hashed, plus ':fresh' for a job that asks the source regardless of the throttle.
    dedup_key text NOT NULL,
    type_ids uuid[] NOT NULL,
    fresh boolean NOT NULL,
    -- source says who asked: request (a check or admin request), admin (a refresh), schedule (the worker's schedule).
    source text DEFAULT 'request' NOT NULL,
    state text DEFAULT 'queued' NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_owner text,
    lease_token bigint,
    lease_until timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    -- unanswered lists the types whose source did not answer in this job (their data stays as it was).
    unanswered uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    error text,
    CONSTRAINT sync_job_pkey PRIMARY KEY (id),
    CONSTRAINT sync_job_state_check CHECK (state IN ('queued', 'running', 'done', 'failed'))
);

-- At most one active job per key: a second request for the same set attaches to it.
CREATE UNIQUE INDEX sync_job_active ON sync_job (dedup_key) WHERE state IN ('queued', 'running');
CREATE INDEX sync_job_claim ON sync_job (state, id) WHERE state IN ('queued', 'running');
CREATE INDEX sync_job_created ON sync_job (created_at);
