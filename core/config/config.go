// Package config reads settings from environment variables grouped by prefix (DB__, AUTH__,
// REDIS__, CORS__, PLATFORM__, HTTP__). Names are case-insensitive. Lists (CORS__ALLOW_ORIGINS,
// PLATFORM__ADMIN_CLAIM_VALUES, ...) are accepted both as a JSON array (`["http://a","http://b"]`)
// and comma-separated.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Database struct {
	URI                string
	PoolSize           int
	MaxOverflow        int
	PoolTimeout        time.Duration
	PoolRecycle        time.Duration
	Echo               bool
	StatementCacheSize int
}

type Auth struct {
	StateSecret string
	StateTTL    time.Duration
}

type Redis struct {
	URI string
}

type Cors struct {
	AllowOrigins     []string
	AllowCredentials bool
	AllowMethods     []string
	AllowHeaders     []string
}

// Platform is the self-service config: Licetum registers itself as a Service to grant permissions
// for managing itself through the same mechanism as for any other service.
// If the OAuth2 fields are empty, seeding is skipped (see db.SeedPlatform). Together with the
// service an admin group is seeded: everyone whose AdminClaim claim takes one of AdminClaimValues
// becomes a member, and the group gets read+write on every platform resource type
// (model.PlatformTypes).
type Platform struct {
	Name             string
	URL              string
	Icon             string
	AuthorizationURL string
	TokenURL         string
	JwksURL          string
	Issuer           string
	ClientID         string
	ClientSecret     string
	AdminGroupName   string
	AdminClaim       string
	AdminClaimValues []string
}

func (p Platform) IsConfigured() bool {
	return p.AuthorizationURL != "" && p.TokenURL != "" && p.JwksURL != "" &&
		p.Issuer != "" && p.ClientID != "" && p.ClientSecret != ""
}

// Sync tunes resource synchronization (see service.ensureSynced and synchronizer.Adapter).
type Sync struct {
	// FetchTTL is how long a fetched list of a source's resources is kept in Redis, so that requests
	// in this window do not go to the source. A synchronizer's own cache_ttl overrides it; 0 — no cache.
	FetchTTL time.Duration
	// LockTTL is when a replica's Redis lock on a synchronization expires if its holder died.
	LockTTL time.Duration
	// LockWait is how long a replica waits for another replica's synchronization of the same types
	// before it does the work itself.
	LockWait time.Duration
}

// Subjects tunes the registry of known subjects (see service.NoteSubject).
type Subjects struct {
	// Enabled turns the registry on: the subjects of verified tokens are remembered with the claims the policy reads.
	Enabled bool
	// RetentionDays is how long a subject that is not seen again is kept; 0 keeps them for good.
	RetentionDays int
}

// Delegation tunes the audit of the calls made on behalf of a person (see service.RecordDelegatedCall).
type Delegation struct {
	// AuditRetentionDays is how long a record is kept; 0 keeps them for good.
	AuditRetentionDays int
}

type HTTP struct {
	// Addr is the address the HTTP server listens on. Empty means the app's own default
	// (:8000 for admin, :8001 for check).
	Addr string
	// PublicURL is the address clients reach the check API at, without a trailing slash (https://licetum.example.com).
	// The AuthZEN discovery document is built from it; without it the document is not served.
	PublicURL string
}

type Config struct {
	DB         Database
	Auth       Auth
	Redis      Redis
	Cors       Cors
	Platform   Platform
	Sync       Sync
	Subjects   Subjects
	Delegation Delegation
	HTTP       HTTP
}

// Load reads the whole environment. Required variables are checked only for the sections the
// calling command actually needs (see RequireDB/RequireAuth/RequireRedis).
func Load() (*Config, error) {
	e := &envReader{}
	cfg := &Config{
		DB: Database{
			URI:                e.str("DB__URI", ""),
			PoolSize:           e.int("DB__POOL_SIZE", 10),
			MaxOverflow:        e.int("DB__MAX_OVERFLOW", 20),
			PoolTimeout:        e.seconds("DB__POOL_TIMEOUT", 30),
			PoolRecycle:        e.seconds("DB__POOL_RECYCLE", 3600),
			Echo:               e.bool("DB__ECHO", false),
			StatementCacheSize: e.int("DB__STATEMENT_CACHE_SIZE", 100),
		},
		Auth: Auth{
			StateSecret: e.str("AUTH__STATE_SECRET", ""),
			StateTTL:    e.seconds("AUTH__STATE_TTL", 300),
		},
		Redis: Redis{
			URI: e.str("REDIS__URI", ""),
		},
		Cors: Cors{
			AllowOrigins:     e.list("CORS__ALLOW_ORIGINS", nil),
			AllowCredentials: e.bool("CORS__ALLOW_CREDENTIALS", false),
			AllowMethods:     e.list("CORS__ALLOW_METHODS", []string{"*"}),
			AllowHeaders:     e.list("CORS__ALLOW_HEADERS", []string{"*"}),
		},
		Platform: Platform{
			Name:             e.str("PLATFORM__NAME", "Licetum"),
			URL:              e.str("PLATFORM__URL", "http://localhost:8000"),
			Icon:             e.str("PLATFORM__ICON", "http://localhost:8000/favicon.ico"),
			AuthorizationURL: e.str("PLATFORM__AUTHORIZATION_URL", ""),
			TokenURL:         e.str("PLATFORM__TOKEN_URL", ""),
			JwksURL:          e.str("PLATFORM__JWKS_URL", ""),
			Issuer:           e.str("PLATFORM__ISSUER", ""),
			ClientID:         e.str("PLATFORM__CLIENT_ID", ""),
			ClientSecret:     e.str("PLATFORM__CLIENT_SECRET", ""),
			// PLATFORM__ADMIN_ROLE_NAME is the name the variable had before the groups split: still honoured.
			AdminGroupName:   e.str("PLATFORM__ADMIN_GROUP_NAME", e.str("PLATFORM__ADMIN_ROLE_NAME", "Platform administrator")),
			AdminClaim:       e.str("PLATFORM__ADMIN_CLAIM", "preferred_username"),
			AdminClaimValues: e.list("PLATFORM__ADMIN_CLAIM_VALUES", nil),
		},
		Sync: Sync{
			FetchTTL: e.seconds("SYNC__FETCH_TTL", 10),
			LockTTL:  e.seconds("SYNC__LOCK_TTL", 60),
			LockWait: e.seconds("SYNC__LOCK_WAIT", 30),
		},
		Subjects: Subjects{
			Enabled:       e.bool("SUBJECTS__ENABLED", false),
			RetentionDays: e.int("SUBJECTS__RETENTION_DAYS", 90),
		},
		Delegation: Delegation{
			AuditRetentionDays: e.int("DELEGATION__AUDIT_RETENTION_DAYS", 365),
		},
		HTTP: HTTP{
			Addr:      e.str("HTTP__ADDR", ""),
			PublicURL: strings.TrimRight(e.str("HTTP__PUBLIC_URL", ""), "/"),
		},
	}
	if u := cfg.HTTP.PublicURL; u != "" {
		if parsed, err := url.Parse(u); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			e.errs = append(e.errs, fmt.Errorf("HTTP__PUBLIC_URL must be an http(s) URL without a path, a query or a fragment, got %q", u))
		}
	}
	if len(e.errs) > 0 {
		return nil, errors.Join(e.errs...)
	}
	return cfg, nil
}

func (c *Config) RequireDB() error {
	if c.DB.URI == "" {
		return errors.New("environment variable DB__URI is not set")
	}
	return nil
}

func (c *Config) RequireRedis() error {
	if c.Redis.URI == "" {
		return errors.New("environment variable REDIS__URI is not set")
	}
	return nil
}

func (c *Config) RequireAuth() error {
	if c.Auth.StateSecret == "" {
		return errors.New("environment variable AUTH__STATE_SECRET is not set")
	}
	return nil
}

// envReader accumulates parse errors to report them all at once rather than one by one.
type envReader struct {
	errs []error
}

// lookup: variable names are case-insensitive, as in pydantic-settings.
func lookup(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func (e *envReader) str(name, def string) string {
	if v, ok := lookup(name); ok {
		return v
	}
	return def
}

func (e *envReader) int(name string, def int) int {
	v, ok := lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: expected an integer, got %q", name, v))
		return def
	}
	return n
}

func (e *envReader) seconds(name string, def int) time.Duration {
	return time.Duration(e.int(name, def)) * time.Second
}

func (e *envReader) bool(name string, def bool) bool {
	v, ok := lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	}
	e.errs = append(e.errs, fmt.Errorf("%s: expected a bool, got %q", name, v))
	return def
}

func (e *envReader) list(name string, def []string) []string {
	v, ok := lookup(name)
	if !ok {
		return def
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "[") {
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			e.errs = append(e.errs, fmt.Errorf("%s: invalid JSON list: %w", name, err))
			return def
		}
		return out
	}
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
