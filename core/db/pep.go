package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/pepauth"
)

// The PEP directory: the identity providers whose tokens a PEP presents, the clients of the services that call the PEP API and
// the API keys issued to them (migration 00006). Every write runs in its own transaction with the bump of the pep scope, so the
// directory read at a revision holds every write up to it. No write touches the rules scope: a PEP change decides nothing.

// The kinds of a client (pep_client.kind).
const (
	PEPKindJWT    = "jwt"
	PEPKindAPIKey = "api_key"
)

// PEPKeyLimit is how many API keys a client has active at once. A key is active until it is revoked or expires: rotation issues
// the new key while the old one still works, then revokes the old one.
const PEPKeyLimit = 2

// The defaults of the columns, for the writes that leave them out (the same values as the migration).
var (
	pepDefaultAlgs       = []string{"RS256", "ES256", "EdDSA"}
	pepDefaultOperations = []string{"evaluate"}
)

// The errors of the PEP directory. The admin layer maps them to answers; any other database error is returned as it is.
var (
	// ErrPEPKeyLimit is returned when the client already has PEPKeyLimit active keys: one of them must be revoked first.
	ErrPEPKeyLimit = errors.New("the PEP client already has the maximum number of active API keys")
	// ErrPEPClientInvalid is returned for a client that does not fit: its kind, issuer, client id or operations do not go together,
	// its issuer does not exist, or its service has another live client with the same name or the same issuer and client id.
	ErrPEPClientInvalid = errors.New("the PEP client is not valid")
	// ErrPEPIssuerInUse is returned when a client, live or deleted, uses the issuer.
	ErrPEPIssuerInUse = errors.New("the PEP issuer is used by a client")
	// ErrPEPNotFound is returned when the issuer, the client or the key does not exist (a deleted client does not exist).
	ErrPEPNotFound = errors.New("no such PEP issuer, client or key")
)

// PEPIssuer is an identity provider whose tokens a PEP may present (pep_issuer).
type PEPIssuer struct {
	Issuer     string   `json:"issuer"`
	JWKSURL    string   `json:"jwks_url"`
	Audiences  []string `json:"audiences"`
	Algs       []string `json:"algs"`
	RequireTyp bool     `json:"require_typ"`
}

// PEPClient is a client of a service that calls the PEP API (pep_client). A jwt client is a client application of an issuer, so
// Issuer and ClientID are set; an api_key client holds the keys issued by Licetum and has neither.
type PEPClient struct {
	ID         uuid.UUID `json:"id"`
	ServiceID  uuid.UUID `json:"service_id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Issuer     *string   `json:"issuer"`
	ClientID   *string   `json:"client_id"`
	Operations []string  `json:"operations"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// PEPCredential is an API key of a client (pep_credential): the keyed SHA-256 (HMAC) of its random part. The key itself is never stored.
type PEPCredential struct {
	KeyID     string     `json:"key_id"`
	ClientID  uuid.UUID  `json:"client_id"`
	SHA256    []byte     `json:"sha256"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// PEPDirectoryData is what LoadPEPDirectory reads: the issuers, the enabled clients that are not deleted, the credentials of those
// clients that are not revoked, and the revision of the pep scope. It is one snapshot.
type PEPDirectoryData struct {
	Revision    int64
	Issuers     []PEPIssuer
	Clients     []PEPClient
	Credentials []PEPCredential
}

// pepDirectorySQL reads the directory in one statement, so that the rows and the revision are one snapshot. An expired
// credential is kept: its expiry moves with the clock, not with a write, so the caller reads it against the clock of each request.
const pepDirectorySQL = `
SELECT
	(SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'issuer', i.issuer, 'jwks_url', i.jwks_url, 'audiences', i.audiences, 'algs', i.algs, 'require_typ', i.require_typ)
		ORDER BY i.issuer), '[]'::jsonb)
	 FROM pep_issuer i),
	(SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'id', c.id, 'service_id', c.service_id, 'name', c.name, 'kind', c.kind, 'issuer', c.issuer, 'client_id', c.client_id,
		'operations', c.operations, 'enabled', c.enabled, 'created_at', c.created_at)
		ORDER BY c.service_id, c.name), '[]'::jsonb)
	 FROM pep_client c JOIN service s ON s.id = c.service_id AND s.deleted_at IS NULL
	 WHERE c.enabled AND c.deleted_at IS NULL),
	(SELECT COALESCE(jsonb_agg(jsonb_build_object(
		'key_id', k.key_id, 'client_id', k.client_id, 'sha256', replace(encode(k.sha256, 'base64'), chr(10), ''),
		'expires_at', k.expires_at, 'revoked_at', k.revoked_at)
		ORDER BY k.key_id), '[]'::jsonb)
	 FROM pep_credential k JOIN pep_client c ON c.id = k.client_id JOIN service s ON s.id = c.service_id AND s.deleted_at IS NULL
	 WHERE k.revoked_at IS NULL AND c.enabled AND c.deleted_at IS NULL),
	COALESCE((SELECT revision FROM revision_state WHERE scope = 'pep'), 0)`

// LoadPEPDirectory reads the PEP directory in one statement, through q (the reader of the check path, see engine.PEPDirectory).
func LoadPEPDirectory(ctx context.Context, q Querier) (PEPDirectoryData, error) {
	var issuers, clients, credentials []byte
	var out PEPDirectoryData
	if err := q.QueryRow(ctx, pepDirectorySQL).Scan(&issuers, &clients, &credentials, &out.Revision); err != nil {
		return PEPDirectoryData{}, err
	}
	if err := json.Unmarshal(issuers, &out.Issuers); err != nil {
		return PEPDirectoryData{}, fmt.Errorf("read the PEP issuers: %w", err)
	}
	if err := json.Unmarshal(clients, &out.Clients); err != nil {
		return PEPDirectoryData{}, fmt.Errorf("read the PEP clients: %w", err)
	}
	if err := json.Unmarshal(credentials, &out.Credentials); err != nil {
		return PEPDirectoryData{}, fmt.Errorf("read the PEP credentials: %w", err)
	}
	return out, nil
}

// UpsertIssuer creates the issuer or replaces its settings. A nil list of algorithms or audiences is the default (the algorithms)
// or the empty list (the audiences).
func UpsertIssuer(ctx context.Context, pool *pgxpool.Pool, i PEPIssuer) error {
	algs := i.Algs
	if algs == nil {
		algs = slices.Clone(pepDefaultAlgs)
	}
	audiences := i.Audiences
	if audiences == nil {
		audiences = []string{}
	}
	return InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO pep_issuer (issuer, jwks_url, audiences, algs, require_typ)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (issuer) DO UPDATE SET
				jwks_url = EXCLUDED.jwks_url, audiences = EXCLUDED.audiences,
				algs = EXCLUDED.algs, require_typ = EXCLUDED.require_typ`,
			i.Issuer, i.JWKSURL, audiences, algs, i.RequireTyp); err != nil {
			return err
		}
		_, err := BumpRevision(ctx, tx, ScopePEP)
		return err
	})
}

// DeleteIssuer deletes the issuer. An issuer that a client uses, live or deleted, cannot be deleted (ErrPEPIssuerInUse).
func DeleteIssuer(ctx context.Context, pool *pgxpool.Pool, issuer string) error {
	return InTx(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM pep_issuer WHERE issuer = $1`, issuer)
		if err != nil {
			if pgCode(err) == "23503" {
				return ErrPEPIssuerInUse
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrPEPNotFound
		}
		_, err = BumpRevision(ctx, tx, ScopePEP)
		return err
	})
}

// ListIssuers returns the issuers, by name.
func ListIssuers(ctx context.Context, q Querier) ([]PEPIssuer, error) {
	rows, err := q.Query(ctx, `SELECT issuer, jwks_url, audiences, algs, require_typ FROM pep_issuer ORDER BY issuer`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PEPIssuer{}
	for rows.Next() {
		var i PEPIssuer
		if err := rows.Scan(&i.Issuer, &i.JWKSURL, &i.Audiences, &i.Algs, &i.RequireTyp); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// CreatePEPClient adds a client to a service and returns it as stored. A zero id is generated; nil operations are the default
// (evaluate only).
func CreatePEPClient(ctx context.Context, pool *pgxpool.Pool, c PEPClient) (PEPClient, error) {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	if c.Operations == nil {
		c.Operations = slices.Clone(pepDefaultOperations)
	}
	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO pep_client (id, service_id, name, kind, issuer, client_id, operations, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING created_at`,
			c.ID, c.ServiceID, c.Name, c.Kind, c.Issuer, c.ClientID, c.Operations, c.Enabled,
		).Scan(&c.CreatedAt); err != nil {
			return pepClientError(err)
		}
		_, err := BumpRevision(ctx, tx, ScopePEP)
		return err
	})
	if err != nil {
		return PEPClient{}, err
	}
	return c, nil
}

// UpdatePEPClient changes the name, issuer, client id, operations and enabled flag of a client of the service. The kind does not
// change: a client keeps its kind and its keys (a different kind is ErrPEPClientInvalid).
func UpdatePEPClient(ctx context.Context, pool *pgxpool.Pool, c PEPClient) (PEPClient, error) {
	return ModifyPEPClient(ctx, pool, c.ServiceID, c.ID, func(cur *PEPClient) error {
		if cur.Kind != c.Kind {
			return ErrPEPClientInvalid
		}
		cur.Name, cur.Issuer, cur.ClientID, cur.Enabled = c.Name, c.Issuer, c.ClientID, c.Enabled
		cur.Operations = c.Operations
		return nil
	})
}

// ModifyPEPClient changes a client of the service: the row is read under its lock, change edits the copy, and the copy is
// written back, so two changes of different fields never lose one another. A nil operations list is the default (evaluate only);
// change may return an error (ErrPEPClientInvalid for a kind that does not fit), which ends the write. A client that does not
// exist, or is deleted, is ErrPEPNotFound.
func ModifyPEPClient(ctx context.Context, pool *pgxpool.Pool, serviceID, clientID uuid.UUID, change func(*PEPClient) error) (PEPClient, error) {
	var c PEPClient
	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id, service_id, name, kind, issuer, client_id, operations, enabled, created_at
			FROM pep_client WHERE id = $1 AND service_id = $2 AND deleted_at IS NULL FOR UPDATE`, clientID, serviceID,
		).Scan(&c.ID, &c.ServiceID, &c.Name, &c.Kind, &c.Issuer, &c.ClientID, &c.Operations, &c.Enabled, &c.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPEPNotFound
		}
		if err != nil {
			return err
		}
		if err := change(&c); err != nil {
			return err
		}
		if c.Operations == nil {
			c.Operations = slices.Clone(pepDefaultOperations)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pep_client SET name = $3, issuer = $4, client_id = $5, operations = $6, enabled = $7
			WHERE id = $1 AND service_id = $2`,
			c.ID, c.ServiceID, c.Name, c.Issuer, c.ClientID, c.Operations, c.Enabled); err != nil {
			return pepClientError(err)
		}
		_, err = BumpRevision(ctx, tx, ScopePEP)
		return err
	})
	if err != nil {
		return PEPClient{}, err
	}
	return c, nil
}

// DeletePEPClient deletes a client of the service: the client is soft-deleted and its keys are revoked with it.
func DeletePEPClient(ctx context.Context, pool *pgxpool.Pool, serviceID, clientID uuid.UUID) error {
	return InTx(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE pep_client SET deleted_at = now()
			WHERE id = $1 AND service_id = $2 AND deleted_at IS NULL`, clientID, serviceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrPEPNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE pep_credential SET revoked_at = now() WHERE client_id = $1 AND revoked_at IS NULL`, clientID); err != nil {
			return err
		}
		_, err = BumpRevision(ctx, tx, ScopePEP)
		return err
	})
}

// ListPEPClients returns the clients of the service that are not deleted, by name.
func ListPEPClients(ctx context.Context, q Querier, serviceID uuid.UUID) ([]PEPClient, error) {
	rows, err := q.Query(ctx, `
		SELECT id, service_id, name, kind, issuer, client_id, operations, enabled, created_at
		FROM pep_client WHERE service_id = $1 AND deleted_at IS NULL ORDER BY name`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PEPClient{}
	for rows.Next() {
		var c PEPClient
		if err := rows.Scan(&c.ID, &c.ServiceID, &c.Name, &c.Kind, &c.Issuer, &c.ClientID, &c.Operations, &c.Enabled,
			&c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPEPClient returns a client of the service that is not deleted; any other is ErrPEPNotFound.
func GetPEPClient(ctx context.Context, q Querier, serviceID, clientID uuid.UUID) (PEPClient, error) {
	var c PEPClient
	err := q.QueryRow(ctx, `
		SELECT id, service_id, name, kind, issuer, client_id, operations, enabled, created_at
		FROM pep_client WHERE id = $1 AND service_id = $2 AND deleted_at IS NULL`, clientID, serviceID,
	).Scan(&c.ID, &c.ServiceID, &c.Name, &c.Kind, &c.Issuer, &c.ClientID, &c.Operations, &c.Enabled, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PEPClient{}, ErrPEPNotFound
	}
	return c, err
}

// PEPKey is an API key as the management routes show it: the key id and its dates, never the secret or its hash.
type PEPKey struct {
	KeyID     string     `json:"key_id"`
	ClientID  uuid.UUID  `json:"client_id"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// ListPEPKeys returns the keys of a client of the service, revoked ones included, the newest first. A client the service does not
// have is ErrPEPNotFound.
func ListPEPKeys(ctx context.Context, q Querier, serviceID, clientID uuid.UUID) ([]PEPKey, error) {
	if _, err := GetPEPClient(ctx, q, serviceID, clientID); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT key_id, client_id, created_at, expires_at, revoked_at FROM pep_credential
		WHERE client_id = $1 ORDER BY created_at DESC, key_id`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PEPKey{}
	for rows.Next() {
		var k PEPKey
		if err := rows.Scan(&k.KeyID, &k.ClientID, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// IssueAPIKey issues an API key to an api_key client and returns the key, which the caller shows once, and its key id. The
// key is stored as the sha256 of its secret. The check of the limit and the insert run under the lock of the client row, so two
// issues for one client never pass the limit together.
func IssueAPIKey(ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID) (string, string, error) {
	var full, keyID string
	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM pep_client WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			clientID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPEPNotFound
		}
		if err != nil {
			return err
		}
		if kind != PEPKindAPIKey {
			return ErrPEPClientInvalid
		}
		var active int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM pep_credential
			WHERE client_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`,
			clientID).Scan(&active); err != nil {
			return err
		}
		if active >= PEPKeyLimit {
			return ErrPEPKeyLimit
		}
		var hash []byte
		full, keyID, hash, err = pepauth.NewAPIKey()
		if err != nil {
			return fmt.Errorf("generate the API key: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pep_credential (key_id, client_id, sha256) VALUES ($1, $2, $3)`,
			keyID, clientID, hash); err != nil {
			return err
		}
		_, err = BumpRevision(ctx, tx, ScopePEP)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return full, keyID, nil
}

// RevokeAPIKey revokes a key. A key that is already revoked stays revoked and changes nothing; a key that does not exist is
// ErrPEPNotFound.
func RevokeAPIKey(ctx context.Context, pool *pgxpool.Pool, keyID string) error {
	return InTx(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE pep_credential SET revoked_at = now() WHERE key_id = $1 AND revoked_at IS NULL`, keyID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pep_credential WHERE key_id = $1)`, keyID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrPEPNotFound
			}
			return nil
		}
		_, err = BumpRevision(ctx, tx, ScopePEP)
		return err
	})
}

// pepClientError turns a constraint violation of a client row into ErrPEPClientInvalid: the kind, issuer, client id and operations do
// not fit together (23514), the issuer or the service does not exist (23503), or a live client of the service has the same name, or the
// same issuer and client id (23505). Any other error is returned as it is.
func pepClientError(err error) error {
	switch pgCode(err) {
	case "23503", "23505", "23514":
		return ErrPEPClientInvalid
	}
	return err
}

// pgCode is the SQLSTATE of a PostgreSQL error, empty for any other error.
func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
