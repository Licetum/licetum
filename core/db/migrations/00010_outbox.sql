-- +goose Up
CREATE TABLE outbox (
    revision bigint PRIMARY KEY,
    epoch integer NOT NULL CHECK (epoch > 0),
    xid xid8 NOT NULL DEFAULT pg_current_xact_id(),
    kind text NOT NULL CHECK (kind IN ('change', 'epoch_start')),
    committed_at timestamptz NOT NULL,
    services uuid[] NOT NULL DEFAULT '{}',
    streams text[] NOT NULL DEFAULT '{}',
    policy jsonb NOT NULL DEFAULT '{}',
    payload uuid,
    chunk_count integer NOT NULL DEFAULT 0 CHECK (chunk_count >= 0),
    payload_sha256 bytea,
    published_at timestamptz
);
CREATE INDEX outbox_xid ON outbox(xid);
CREATE INDEX outbox_unpublished ON outbox(revision) WHERE published_at IS NULL;
CREATE TABLE outbox_chunk (
    payload uuid NOT NULL,
    stream text NOT NULL,
    n integer NOT NULL CHECK (n >= 0),
    body bytea NOT NULL CHECK (octet_length(body) <= 1048576),
    PRIMARY KEY (payload, stream, n)
);
CREATE TABLE change_log (
    revision bigint NOT NULL REFERENCES outbox(revision),
    seq integer NOT NULL CHECK (seq > 0),
    at timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor text NOT NULL DEFAULT '',
    service_id uuid,
    entity text NOT NULL,
    entity_id uuid,
    action text NOT NULL CHECK (action IN ('create','update','delete')),
    before jsonb,
    after jsonb,
    PRIMARY KEY (revision, seq)
);
CREATE INDEX change_log_service_revision ON change_log(service_id, revision, seq);
CREATE TABLE publish_state (
    id integer PRIMARY KEY CHECK (id=1),
    epoch integer NOT NULL DEFAULT 1,
    last_published bigint NOT NULL DEFAULT 0
);
INSERT INTO publish_state(id) VALUES (1);
CREATE UNLOGGED TABLE tx_dirty (xid xid8 PRIMARY KEY);

-- +goose Down
DROP TABLE tx_dirty, publish_state, change_log, outbox_chunk, outbox;
