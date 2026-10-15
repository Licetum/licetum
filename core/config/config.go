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
	// SyncPoolSize is the size of the pool the synchronization transactions run on (the worker only): it is separate
	// from the request pool, so waiting synchronizations never starve the requests.
	SyncPoolSize int
}

type Auth struct {
	StateSecret string
	StateTTL    time.Duration
	// TokenL1Size is how many verified tokens a process keeps in memory (the first level of the token cache; the second
	// is Redis). Zero takes the default, a negative value turns the level off.
	TokenL1Size int
	// JWKSTTL is how long the key set of a service is kept before it is fetched again.
	JWKSTTL time.Duration
	// JWKSMinRefresh is the shortest time between two fetches of the same key set: a token that names an unknown key
	// triggers a fetch at most this often per key set URL.
	JWKSMinRefresh time.Duration
	// InvalidTokenTTL is how long a token that failed verification is remembered, so that the same bad token does not
	// reach the identity provider again.
	InvalidTokenTTL time.Duration
}

type Redis struct {
	URI string
	// HotTimeout is the read and write timeout of the Redis client of the check API (no retries): a slow Redis costs a
	// request at most this much. After a few failures in a row Redis is skipped for a while (see redisx.FastFail).
	HotTimeout time.Duration
}

// Cache tunes the in-process caches in front of Redis.
type Cache struct {
	// AnswerL1Size is how many computed answers a process keeps in memory. Zero takes the default, a negative value
	// turns the level off.
	AnswerL1Size int
}

// Snapshot tunes the in-memory copy of the policy (see engine.snapshot).
type Snapshot struct {
	// MaxBytes bounds the memory of the copy: a policy that would not fit is not copied, and the queries read it. Zero
	// takes the default, a negative value turns the copy off.
	MaxBytes int64
	// MaxAge is the age after which the copy is read again even when the revision did not move (a change made outside
	// the admin API does not bump it). Zero takes the default, a negative value never reads it again by age.
	MaxAge time.Duration
}

// Revision tunes how a process follows the revision of the policy, which lives in PostgreSQL (revision_state).
type Revision struct {
	// PollInterval is how often a process reads the revisions: a write made through another replica reaches this process
	// within this time.
	PollInterval time.Duration
	// WaitMax is how long a request that asks for at least a revision (at_least_as_fresh) waits for this process to reach
	// it before it computes the answer from the data.
	WaitMax time.Duration
}

// Compute tunes the computation of answers that are not cached.
type Compute struct {
	// Concurrency bounds the computations running at once, so that a wave of cache misses queues instead of exhausting
	// the database pool. Zero is half of DB__POOL_SIZE.
	Concurrency int
}

// Background tunes the work done after a request has been answered (writing the cache, refreshing a source).
type Background struct {
	// Workers is how many tasks run at once; Queue is how many wait. A task that finds the queue full is dropped.
	Workers int
	Queue   int
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
	// LockTTL and LockWait belong to the Redis lock that the job table replaced. They are still read, for one release,
	// so that a deployment that sets them gets a deprecation warning instead of silence (see Config.Deprecated).
	LockTTL  time.Duration
	LockWait time.Duration

	// Lease is how long a worker owns a synchronization job without renewing it; it renews every Lease/3. A job whose
	// lease ran out is taken over by another worker.
	Lease time.Duration
	// RequestWait is how long a request waits for a synchronization it needs (a type never synchronized, a platform type
	// after a write) before it answers from the data that exists.
	RequestWait time.Duration
	// AdminWait is the same for the admin paths (resource lists, refresh): they wait for the worker longer.
	AdminWait time.Duration
	// StatementTimeout bounds each statement of a synchronization transaction; the lock timeout is 5 s.
	StatementTimeout time.Duration
	// ScheduleTick is how often the worker looks for the types that are due.
	ScheduleTick time.Duration
	// KeepWarm makes the worker refresh every type with a source when it is due, whether or not anybody asked lately.
	KeepWarm bool
	// DemandWindow is how long after a request for a type the worker keeps it fresh on its schedule.
	DemandWindow time.Duration
	// FreshMinInterval is the least time between two source calls for one type that the fresh requests cause.
	FreshMinInterval time.Duration
	// Concurrency is how many jobs a worker executes at once (default: the size of the synchronization pool).
	Concurrency int
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
	Cache      Cache
	Snapshot   Snapshot
	Background Background
	Revision   Revision
	Compute    Compute
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
			SyncPoolSize:       e.int("DB__SYNC_POOL_SIZE", 4),
		},
		Auth: Auth{
			StateSecret: e.str("AUTH__STATE_SECRET", ""),
			StateTTL:    e.seconds("AUTH__STATE_TTL", 300),

			TokenL1Size:     e.int("AUTH__TOKEN_L1_SIZE", 50_000),
			JWKSTTL:         e.duration("AUTH__JWKS_TTL", 10*time.Minute),
			JWKSMinRefresh:  e.duration("AUTH__JWKS_MIN_REFRESH", 30*time.Second),
			InvalidTokenTTL: e.duration("AUTH__INVALID_TOKEN_TTL", 10*time.Second),
		},
		Redis: Redis{
			URI:        e.str("REDIS__URI", ""),
			HotTimeout: e.duration("REDIS__HOT_TIMEOUT", 50*time.Millisecond),
		},
		Cache: Cache{
			AnswerL1Size: e.int("CACHE__ANSWER_L1_SIZE", 50_000),
		},
		Snapshot: Snapshot{
			MaxBytes: int64(e.int("SNAPSHOT__MAX_BYTES", 256<<20)),
			MaxAge:   e.duration("SNAPSHOT__MAX_AGE", time.Minute),
		},
		Revision: Revision{
			PollInterval: e.duration("REVISION__POLL_INTERVAL", 250*time.Millisecond),
			WaitMax:      e.duration("REVISION__WAIT_MAX", 500*time.Millisecond),
		},
		Compute: Compute{Concurrency: e.int("COMPUTE__CONCURRENCY", 0)},
		Background: Background{
			Workers: e.int("BACKGROUND__WORKERS", 16),
			Queue:   e.int("BACKGROUND__QUEUE", 1024),
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

			Lease:            e.seconds("SYNC__LEASE", 30),
			RequestWait:      e.seconds("SYNC__REQUEST_WAIT", 5),
			AdminWait:        e.seconds("SYNC__ADMIN_WAIT", 30),
			StatementTimeout: e.seconds("SYNC__STATEMENT_TIMEOUT", 120),
			ScheduleTick:     e.seconds("SYNC__SCHEDULE_TICK", 5),
			KeepWarm:         e.bool("SYNC__KEEP_WARM", false),
			DemandWindow:     e.seconds("SYNC__DEMAND_WINDOW", 600),
			FreshMinInterval: e.seconds("SYNC__FRESH_MIN_INTERVAL", 1),
			Concurrency:      e.int("SYNC__CONCURRENCY", 0),
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

// DeprecatedSettings names the settings that are set but no longer have an effect: the Redis lock of the synchronization
// was replaced by the job table (SYNC__LEASE). They are still read for one release; the processes log a warning.
func DeprecatedSettings() []string {
	var out []string
	for _, name := range []string{"SYNC__LOCK_TTL", "SYNC__LOCK_WAIT"} {
		if _, ok := lookup(name); ok {
			out = append(out, name)
		}
	}
	return out
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

// duration reads a Go duration ("50ms", "10m"); a bare integer is a number of seconds, as in the settings above.
func (e *envReader) duration(name string, def time.Duration) time.Duration {
	v, ok := lookup(name)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: expected a duration such as 50ms or 10m, got %q", name, v))
		return def
	}
	return d
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
