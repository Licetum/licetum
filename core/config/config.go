// Package config reads settings from environment variables grouped by prefix (DB__, AUTH__,
// REDIS__, PLATFORM__, HTTP__). Names are case-insensitive. Lists (
// PLATFORM__ADMIN_CLAIM_VALUES, ...) are accepted both as a JSON array (`["http://a","http://b"]`)
// and comma-separated.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
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
	// PgBouncer says the URI points at PgBouncer in transaction mode: statements go by the simple protocol and are not
	// prepared, because a prepared statement belongs to one server connection and the next transaction may get another.
	PgBouncer bool
	// MigrateURI is the connection string `licetum migrate` uses, when it must differ from URI: the migrations hold a
	// session-level advisory lock, which needs the same server connection for the whole run, so behind PgBouncer in
	// transaction mode it points at PostgreSQL itself. Empty: URI.
	MigrateURI string
}

// MaxConns is how many connections the request pool of a process may hold: the pool size plus the overflow.
func (d Database) MaxConns() int { return d.PoolSize + d.MaxOverflow }

type Auth struct {
	// JWKSTTL is how long the key set of an identity provider is kept before it is fetched again.
	JWKSTTL time.Duration
	// JWKSMinRefresh is the shortest time between two fetches of the same key set: a token that names an unknown key
	// triggers a fetch at most this often per key set URL.
	JWKSMinRefresh time.Duration
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

// Platform is the self-service config: Licetum registers itself as a Service to grant permissions
// for managing itself through the same mechanism as for any other service. The seed runs when ADMIN_AUTH__* is
// configured (see AdminAuth and db.SeedPlatform). Together with the service an admin group is seeded: everyone whose
// AdminClaim claim takes one of AdminClaimValues becomes a member, and the group gets read+write on every platform
// resource type (model.PlatformTypes).
//
// The service no longer carries an OAuth2 client of its own: administrators sign in at the identity provider of
// ADMIN_AUTH__*, with the web interface as the client. The OAuth half of the old PLATFORM__* settings (the URLs of the
// provider, the issuer, the client id and secret) is gone; see DeprecatedCatalog.
type Platform struct {
	Name             string
	URL              string
	Icon             string
	AdminGroupName   string
	AdminClaim       string
	AdminClaimValues []string
}

// DefaultSubjectClaim is the claim that names an administrator when ADMIN_AUTH__SUBJECT_CLAIM is not set. A token without
// it is named by its sub.
const DefaultSubjectClaim = "preferred_username"

// AdminAuth is how administrators authenticate to the admin API (ADR 0001, 8.12): one static OIDC issuer. The admin API
// verifies the bearer token against the keys of the issuer: iss is compared exactly, aud must contain Audience, exp and nbf
// are checked, and the signature algorithm must be an asymmetric one the verifier allows. The web interface is the OIDC client
// of the issuer (OIDC_* in web/README.md); the admin API never talks to the issuer except to read its keys.
type AdminAuth struct {
	// Issuer is the iss of the tokens, compared exactly (ADMIN_AUTH__ISSUER).
	Issuer string
	// Audience is a value the aud of every token must contain (ADMIN_AUTH__AUDIENCE). It is required: a token issued for
	// another application is never an administrator's.
	Audience string
	// JWKSURL is where the keys of the issuer are read (ADMIN_AUTH__JWKS_URL).
	JWKSURL string
	// SubjectClaim is the claim that names the administrator (ADMIN_AUTH__SUBJECT_CLAIM, default preferred_username); a token
	// without it is named by its sub.
	SubjectClaim string
}

// IsConfigured reports whether administrators can authenticate: the issuer, the audience and the keys are known. The seed of
// the self-service and restore-admin need it; without it the admin API answers 503.
func (a AdminAuth) IsConfigured() bool {
	return a.Issuer != "" && a.Audience != "" && a.JWKSURL != ""
}

// Sync tunes resource synchronization (see service.ensureSynced and synchronizer.Adapter).
type Sync struct {
	// FetchTTL is how long a fetched list of a source's resources is kept in Redis, so that requests
	// in this window do not go to the source. A synchronizer's own cache_ttl overrides it; 0 — no cache.
	FetchTTL time.Duration
	// DBOSPoolSize is the size of the connection pool of the DBOS client of a process; it is outside DB__POOL_SIZE.
	DBOSPoolSize int

	// DBOSSchema is the schema of the DBOS system tables.
	DBOSSchema string
	// DBOSURI is the connection string of the DBOS client when it must differ from DB__URI: DBOS prepares its statements,
	// which PgBouncer in transaction mode carries only from version 1.21 with max_prepared_statements set, so behind an older
	// one it points at PostgreSQL itself. Empty: DB__URI.
	DBOSURI string

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

type HTTP struct {
	// Addr is the address the HTTP server listens on. Empty means the app's own default
	// (:8000 for the admin API; the PDP has its own setting, PDP__ADDR).
	Addr string
	// MaxInflight is how many requests a process serves at once; a request above it is refused with 503 and Retry-After
	// (see server.WithAdmission). Zero takes the default (GOMAXPROCS × 256), a negative value turns the limit off.
	MaxInflight int
	// RequestTimeout is the deadline put on the context of a request. Zero takes the default of the process (10 s for the
	// check API, 60 s for the admin API, whose requests may wait for a synchronization), a negative value sets none.
	RequestTimeout time.Duration
}

// DefaultSearchScanBudget is the default of PDP__SEARCH_SCAN_BUDGET, and what a Config that was not loaded (a zero value) means.
const DefaultSearchScanBudget = 10000

// PEP tunes how the PDP serves the clients that authenticate with PEP credentials (an API key, or a token of a registered issuer).
type PEP struct {
	// RateLimit is how many requests per second one PEP client may send to one PDP pod (PEP__RATE_LIMIT; 0, the default, turns the
	// limit off). It is a token bucket per client and per pod: without a shared state between the pods, the limit of the fleet grows
	// with their number, and a global limit belongs to the ingress in front of the PDPs. A request over the limit is a 429 with
	// Retry-After.
	RateLimit int
	// Burst is how many requests a client may send at once when its bucket is full (PEP__RATE_BURST; 0, the default, is the
	// rate, at least 1).
	Burst int
}

// PDP tunes the regional PDP process (licetum pdp, see cli.Serve).
type PDP struct {
	// Addr is the address the PDP listens on (PDP__ADDR, default :8001). The -addr flag overrides it; HTTP__ADDR is the
	// address of the admin API and is not read for the PDP.
	Addr string
	// OpsAddr is the address of the operations listener (PDP__OPS_ADDR, default :9090): the liveness and readiness probes and
	// the metrics. They are not on Addr, so a client of the PDP cannot reach them (see server.NewOpsHandler).
	OpsAddr string
	// PublicURL is the identifier of the PDP that the PEPs use, without a trailing slash (PDP__PUBLIC_URL, required:
	// https://licetum.example.com). The policy decision point of a service is PublicURL + "/service/" + slug, and the AuthZEN
	// metadata is built from it. Only a loopback host may use http (localhost, 127.0.0.1, [::1]).
	PublicURL string
	// Audience is the audience a client token must carry when its issuer lists none (PDP__AUDIENCE, default PDP__PUBLIC_URL).
	Audience string
	// SearchScanBudget is how many candidate resources one request of the resource search reads (PDP__SEARCH_SCAN_BUDGET, default
	// 10000). A page that the budget stops is shorter than its limit, even empty, and still carries a next_token.
	SearchScanBudget int
}

// Policy tunes what the check API does while it cannot confirm that its copy of the policy is current.
type Policy struct {
	// MaxStaleness is how long the process goes on answering from its copy of the policy after it last read the revision
	// of the policy from PostgreSQL. Beyond it a request is refused with 503 (policy_unavailable): the process fails
	// closed rather than decide from rules that may have been revoked. Zero, the default, means no limit: the process then
	// answers from its copy for as long as PostgreSQL cannot be reached, and a revocation made meanwhile is not seen. Set it
	// where that is not acceptable.
	MaxStaleness time.Duration
}

// Shutdown tunes how a process stops on SIGTERM (see cli.Serve).
type Shutdown struct {
	// Drain is how long the process says it is not ready, and goes on serving, before it stops listening: the time the
	// balancer needs to stop sending it requests.
	Drain time.Duration
	// Timeout is how long the requests in progress have to finish once the process stops listening.
	Timeout time.Duration
}

type Config struct {
	DB         Database
	Auth       Auth
	Redis      Redis
	Platform   Platform
	AdminAuth  AdminAuth
	Sync       Sync
	HTTP       HTTP
	Cache      Cache
	Snapshot   Snapshot
	Background Background
	Revision   Revision
	Compute    Compute
	Policy     Policy
	Shutdown   Shutdown
	PDP        PDP
	PEP        PEP
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
			PgBouncer:          e.bool("DB__PGBOUNCER", false),
			MigrateURI:         e.str("DB__MIGRATE_URI", ""),
		},
		Auth: Auth{

			JWKSTTL:        e.duration("AUTH__JWKS_TTL", 10*time.Minute),
			JWKSMinRefresh: e.duration("AUTH__JWKS_MIN_REFRESH", 30*time.Second),
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
		Policy:  Policy{MaxStaleness: e.duration("POLICY__MAX_STALENESS", 0)},
		Shutdown: Shutdown{
			Drain:   e.duration("SHUTDOWN__DRAIN", 10*time.Second),
			Timeout: e.duration("SHUTDOWN__TIMEOUT", 30*time.Second),
		},
		Background: Background{
			Workers: e.int("BACKGROUND__WORKERS", 16),
			Queue:   e.int("BACKGROUND__QUEUE", 1024),
		},
		Platform: Platform{
			Name: e.str("PLATFORM__NAME", "Licetum"),
			URL:  e.str("PLATFORM__URL", "http://localhost:8000"),
			Icon: e.str("PLATFORM__ICON", "http://localhost:8000/favicon.ico"),
			// PLATFORM__ADMIN_ROLE_NAME is the name the variable had before the groups split: still honoured.
			AdminGroupName:   e.str("PLATFORM__ADMIN_GROUP_NAME", e.str("PLATFORM__ADMIN_ROLE_NAME", "Platform administrator")),
			AdminClaim:       e.str("PLATFORM__ADMIN_CLAIM", e.str("ADMIN_AUTH__SUBJECT_CLAIM", DefaultSubjectClaim)),
			AdminClaimValues: e.list("PLATFORM__ADMIN_CLAIM_VALUES", nil),
		},
		AdminAuth: AdminAuth{
			Issuer:       e.str("ADMIN_AUTH__ISSUER", ""),
			Audience:     e.str("ADMIN_AUTH__AUDIENCE", ""),
			JWKSURL:      e.str("ADMIN_AUTH__JWKS_URL", ""),
			SubjectClaim: e.str("ADMIN_AUTH__SUBJECT_CLAIM", DefaultSubjectClaim),
		},
		Sync: Sync{
			FetchTTL:         e.seconds("SYNC__FETCH_TTL", 10),
			DBOSPoolSize:     e.int("SYNC__DBOS_POOL_SIZE", 20),
			DBOSSchema:       e.str("SYNC__DBOS_SCHEMA", "dbos"),
			DBOSURI:          e.str("SYNC__DBOS_URI", ""),
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
		HTTP: HTTP{
			Addr:           e.str("HTTP__ADDR", ""),
			MaxInflight:    e.int("HTTP__MAX_INFLIGHT", 0),
			RequestTimeout: e.duration("HTTP__REQUEST_TIMEOUT", 0),
		},
		PDP: PDP{
			Addr:             e.str("PDP__ADDR", ":8001"),
			OpsAddr:          e.address("PDP__OPS_ADDR", ":9090"),
			PublicURL:        strings.TrimRight(e.str("PDP__PUBLIC_URL", ""), "/"),
			Audience:         e.str("PDP__AUDIENCE", ""),
			SearchScanBudget: e.int("PDP__SEARCH_SCAN_BUDGET", DefaultSearchScanBudget),
		},
		PEP: PEP{RateLimit: e.int("PEP__RATE_LIMIT", 0), Burst: e.int("PEP__RATE_BURST", 0)},
	}
	if cfg.DB.PoolSize < 1 {
		e.errs = append(e.errs, fmt.Errorf("DB__POOL_SIZE must be positive, got %d", cfg.DB.PoolSize))
	}
	if cfg.DB.MaxOverflow < 0 {
		e.errs = append(e.errs, fmt.Errorf("DB__MAX_OVERFLOW must not be negative, got %d", cfg.DB.MaxOverflow))
	}
	if cfg.Policy.MaxStaleness < 0 {
		e.errs = append(e.errs, fmt.Errorf("POLICY__MAX_STALENESS must not be negative (0 means no limit), got %s", cfg.Policy.MaxStaleness))
	}
	if cfg.Shutdown.Drain < 0 || cfg.Shutdown.Timeout <= 0 {
		e.errs = append(e.errs, fmt.Errorf("SHUTDOWN__DRAIN must not be negative and SHUTDOWN__TIMEOUT must be positive, got %s and %s",
			cfg.Shutdown.Drain, cfg.Shutdown.Timeout))
	}
	if u := cfg.PDP.PublicURL; u != "" {
		if err := checkPublicURL(u); err != nil {
			e.errs = append(e.errs, err)
		}
	}
	if a := cfg.AdminAuth; a.Issuer != "" || a.Audience != "" || a.JWKSURL != "" {
		// A half of the settings is a mistake that would otherwise show as 503 at the first sign-in.
		if !a.IsConfigured() {
			e.errs = append(e.errs, errors.New("ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE and ADMIN_AUTH__JWKS_URL must be set together"))
		} else if u, err := url.Parse(a.JWKSURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e.errs = append(e.errs, fmt.Errorf("ADMIN_AUTH__JWKS_URL must be an http or https URL, got %q", a.JWKSURL))
		}
	}
	if cfg.AdminAuth.SubjectClaim == "" {
		cfg.AdminAuth.SubjectClaim = DefaultSubjectClaim
	}
	if cfg.PEP.RateLimit < 0 || cfg.PEP.Burst < 0 {
		e.errs = append(e.errs, fmt.Errorf("PEP__RATE_LIMIT and PEP__RATE_BURST must not be negative (0 turns the limit off), got %d and %d",
			cfg.PEP.RateLimit, cfg.PEP.Burst))
	}
	if cfg.PDP.SearchScanBudget < 1 {
		e.errs = append(e.errs, fmt.Errorf("PDP__SEARCH_SCAN_BUDGET must be positive, got %d", cfg.PDP.SearchScanBudget))
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

// Deprecated is a setting that no longer has an effect and the one sentence an operator reads about it.
type Deprecated struct {
	Name   string
	Reason string
}

// deprecatedSettings is the table of the settings that were removed but may still be set in a deployment. A pull request
// that removes a setting stops Load() reading it and appends one row here in the same commit; the row's Reason says in one
// sentence what replaced the setting. P3 deletes this mechanism (ADR 9.7).
var deprecatedSettings = []Deprecated{
	{"SYNC__LOCK_TTL", "the Redis lock of the synchronization is gone; a synchronization is one job under a lease, timed by SYNC__LEASE, and this setting has not been read since the lock was removed"},
	{"SYNC__LOCK_WAIT", "the Redis lock of the synchronization is gone; a synchronization is one job under a lease, timed by SYNC__LEASE, and this setting has not been read since the lock was removed"},
	{"HTTP__PUBLIC_URL", "renamed PDP__PUBLIC_URL, which the pdp command requires; HTTP__PUBLIC_URL is not read"},
	// The OAuth client of the self-service: administrators sign in through ADMIN_AUTH__* now.
	{"PLATFORM__AUTHORIZATION_URL", platformOAuthGone},
	{"PLATFORM__TOKEN_URL", platformOAuthGone},
	{"PLATFORM__JWKS_URL", platformOAuthGone},
	{"PLATFORM__ISSUER", platformOAuthGone},
	{"PLATFORM__CLIENT_ID", platformOAuthGone},
	{"PLATFORM__CLIENT_SECRET", platformOAuthGone},
	// The registry of known subjects gave way to the subject directory of each service.
	{"SUBJECTS__ENABLED", subjectRegistryGone},
	{"SUBJECTS__RETENTION_DAYS", subjectRegistryGone},
	// The PDP takes no token of a person (PEP clients authenticate), so the sign-in state, the token caches, the calls on behalf
	// of a person and their audit, and the CORS of the browser are gone: the browser reaches the admin API through its BFF.
	{"AUTH__STATE_SECRET", signInStateGone},
	{"AUTH__STATE_TTL", signInStateGone},
	{"AUTH__TOKEN_L1_SIZE", personCacheGone},
	{"AUTH__INVALID_TOKEN_TTL", personCacheGone},
	{"DELEGATION__AUDIT_RETENTION_DAYS", delegationGone},
	{"DELEGATION__AUDIT_MODE", delegationGone},
	{"DELEGATION__AUDIT_BUFFER", delegationGone},
	{"CORS__ALLOW_ORIGINS", corsGone},
	{"CORS__ALLOW_CREDENTIALS", corsGone},
	{"CORS__ALLOW_METHODS", corsGone},
	{"CORS__ALLOW_HEADERS", corsGone},
	// The synchronization runs on DBOS only.
	{"SYNC__ENGINE", "DBOS is the only synchronization engine; a value of postgres no longer selects the sync_job table"},
	{"WORKER__REGION", "a source with regions may be asked by any executor until regions are enforced again; the worker no longer filters the jobs by region"},
	// The PDP reads the policy from one PostgreSQL.
	{"DB__READ_URI", readReplicaGone},
	{"DB__MAX_REPLICA_LAG", readReplicaGone},
	{"DB__LAG_ACTION", readReplicaGone},
	{"DB__LAG_INTERVAL", readReplicaGone},
}

// The reasons shared by the rows of one feature.
const (
	platformOAuthGone   = "the OAuth client of the self-service is gone; administrators sign in at the identity provider named by ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE and ADMIN_AUTH__JWKS_URL, with the web interface as the client"
	subjectRegistryGone = "the registry of known subjects is gone; each service has a subject directory with its own retention (PUT /service/{id}/subjects/settings)"
	signInStateGone     = "the PDP keeps no sign-in state: per-service OAuth was removed and administrators sign in through ADMIN_AUTH__*"
	personCacheGone     = "the PDP no longer verifies the token of a person against a service, so it keeps no cache of them; PEP clients authenticate with API keys or the tokens of a registered pep_issuer (AUTH__JWKS_TTL and AUTH__JWKS_MIN_REFRESH still apply to their key sets)"
	delegationGone      = "calls on behalf of a person and their audit were removed; the PEP sends the person as the subject of its AuthZEN request"
	corsGone            = "the PDP and the admin API send no CORS headers; the browser reaches the admin API through the web interface's server"
	readReplicaGone     = "the PDP reads the policy from the one PostgreSQL named by DB__URI; routing the reads to a replica and measuring its lag are gone"
)

// DeprecatedCatalog is the table of the removed settings with the reason of each, whether or not they are set. The caller
// gets a copy.
func DeprecatedCatalog() []Deprecated {
	return slices.Clone(deprecatedSettings)
}

// DeprecatedReason is the reason of the removed setting of that (upper-case) name; false when no such row exists.
func DeprecatedReason(name string) (string, bool) {
	for _, d := range deprecatedSettings {
		if d.Name == name {
			return d.Reason, true
		}
	}
	return "", false
}

// DeprecatedSettings names the rows of the table of removed settings whose variable is set (names are case-insensitive, an
// empty value counts as set). The processes log a warning for each, with its reason, so that a deployment notices it.
func DeprecatedSettings() []string {
	var out []string
	for _, d := range deprecatedSettings {
		if _, ok := lookup(d.Name); ok {
			out = append(out, d.Name)
		}
	}
	return out
}

// RequireAdminAuth checks that administrators can authenticate: the admin API refuses to start without ADMIN_AUTH__*, since
// every request would be answered 503.
func (c *Config) RequireAdminAuth() error {
	if !c.AdminAuth.IsConfigured() {
		return errors.New("ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE and ADMIN_AUTH__JWKS_URL are required: administrators sign in at that identity provider")
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

// address is str for a listen address whose empty value is the default: an empty address makes the listener take a random
// port, which no probe or scrape finds.
func (e *envReader) address(name, def string) string {
	if v := e.str(name, def); strings.TrimSpace(v) != "" {
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

// checkPublicURL is the rule of PDP__PUBLIC_URL: https, or http for a loopback host, and no path, query or fragment.
func checkPublicURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || strings.ContainsAny(raw, "?#") {
		return fmt.Errorf("PDP__PUBLIC_URL must be an https URL without a path, a query or a fragment, got %q", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("PDP__PUBLIC_URL must use https (http is allowed only for localhost, 127.0.0.1 or [::1]), got %q", raw)
}
