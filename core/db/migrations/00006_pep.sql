-- +goose Up
-- The PEP credentials: the identity providers whose tokens a PEP presents (pep_issuer), the clients of the services that call
-- the PEP API (pep_client: a jwt client is a client application of an issuer, an api_key client holds the keys Licetum issues)
-- and the API keys (pep_credential, which keeps the sha256 of the secret, never the key). Additive: only new tables.
--
-- A change of these tables bumps the revision scope 'pep' (see revision_state), not 'rules': a key rotation changes no
-- decision, and must not flush the answers of every replica. A client is soft-deleted, as services and type resources are, so
-- its name and its identity are free again from the moment it is deleted.

CREATE TABLE pep_issuer (
    issuer      text    PRIMARY KEY,
    jwks_url    text    NOT NULL,
    audiences   text[]  NOT NULL DEFAULT '{}'::text[],
    algs        text[]  NOT NULL DEFAULT '{RS256,ES256,EdDSA}'::text[],
    require_typ boolean NOT NULL DEFAULT false,
    -- Asymmetric algorithms only: a key set holds public keys, and an HMAC algorithm would accept them as secrets.
    CONSTRAINT ck_pep_issuer_algs CHECK (cardinality(algs) > 0 AND algs <@ ARRAY[
        'RS256', 'RS384', 'RS512', 'PS256', 'PS384', 'PS512', 'ES256', 'ES384', 'ES512', 'EdDSA']::text[])
);

CREATE TABLE pep_client (
    id          uuid                     PRIMARY KEY,
    service_id  uuid                     NOT NULL REFERENCES service (id) ON DELETE CASCADE,
    name        text                     NOT NULL,
    kind        text                     NOT NULL CHECK (kind IN ('jwt', 'api_key')),
    issuer      text                     REFERENCES pep_issuer (issuer),
    client_id   text,
    operations  text[]                   NOT NULL DEFAULT '{evaluate}'::text[]
                CHECK (operations <@ ARRAY['evaluate', 'explain', 'search_resource', 'search_action', 'search_subject']::text[]),
    enabled     boolean                  NOT NULL DEFAULT true,
    created_at  timestamp with time zone NOT NULL DEFAULT now(),
    deleted_at  timestamp with time zone,
    CONSTRAINT ck_pep_client_jwt CHECK (kind <> 'jwt' OR (issuer IS NOT NULL AND client_id IS NOT NULL)),
    CONSTRAINT ck_pep_client_api_key CHECK (kind <> 'api_key' OR (issuer IS NULL AND client_id IS NULL))
);

-- The live rows only: a deleted client does not block a new one with the same name, or the same issuer and client id.
CREATE UNIQUE INDEX uq_pep_client_name ON pep_client USING btree (service_id, name) WHERE (deleted_at IS NULL);
CREATE UNIQUE INDEX uq_pep_client_identity ON pep_client USING btree (service_id, issuer, client_id) WHERE (deleted_at IS NULL);

CREATE TABLE pep_credential (
    key_id      text                     PRIMARY KEY,
    client_id   uuid                     NOT NULL REFERENCES pep_client (id) ON DELETE CASCADE,
    sha256      bytea                    NOT NULL CHECK (octet_length(sha256) = 32),
    expires_at  timestamp with time zone,
    revoked_at  timestamp with time zone,
    created_at  timestamp with time zone NOT NULL DEFAULT now()
);

CREATE INDEX ix_pep_credential_client_id ON pep_credential USING btree (client_id);

-- +goose Down
DROP TABLE pep_credential;
DROP TABLE pep_client;
DROP TABLE pep_issuer;
