package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// The synchronizer kind in the API is "sql" / "http"; in the DB it is the synchronizer_kind enum with
// values 'SQL' / 'HTTP' (SQLAlchemy stored enum member names, not values).
const (
	KindSQL  = "sql"
	KindHTTP = "http"
)

func KindToDB(kind string) string   { return strings.ToUpper(kind) }
func KindFromDB(kind string) string { return strings.ToLower(kind) }

// SynchronizerConfig is a union discriminated by kind:
//
//	sql:  dsn, query (one resource by :id), list_query (all resources), optional parents_query and
//	      parents_list_query (the parents of a resource / of all resources, for several parents)
//	http: url ({url}/{id} — one resource, {url} — the list), optional headers
type SynchronizerConfig struct {
	_                struct{}          `additionalProperties:"true"`
	Kind             string            `json:"kind" enum:"sql,http" doc:"Source kind"`
	DSN              string            `json:"dsn,omitempty" doc:"kind=sql: connection string of the PostgreSQL source" example:"postgresql://user:pass@host:5432/db"`
	Query            string            `json:"query,omitempty" doc:"kind=sql: query of a single resource by :id returning the columns id, name, parent_id" example:"SELECT id, name, parent_id FROM branch WHERE id = :id"`
	ListQuery        string            `json:"list_query,omitempty" doc:"kind=sql: query listing all resources (id, name, parent_id), without a filter" example:"SELECT id, name, parent_id FROM branch"`
	ParentsQuery     string            `json:"parents_query,omitempty" doc:"kind=sql, optional: query of the parents of a single resource by :id returning the columns parent_type, parent_id — for resources with several parents; parent_type is the name of a parent type of this resource type (service/name if ambiguous)" example:"SELECT 'folder' AS parent_type, folder_id AS parent_id FROM document_folder WHERE document_id = :id"`
	ParentsListQuery string            `json:"parents_list_query,omitempty" doc:"kind=sql, optional: query of the parents of all resources returning the columns id, parent_type, parent_id (one row per link)" example:"SELECT document_id AS id, 'folder' AS parent_type, folder_id AS parent_id FROM document_folder"`
	URL              string            `json:"url,omitempty" format:"uri" doc:"kind=http: base URL — {url}/{id} returns one resource, {url} returns them all" example:"https://filials.local/api/branch"`
	Headers          map[string]string `json:"headers,omitempty" doc:"kind=http: headers of the requests to the source (e.g. Authorization). The values are secrets: responses mask them (********) and they are never logged" example:"{\"Authorization\":\"Bearer <token>\"}"`
	CacheTTL         *int              `json:"cache_ttl,omitempty" doc:"Seconds to keep the list of the source's resources in Redis, so that requests in this window do not go to the source (0 — no cache; omitted — the default SYNC__FETCH_TTL). The resource cache may lag the source by this much" example:"30"`
}

// MaxCacheTTL bounds cache_ttl (a day).
const MaxCacheTTL = 86_400

// SecretMask replaces the source secrets (the password in a DSN, header values) in API responses. In
// an update request the mask means "keep the stored value" — see RestoreSecrets.
const SecretMask = "********"

// Masked returns a copy of the config with the secrets masked.
func (c SynchronizerConfig) Masked() SynchronizerConfig {
	if start, end, ok := dsnPasswordSpan(c.DSN); ok && start < end {
		c.DSN = c.DSN[:start] + SecretMask + c.DSN[end:]
	}
	if len(c.Headers) > 0 {
		masked := make(map[string]string, len(c.Headers))
		for name := range c.Headers {
			masked[name] = SecretMask
		}
		c.Headers = masked
	}
	return c
}

// Secrets lists the secret values of the config (the DSN password, as written and unescaped, and the header values), to
// keep them out of anything shown to a person.
func (c SynchronizerConfig) Secrets() []string {
	var out []string
	if start, end, ok := dsnPasswordSpan(c.DSN); ok && start < end {
		out = append(out, c.DSN[start:end])
		if plain, err := url.PathUnescape(c.DSN[start:end]); err == nil && plain != c.DSN[start:end] {
			out = append(out, plain)
		}
	}
	for _, value := range c.Headers {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// RestoreSecrets puts the stored secrets back wherever the client sent the mask (i.e. did not change
// the value).
func (c SynchronizerConfig) RestoreSecrets(old SynchronizerConfig) SynchronizerConfig {
	if start, end, ok := dsnPasswordSpan(c.DSN); ok {
		password, err := url.PathUnescape(c.DSN[start:end])
		oldStart, oldEnd, oldOK := dsnPasswordSpan(old.DSN)
		if err == nil && password == SecretMask && oldOK && oldStart < oldEnd {
			c.DSN = c.DSN[:start] + old.DSN[oldStart:oldEnd] + c.DSN[end:]
		}
	}
	if len(c.Headers) > 0 {
		restored := make(map[string]string, len(c.Headers))
		for name, value := range c.Headers {
			if oldValue, ok := old.Headers[name]; ok && value == SecretMask {
				value = oldValue
			}
			restored[name] = value
		}
		c.Headers = restored
	}
	return c
}

// dsnPasswordSpan returns the byte range of the (still escaped) password in a DSN
// scheme://user:password@host/...; ok is false when the DSN has no password part.
func dsnPasswordSpan(dsn string) (start, end int, ok bool) {
	i := strings.Index(dsn, "://")
	if i < 0 {
		return 0, 0, false
	}
	authStart := i + 3
	authority := dsn[authStart:]
	if cut := strings.IndexAny(authority, "/?#"); cut >= 0 {
		authority = authority[:cut]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return 0, 0, false
	}
	colon := strings.Index(authority[:at], ":")
	if colon < 0 {
		return 0, 0, false
	}
	return authStart + colon + 1, authStart + at, true
}

// StoredConfig is what goes into the synchronizer.config column (without kind).
func (c SynchronizerConfig) StoredConfig() map[string]any {
	stored := map[string]any{}
	if c.Kind == KindSQL {
		stored = map[string]any{"dsn": c.DSN, "query": c.Query, "list_query": c.ListQuery}
		if c.ParentsQuery != "" {
			stored["parents_query"] = c.ParentsQuery
		}
		if c.ParentsListQuery != "" {
			stored["parents_list_query"] = c.ParentsListQuery
		}
	} else {
		stored["url"] = c.URL
		if len(c.Headers) > 0 {
			stored["headers"] = c.Headers
		}
	}
	if c.CacheTTL != nil {
		stored["cache_ttl"] = *c.CacheTTL
	}
	return stored
}

// ConfigFromDB builds the config from the kind enum column and the config JSONB column.
func ConfigFromDB(kind string, raw []byte) (SynchronizerConfig, error) {
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return SynchronizerConfig{}, err
	}
	get := func(k string) string {
		s, _ := stored[k].(string)
		return s
	}
	var headers map[string]string
	if raw, ok := stored["headers"].(map[string]any); ok && len(raw) > 0 {
		headers = make(map[string]string, len(raw))
		for name, value := range raw {
			headers[name], _ = value.(string)
		}
	}
	var cacheTTL *int
	if v, ok := stored["cache_ttl"].(float64); ok {
		n := int(v)
		cacheTTL = &n
	}
	return SynchronizerConfig{
		CacheTTL:  cacheTTL,
		Kind:      KindFromDB(kind),
		DSN:       get("dsn"),
		Query:     get("query"),
		ListQuery: get("list_query"),
		URL:       get("url"),
		Headers:   headers,

		ParentsQuery:     get("parents_query"),
		ParentsListQuery: get("parents_list_query"),
	}, nil
}

type SynchronizerInput struct {
	_               struct{}           `additionalProperties:"true"`
	Config          SynchronizerConfig `json:"config" doc:"Source configuration"`
	ProbeResourceID string             `json:"probe_resource_id" doc:"Id of an existing resource used to check the source before saving" example:"b1"`
	// Regions: absent keeps the regions a synchronizer has (empty on creation), an empty list clears them.
	Regions []string `json:"regions,omitempty" required:"false" doc:"Regions the source can be reached from (lowercase letters, digits, hyphens; at most 16); empty means from anywhere. Only the workers of these regions (WORKER__REGION) synchronize the type. Absent on an update keeps them" example:"eu"`
}

// NormalizeRegions validates the regions of an input and returns them sorted and without repeats (nil stays nil: absent).
func NormalizeRegions(regions []string) ([]string, error) {
	if regions == nil {
		return nil, nil
	}
	if len(regions) > 16 {
		return nil, fmt.Errorf("regions: at most 16, got %d", len(regions))
	}
	out := make([]string, 0, len(regions))
	seen := map[string]bool{}
	for _, r := range regions {
		if !ValidRegion(r) {
			return nil, fmt.Errorf("regions: %q is not a region name (lowercase letters, digits and hyphens, at most 32)", r)
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *SynchronizerInput) Validate() error {
	c := &s.Config
	switch c.Kind {
	case KindSQL:
		if c.DSN == "" {
			return required("config.dsn")
		}
		if c.Query == "" {
			return required("config.query")
		}
		if c.ListQuery == "" {
			return required("config.list_query")
		}
		dsn, err := ValidatePostgresDSN("config.dsn", c.DSN)
		if err != nil {
			return err
		}
		*c = SynchronizerConfig{
			Kind: KindSQL, DSN: dsn, Query: c.Query, ListQuery: c.ListQuery,
			ParentsQuery: c.ParentsQuery, ParentsListQuery: c.ParentsListQuery, CacheTTL: c.CacheTTL,
		}
	case KindHTTP:
		if c.URL == "" {
			return required("config.url")
		}
		u, err := NormalizeHTTPURL("config.url", c.URL)
		if err != nil {
			return err
		}
		for name, value := range c.Headers {
			if !validHeaderName(name) || strings.ContainsAny(value, "\r\n\x00") {
				return apperr.Unprocessable(i18n.SynchronizerHeaderInvalid, name)
			}
		}
		headers := c.Headers
		if len(headers) == 0 {
			headers = nil
		}
		*c = SynchronizerConfig{Kind: KindHTTP, URL: u, Headers: headers, CacheTTL: c.CacheTTL}
	default:
		return apperr.Unprocessable(i18n.SynchronizerKindInvalid)
	}
	if c.CacheTTL != nil && (*c.CacheTTL < 0 || *c.CacheTTL > MaxCacheTTL) {
		return apperr.Unprocessable(i18n.CacheTTLInvalid, MaxCacheTTL)
	}
	regions, err := NormalizeRegions(s.Regions)
	if err != nil {
		return apperr.Unprocessable(i18n.SynchronizerRegionsInvalid, err.Error())
	}
	s.Regions = regions
	return nil
}

// SynchronizerTestInput is the body of the source test: a configuration that is not saved.
type SynchronizerTestInput struct {
	_              struct{}           `additionalProperties:"true"`
	Config         SynchronizerConfig `json:"config" doc:"Source configuration to test"`
	SynchronizerID *uuid.UUID         `json:"synchronizer_id" required:"false" nullable:"true" format:"uuid" doc:"The saved source this configuration edits: a secret sent back masked (********) is taken from it"`
}

func (s *SynchronizerTestInput) Validate() error {
	in := SynchronizerInput{Config: s.Config}
	if err := in.Validate(); err != nil {
		return err
	}
	s.Config = in.Config
	return nil
}

// SourceAttribute is an attribute a source reports and how many resources carry it.
type SourceAttribute struct {
	Name  string `json:"name" doc:"Attribute name (a column of the SQL source, a field of the HTTP one)"`
	Count int    `json:"count" doc:"How many of the resources carry it"`
}

// SourceOrphan is a resource whose declared parent is not known.
type SourceOrphan struct {
	ID         string `json:"id" doc:"Resource id"`
	ParentType string `json:"parent_type" doc:"Name of the parent type"`
	ParentID   string `json:"parent_id" doc:"The parent id that is not known"`
}

// SourceTestResult is what a source gave when it was asked, without saving anything.
type SourceTestResult struct {
	Total          int               `json:"total" doc:"Rows the source returned"`
	Sample         []Resource        `json:"sample" nullable:"false" doc:"The first resources, as Licetum would read them"`
	DuplicateCount int               `json:"duplicate_count" doc:"Ids the source returned more than once (the last row wins)"`
	Duplicates     []string          `json:"duplicates" nullable:"false" doc:"Up to 20 of the repeated ids"`
	OrphanCount    int               `json:"orphan_count" doc:"Resources whose parent is neither in this answer nor in the cache of the parent type; access under a denial that could cover them is refused until the parent appears"`
	Orphans        []SourceOrphan    `json:"orphans" nullable:"false" doc:"Up to 20 of them"`
	Unresolvable   int               `json:"unresolvable_links" doc:"Parent links that name no parent type of this type (the type is unknown or ambiguous); they are ignored"`
	Attributes     []SourceAttribute `json:"attributes" nullable:"false" doc:"The attributes the resources carry, the most common first"`
	DurationMS     int64             `json:"duration_ms" doc:"How long the source took to answer"`
}

type Synchronizer struct {
	ID             uuid.UUID          `json:"id" format:"uuid" doc:"Synchronizer id"`
	TypeResourceID uuid.UUID          `json:"type_resource_id" format:"uuid" doc:"Resource type id"`
	Config         SynchronizerConfig `json:"config" doc:"Source configuration"`
	Regions        []string           `json:"regions" doc:"Regions the source can be reached from; empty means from anywhere. Only the workers of these regions (WORKER__REGION) synchronize the type" example:"eu"`
}

// ValidRegion reports a region name: lowercase Latin letters, digits and hyphens, starting with a letter or a digit, at most 32.
func ValidRegion(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// validHeaderName reports an RFC 9110 token: letters, digits and !#$%&'*+-.^_`|~.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}
