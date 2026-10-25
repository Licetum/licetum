package model

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// The kinds of a PEP client (pep_client.kind).
const (
	PEPKindJWT    = "jwt"
	PEPKindAPIKey = "api_key"
)

// PEPOperations are the operations of the AuthZEN API that a PEP client may be allowed (pep_client.operations); evaluate is the
// default, explain and the three searches are off until an administrator turns them on.
var PEPOperations = []string{"evaluate", "search_resource", "search_action", "search_subject", "explain"}

// PEPSignatureAlgs are the signature algorithms a PEP issuer may allow: asymmetric ones, since a key set holds public keys and an
// HMAC algorithm would take them for secrets (ck_pep_issuer_algs).
var PEPSignatureAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

const (
	pepNameMax     = 100
	pepIdentityMax = 2048
	pepListMax     = 16
)

// PEPClientInput creates a PEP client of a service.
type PEPClientInput struct {
	_          struct{} `additionalProperties:"true"`
	Name       string   `json:"name" minLength:"1" maxLength:"100" doc:"Name of the client, unique within the service" example:"checkout"`
	Kind       string   `json:"kind" enum:"jwt,api_key" doc:"jwt: a client application of a registered issuer, which presents its access tokens; api_key: the client holds API keys that Licetum issues"`
	Issuer     *string  `json:"issuer,omitempty" required:"false" doc:"jwt only: the registered issuer (its iss) the tokens of the client come from" example:"https://idp.example.com/realms/main"`
	ClientID   *string  `json:"client_id,omitempty" required:"false" doc:"jwt only: the client of the issuer (the azp, or client_id, of its tokens)" example:"checkout-backend"`
	Operations []string `json:"operations,omitempty" required:"false" enum:"evaluate,search_resource,search_action,search_subject,explain" doc:"What the client may call. Absent: evaluate only; explain and the three searches are off by default"`
	Enabled    *bool    `json:"enabled,omitempty" required:"false" doc:"Whether the client is accepted; absent: true"`
}

// Validate checks the shape of the body and normalizes it: the strings are trimmed, the operations de-duplicated.
func (c *PEPClientInput) Validate() error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return required("name")
	}
	if len(c.Name) > pepNameMax {
		return apperr.Unprocessable(i18n.FieldInvalid, "name")
	}
	var err error
	if c.Issuer, err = trimmedOptional("issuer", c.Issuer); err != nil {
		return err
	}
	if c.ClientID, err = trimmedOptional("client_id", c.ClientID); err != nil {
		return err
	}
	switch c.Kind {
	case PEPKindJWT:
		if c.Issuer == nil {
			return required("issuer")
		}
		if c.ClientID == nil {
			return required("client_id")
		}
	case PEPKindAPIKey:
		if c.Issuer != nil {
			return apperr.Unprocessable(i18n.FieldInvalid, "issuer")
		}
		if c.ClientID != nil {
			return apperr.Unprocessable(i18n.FieldInvalid, "client_id")
		}
	default:
		return apperr.Unprocessable(i18n.FieldInvalid, "kind")
	}
	if c.Operations != nil {
		if c.Operations, err = normalizePEPOperations(c.Operations); err != nil {
			return err
		}
	}
	return nil
}

// PEPClientPatch changes a PEP client: a field that is absent stays as it is. The kind does not change.
type PEPClientPatch struct {
	_          struct{}  `additionalProperties:"true"`
	Name       *string   `json:"name,omitempty" required:"false" minLength:"1" maxLength:"100" doc:"New name of the client"`
	Issuer     *string   `json:"issuer,omitempty" required:"false" doc:"jwt only: the registered issuer the tokens of the client come from"`
	ClientID   *string   `json:"client_id,omitempty" required:"false" doc:"jwt only: the client of the issuer"`
	Operations *[]string `json:"operations,omitempty" required:"false" enum:"evaluate,search_resource,search_action,search_subject,explain" doc:"What the client may call; replaces the list"`
	Enabled    *bool     `json:"enabled,omitempty" required:"false" doc:"Whether the client is accepted. A disabled client's API keys are refused (401) and its tokens get the same 403 as an unregistered client"`
}

// Validate checks and normalizes the fields that are present.
func (p *PEPClientPatch) Validate() error {
	var err error
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return required("name")
		}
		if len(name) > pepNameMax {
			return apperr.Unprocessable(i18n.FieldInvalid, "name")
		}
		p.Name = &name
	}
	if p.Issuer, err = trimmedOptional("issuer", p.Issuer); err != nil {
		return err
	}
	if p.ClientID, err = trimmedOptional("client_id", p.ClientID); err != nil {
		return err
	}
	if p.Operations != nil {
		ops, err := normalizePEPOperations(*p.Operations)
		if err != nil {
			return err
		}
		p.Operations = &ops
	}
	return nil
}

// PEPClient is a PEP client as the API returns it.
type PEPClient struct {
	ID         uuid.UUID `json:"id" format:"uuid" doc:"Client id"`
	ServiceID  uuid.UUID `json:"service_id" format:"uuid" doc:"The service the client belongs to"`
	Name       string    `json:"name" doc:"Name of the client, unique within the service"`
	Kind       string    `json:"kind" enum:"jwt,api_key" doc:"jwt or api_key"`
	Issuer     *string   `json:"issuer" doc:"jwt only: the issuer of the client's tokens"`
	ClientID   *string   `json:"client_id" doc:"jwt only: the client of the issuer"`
	Operations []string  `json:"operations" nullable:"false" doc:"What the client may call"`
	Enabled    bool      `json:"enabled" doc:"Whether the client is accepted"`
	CreatedAt  time.Time `json:"created_at" doc:"When the client was created"`
}

// PEPKey is an API key of a client as the API lists it: never the secret.
type PEPKey struct {
	KeyID     string     `json:"key_id" doc:"Identifies the key (it is the part of the key after lct_); the key itself is never stored" example:"3f9a1c0d5b7e2a64"`
	CreatedAt time.Time  `json:"created_at" doc:"When the key was issued"`
	ExpiresAt *time.Time `json:"expires_at" doc:"When the key stops working; null: never"`
	RevokedAt *time.Time `json:"revoked_at" doc:"When the key was revoked; null: it is not"`
	Active    bool       `json:"active" doc:"Whether the key works now: not revoked and not expired. A client has at most two active keys"`
}

// PEPKeyIssued is the answer that issues an API key: the only time the key itself is shown.
type PEPKeyIssued struct {
	PEPKey
	Key string `json:"key" doc:"The API key (lct_<key id>_<secret>). It is shown once and cannot be read again; send it as the bearer token of the AuthZEN API"`
}

// PEPIssuer is an identity provider whose tokens a PEP may present (pep_issuer).
type PEPIssuer struct {
	_          struct{} `additionalProperties:"true"`
	Issuer     string   `json:"issuer" minLength:"1" maxLength:"2048" doc:"The iss of the tokens, compared exactly" example:"https://idp.example.com/realms/main"`
	JWKSURL    string   `json:"jwks_url" doc:"Where the keys of the issuer are read" example:"https://idp.example.com/realms/main/protocol/openid-connect/certs"`
	Audiences  []string `json:"audiences,omitempty" required:"false" doc:"The values one of which the aud of a token must contain. Absent or empty: the audience of the PDP (PDP__AUDIENCE, else PDP__PUBLIC_URL). A token always needs an aud"`
	Algs       []string `json:"algs,omitempty" required:"false" enum:"RS256,RS384,RS512,PS256,PS384,PS512,ES256,ES384,ES512,EdDSA" doc:"The signature algorithms accepted. Absent: RS256, ES256 and EdDSA. Asymmetric algorithms only"`
	RequireTyp bool     `json:"require_typ" required:"false" doc:"Accept only tokens whose typ header is at+jwt (RFC 9068)"`
}

// Validate checks the body and normalizes it: the issuer is kept as it is (it is compared exactly), the JWKS URL is a URL.
func (i *PEPIssuer) Validate() error {
	if strings.TrimSpace(i.Issuer) == "" {
		return required("issuer")
	}
	if i.Issuer != strings.TrimSpace(i.Issuer) || len(i.Issuer) > pepIdentityMax {
		return apperr.Unprocessable(i18n.FieldInvalid, "issuer")
	}
	jwks, err := NormalizeHTTPURL("jwks_url", i.JWKSURL)
	if err != nil {
		return err
	}
	i.JWKSURL = jwks
	if len(i.Audiences) > pepListMax {
		return apperr.Unprocessable(i18n.FieldInvalid, "audiences")
	}
	for k, a := range i.Audiences {
		i.Audiences[k] = strings.TrimSpace(a)
		if i.Audiences[k] == "" || len(i.Audiences[k]) > pepIdentityMax {
			return apperr.Unprocessable(i18n.FieldInvalid, "audiences")
		}
	}
	i.Audiences = slices.Compact(slices.Sorted(slices.Values(i.Audiences)))
	if i.Algs != nil {
		if len(i.Algs) == 0 || len(i.Algs) > len(PEPSignatureAlgs) {
			return apperr.Unprocessable(i18n.FieldInvalid, "algs")
		}
		for _, a := range i.Algs {
			if !slices.Contains(PEPSignatureAlgs, a) {
				return apperr.Unprocessable(i18n.FieldInvalid, "algs")
			}
		}
		i.Algs = slices.Compact(slices.Sorted(slices.Values(i.Algs)))
	}
	return nil
}

// trimmedOptional trims an optional string; a blank one is an error, since "" would never be an issuer or a client id.
func trimmedOptional(field string, v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	t := strings.TrimSpace(*v)
	if t == "" || len(t) > pepIdentityMax {
		return nil, apperr.Unprocessable(i18n.FieldInvalid, field)
	}
	return &t, nil
}

// normalizePEPOperations checks the operations against the known ones and drops the repeated, keeping the order in which they were
// given.
func normalizePEPOperations(ops []string) ([]string, error) {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		if !slices.Contains(PEPOperations, op) {
			return nil, apperr.Unprocessable(i18n.FieldInvalid, "operations")
		}
		if !slices.Contains(out, op) {
			out = append(out, op)
		}
	}
	return out, nil
}
