-- The state of the synchronization of the resource sources moves out of Redis into PostgreSQL, and a synchronization
-- becomes a job that one worker executes under a lease (see core/engine/sync_jobs.go).
--
--   sync_state: what was last applied for a type of resources, and when it is due again (the throttle and the backoff
--               of a source that does not answer);
--   sync_job:   a request to synchronize a type, executed by exactly one worker at a time (lease + fencing token).

-- +goose Up
CREATE TABLE sync_state (
    type_resource_id uuid NOT NULL,
    -- format is the version of the digest (data_hash, graph_sig) the row was written with; 0 = nothing applied yet.
    format integer DEFAULT 0 NOT NULL,
    data_hash text DEFAULT '' NOT NULL,
    graph_sig text DEFAULT '' NOT NULL,
    -- applied_at is when the data of the type was last brought up to the state of its source (NULL = never).
    applied_at timestamp with time zone,
    -- fetched_at is when the source was last asked (it answered).
    fetched_at timestamp with time zone,
    -- next_due_at is the throttle: the type is not synchronized again before it. After a failure it is the end of the backoff.
    next_due_at timestamp with time zone DEFAULT now() NOT NULL,
    -- invalidated_at is set by a write that changes what the source of the type returns (the entities of the platform
    -- types): a synchronization that fetched before it does not postpone next_due_at.
    invalidated_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    CONSTRAINT sync_state_pkey PRIMARY KEY (type_resource_id),
    CONSTRAINT sync_state_type_resource_id_fkey FOREIGN KEY (type_resource_id) REFERENCES type_resource(id) ON DELETE CASCADE
);

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

-- +goose Down
DROP TABLE IF EXISTS sync_job CASCADE;
DROP SEQUENCE IF EXISTS sync_job_token_seq;
DROP TABLE IF EXISTS sync_state CASCADE;
