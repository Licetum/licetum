package config_test

import (
	"slices"
	"testing"
	"time"

	"github.com/licetum/licetum/core/config"
)

func TestLoadListsAndDefaults(t *testing.T) {
	t.Setenv("CORS__ALLOW_ORIGINS", `["http://a", "http://b"]`)
	t.Setenv("cors__allow_headers", "X-A, X-B")
	t.Setenv("AUTH__STATE_TTL", "60")
	t.Setenv("DB__ECHO", "yes")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Cors.AllowOrigins, []string{"http://a", "http://b"}) {
		t.Errorf("AllowOrigins = %v", cfg.Cors.AllowOrigins)
	}
	if !slices.Equal(cfg.Cors.AllowHeaders, []string{"X-A", "X-B"}) {
		t.Errorf("AllowHeaders (case-insensitive name, CSV) = %v", cfg.Cors.AllowHeaders)
	}
	if !slices.Equal(cfg.Cors.AllowMethods, []string{"*"}) {
		t.Errorf("AllowMethods default = %v", cfg.Cors.AllowMethods)
	}
	if cfg.Auth.StateTTL != time.Minute || !cfg.DB.Echo || cfg.DB.PoolSize != 10 {
		t.Errorf("wrong values: %+v", cfg)
	}
	if cfg.AdminAuth.IsConfigured() {
		t.Error("administrator sign-in without ADMIN_AUTH__* must not be considered configured")
	}
	if cfg.AdminAuth.SubjectClaim != "preferred_username" {
		t.Errorf("the default subject claim is %q", cfg.AdminAuth.SubjectClaim)
	}
	if cfg.Platform.AdminClaim != "preferred_username" || cfg.Platform.AdminGroupName != "Platform administrator" ||
		cfg.Platform.AdminClaimValues != nil {
		t.Errorf("wrong platform admin defaults: %+v", cfg.Platform)
	}
}

func TestLoadPlatformAdmin(t *testing.T) {
	t.Setenv("PLATFORM__ADMIN_CLAIM", "groups")
	t.Setenv("PLATFORM__ADMIN_CLAIM_VALUES", `["licetum-admins", "ops"]`)
	t.Setenv("PLATFORM__ADMIN_GROUP_NAME", "Admins")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Platform
	if p.AdminClaim != "groups" || p.AdminGroupName != "Admins" ||
		!slices.Equal(p.AdminClaimValues, []string{"licetum-admins", "ops"}) {
		t.Errorf("wrong platform admin settings: %+v", p)
	}
}

func TestLoadPlatformAdminGroupNameFallsBackToTheOldVariable(t *testing.T) {
	t.Setenv("PLATFORM__ADMIN_ROLE_NAME", "Admins")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.AdminGroupName != "Admins" {
		t.Fatalf("the old variable is ignored: %q", cfg.Platform.AdminGroupName)
	}
	t.Setenv("PLATFORM__ADMIN_GROUP_NAME", "Operators")
	if cfg, err = config.Load(); err != nil || cfg.Platform.AdminGroupName != "Operators" {
		t.Fatalf("the new variable wins: %q, %v", cfg.Platform.AdminGroupName, err)
	}
}

func TestLoadReportsBadValues(t *testing.T) {
	t.Setenv("DB__POOL_SIZE", "ten")
	t.Setenv("CORS__ALLOW_ORIGINS", `["broken`)
	if _, err := config.Load(); err == nil {
		t.Fatal("expected a parse error")
	}
}

// The registry of known subjects is gone: its settings are named by the warning of the deprecated ones.
func TestTheSettingsOfTheRetiredSubjectRegistryAreDeprecated(t *testing.T) {
	t.Setenv("SUBJECTS__ENABLED", "true")
	t.Setenv("SUBJECTS__RETENTION_DAYS", "0")
	got := config.DeprecatedSettings()
	if !slices.Contains(got, "SUBJECTS__ENABLED") || !slices.Contains(got, "SUBJECTS__RETENTION_DAYS") {
		t.Errorf("DeprecatedSettings = %v", got)
	}
}

func TestLoadPDPAddressIsItsOwn(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.Addr != ":8001" {
		t.Errorf("PDP.Addr default = %q, want :8001", cfg.PDP.Addr)
	}

	// HTTP__ADDR is the admin API's address: the PDP does not read it.
	t.Setenv("HTTP__ADDR", ":8000")
	t.Setenv("PDP__ADDR", "127.0.0.1:9001")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.Addr != "127.0.0.1:9001" || cfg.HTTP.Addr != ":8000" {
		t.Errorf("PDP.Addr = %q, HTTP.Addr = %q", cfg.PDP.Addr, cfg.HTTP.Addr)
	}
}

// An empty PDP__OPS_ADDR is the default, not an empty address: an empty one makes the listener take a random port.
func TestLoadPDPOpsAddressDefaultsWhenEmpty(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.OpsAddr != ":9090" {
		t.Errorf("PDP.OpsAddr default = %q, want :9090", cfg.PDP.OpsAddr)
	}

	t.Setenv("PDP__OPS_ADDR", "")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.OpsAddr != ":9090" {
		t.Errorf("PDP.OpsAddr with an empty PDP__OPS_ADDR = %q, want :9090", cfg.PDP.OpsAddr)
	}

	t.Setenv("PDP__OPS_ADDR", "127.0.0.1:9191")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.OpsAddr != "127.0.0.1:9191" {
		t.Errorf("PDP.OpsAddr = %q, want 127.0.0.1:9191", cfg.PDP.OpsAddr)
	}
}

// TestContract_AZ05_PublicURLIsHTTPSExceptOnLoopback: PDP__PUBLIC_URL is the PDP identifier the PEPs use, so it is https, with
// the loopback exception of the Compose stack and the load test, and no path, query or fragment (ADR 0001, 7.3).
func TestContract_AZ05_PublicURLIsHTTPSExceptOnLoopback(t *testing.T) {
	t.Setenv("PDP__PUBLIC_URL", "https://licetum.example.com/")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.PublicURL != "https://licetum.example.com" {
		t.Errorf("the trailing slash must go: %q", cfg.PDP.PublicURL)
	}

	for _, ok := range []string{"http://localhost:8001", "http://127.0.0.1:8001", "http://[::1]:8001"} {
		t.Setenv("PDP__PUBLIC_URL", ok)
		if _, err := config.Load(); err != nil {
			t.Errorf("%q is a loopback address and must be accepted: %v", ok, err)
		}
	}

	for _, bad := range []string{"licetum.example.com", "ftp://x", "http://licetum.example.com", "https://x/?a=1", "https://x/#f", "https://x/licetum", "https://x?", "https://x#", "https://user:pw@x"} {
		t.Setenv("PDP__PUBLIC_URL", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// PDP__AUDIENCE is read as it is; the default is the PDP identifier, which the token check applies (P2-15).
func TestLoadPDPAudience(t *testing.T) {
	t.Setenv("PDP__AUDIENCE", "licetum-pdp")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.Audience != "licetum-pdp" {
		t.Errorf("PDP.Audience = %q, want licetum-pdp", cfg.PDP.Audience)
	}
}

func TestLoadFastReadPathSettings(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Snapshot.MaxBytes != 256<<20 || cfg.Revision.PollInterval != 250*time.Millisecond || cfg.Revision.WaitMax != 500*time.Millisecond || cfg.Snapshot.MaxAge != time.Minute ||
		cfg.Auth.TokenL1Size != 50_000 || cfg.Cache.AnswerL1Size != 50_000 ||
		cfg.Auth.JWKSTTL != 10*time.Minute || cfg.Auth.JWKSMinRefresh != 30*time.Second || cfg.Auth.InvalidTokenTTL != 10*time.Second ||
		cfg.Background.Workers != 16 || cfg.Background.Queue != 1024 || cfg.Redis.HotTimeout != 50*time.Millisecond {
		t.Errorf("wrong defaults: %+v %+v %+v %+v %+v", cfg.Snapshot, cfg.Auth, cfg.Cache, cfg.Background, cfg.Redis)
	}

	t.Setenv("SNAPSHOT__MAX_BYTES", "1048576")
	t.Setenv("REVISION__POLL_INTERVAL", "100ms")
	t.Setenv("REVISION__WAIT_MAX", "1s")
	t.Setenv("COMPUTE__CONCURRENCY", "3")
	t.Setenv("AUTH__JWKS_TTL", "300") // a bare number is seconds, as in the other settings
	t.Setenv("AUTH__JWKS_MIN_REFRESH", "5s")
	t.Setenv("AUTH__INVALID_TOKEN_TTL", "2s")
	t.Setenv("AUTH__TOKEN_L1_SIZE", "10")
	t.Setenv("CACHE__ANSWER_L1_SIZE", "20")
	t.Setenv("BACKGROUND__WORKERS", "4")
	t.Setenv("BACKGROUND__QUEUE", "8")
	t.Setenv("REDIS__HOT_TIMEOUT", "75ms")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Snapshot.MaxBytes != 1<<20 || cfg.Revision.PollInterval != 100*time.Millisecond || cfg.Revision.WaitMax != time.Second || cfg.Compute.Concurrency != 3 || cfg.Auth.JWKSTTL != 5*time.Minute ||
		cfg.Auth.JWKSMinRefresh != 5*time.Second || cfg.Auth.InvalidTokenTTL != 2*time.Second ||
		cfg.Auth.TokenL1Size != 10 || cfg.Cache.AnswerL1Size != 20 ||
		cfg.Background.Workers != 4 || cfg.Background.Queue != 8 || cfg.Redis.HotTimeout != 75*time.Millisecond {
		t.Errorf("wrong values: %+v %+v %+v %+v %+v", cfg.Snapshot, cfg.Auth, cfg.Cache, cfg.Background, cfg.Redis)
	}
}

func TestLoadRejectsABadDuration(t *testing.T) {
	t.Setenv("REDIS__HOT_TIMEOUT", "fast")
	if _, err := config.Load(); err == nil {
		t.Fatal("a duration that is not one must be refused")
	}
}

func TestLoadSyncPipelineSettings(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Sync
	if s.Lease != 30*time.Second || s.RequestWait != 5*time.Second || s.StatementTimeout != 120*time.Second ||
		s.ScheduleTick != 5*time.Second || s.FreshMinInterval != time.Second || s.KeepWarm || cfg.DB.SyncPoolSize != 4 {
		t.Errorf("wrong synchronization defaults: %+v, sync pool %d", s, cfg.DB.SyncPoolSize)
	}

	t.Setenv("SYNC__LEASE", "9")
	t.Setenv("SYNC__KEEP_WARM", "true")
	t.Setenv("DB__SYNC_POOL_SIZE", "2")
	t.Setenv("SYNC__LOCK_TTL", "10")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.Lease != 9*time.Second || !cfg.Sync.KeepWarm || cfg.DB.SyncPoolSize != 2 {
		t.Errorf("wrong values: %+v", cfg.Sync)
	}
	if got := config.DeprecatedSettings(); !slices.Equal(got, []string{"SYNC__LOCK_TTL"}) {
		t.Errorf("DeprecatedSettings = %v", got)
	}
}

func TestSyncEngineDefaultsToDBOS(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.Engine != "dbos" {
		t.Errorf("default engine = %q, want dbos", cfg.Sync.Engine)
	}
	t.Setenv("SYNC__ENGINE", "postgres")
	if cfg, err = config.Load(); err != nil || cfg.Sync.Engine != "postgres" {
		t.Errorf("engine = %q, %v", cfg.Sync.Engine, err)
	}
	t.Setenv("SYNC__ENGINE", "kafka")
	if _, err := config.Load(); err == nil {
		t.Error("an unknown engine must be refused")
	}
}

func TestLoadResilienceDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.MaxStaleness != 0 {
		t.Errorf("POLICY__MAX_STALENESS defaults to no limit, got %v", cfg.Policy.MaxStaleness)
	}
	if cfg.Shutdown.Drain != 10*time.Second || cfg.Shutdown.Timeout != 30*time.Second {
		t.Errorf("shutdown defaults are 10s and 30s, got %+v", cfg.Shutdown)
	}
	if cfg.HTTP.MaxInflight != 0 || cfg.HTTP.RequestTimeout != 0 {
		t.Errorf("HTTP__MAX_INFLIGHT and HTTP__REQUEST_TIMEOUT default to zero (the defaults of the process), got %d and %v",
			cfg.HTTP.MaxInflight, cfg.HTTP.RequestTimeout)
	}
	if cfg.DB.PgBouncer || cfg.DB.MigrateURI != "" || cfg.Sync.DBOSURI != "" {
		t.Errorf("PgBouncer settings are off by default: %+v %q", cfg.DB, cfg.Sync.DBOSURI)
	}
	if cfg.DB.MaxConns() != 30 {
		t.Errorf("the default pool is 10 + 20 connections, got %d", cfg.DB.MaxConns())
	}
}

func TestLoadResilienceSettings(t *testing.T) {
	t.Setenv("POLICY__MAX_STALENESS", "45s")
	t.Setenv("SHUTDOWN__DRAIN", "0")
	t.Setenv("SHUTDOWN__TIMEOUT", "1m")
	t.Setenv("HTTP__MAX_INFLIGHT", "128")
	t.Setenv("HTTP__REQUEST_TIMEOUT", "2500ms")
	t.Setenv("DB__PGBOUNCER", "true")
	t.Setenv("DB__MIGRATE_URI", "postgres://direct/db")
	t.Setenv("SYNC__DBOS_URI", "postgres://direct/dbos")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.MaxStaleness != 45*time.Second || cfg.Shutdown.Drain != 0 || cfg.Shutdown.Timeout != time.Minute ||
		cfg.HTTP.MaxInflight != 128 || cfg.HTTP.RequestTimeout != 2500*time.Millisecond ||
		!cfg.DB.PgBouncer || cfg.DB.MigrateURI != "postgres://direct/db" || cfg.Sync.DBOSURI != "postgres://direct/dbos" {
		t.Errorf("wrong values: %+v %+v %+v %+v", cfg.Policy, cfg.Shutdown, cfg.HTTP, cfg.DB)
	}
}

func TestLoadRefusesSettingsThatCannotWork(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"a pool of no connection": {"DB__POOL_SIZE": "0"},
		"a negative overflow":     {"DB__MAX_OVERFLOW": "-1"},
		"a negative staleness":    {"POLICY__MAX_STALENESS": "-1s"},
		"no time to shut down":    {"SHUTDOWN__TIMEOUT": "0"},
		"a negative drain":        {"SHUTDOWN__DRAIN": "-1s"},
		"a duration that is not":  {"HTTP__REQUEST_TIMEOUT": "soon"},
		"a bool that is not":      {"DB__PGBOUNCER": "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := config.Load(); err == nil {
				t.Fatalf("%v was accepted", env)
			}
		})
	}
}

// PDP__SEARCH_SCAN_BUDGET bounds the candidates one resource search reads (AZ13); zero or less would make every page empty.
func TestLoadPDPSearchScanBudget(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.SearchScanBudget != 10000 {
		t.Errorf("default = %d, want 10000", cfg.PDP.SearchScanBudget)
	}
	t.Setenv("PDP__SEARCH_SCAN_BUDGET", "250")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PDP.SearchScanBudget != 250 {
		t.Errorf("SearchScanBudget = %d, want 250", cfg.PDP.SearchScanBudget)
	}
	for _, bad := range []string{"0", "-1"} {
		t.Setenv("PDP__SEARCH_SCAN_BUDGET", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestLoadAdminAuth(t *testing.T) {
	t.Setenv("ADMIN_AUTH__ISSUER", "https://idp.example.com/realms/main")
	t.Setenv("ADMIN_AUTH__AUDIENCE", "licetum-admin")
	t.Setenv("ADMIN_AUTH__JWKS_URL", "https://idp.example.com/realms/main/certs")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.AdminAuth
	if !a.IsConfigured() || a.Issuer != "https://idp.example.com/realms/main" || a.Audience != "licetum-admin" ||
		a.SubjectClaim != "preferred_username" {
		t.Errorf("wrong admin auth: %+v", a)
	}
	if err := cfg.RequireAdminAuth(); err != nil {
		t.Errorf("RequireAdminAuth: %v", err)
	}
	// The claim that names the administrators is the default of the claim the admin group is matched by.
	t.Setenv("ADMIN_AUTH__SUBJECT_CLAIM", "email")
	if cfg, err = config.Load(); err != nil || cfg.AdminAuth.SubjectClaim != "email" || cfg.Platform.AdminClaim != "email" {
		t.Errorf("subject claim %q, admin claim %q, %v", cfg.AdminAuth.SubjectClaim, cfg.Platform.AdminClaim, err)
	}
	t.Setenv("PLATFORM__ADMIN_CLAIM", "groups")
	if cfg, err = config.Load(); err != nil || cfg.Platform.AdminClaim != "groups" {
		t.Errorf("PLATFORM__ADMIN_CLAIM must win: %q, %v", cfg.Platform.AdminClaim, err)
	}
}

// Half of the settings is a mistake that would show as a 503 at the first sign-in: it is refused at the start. None of them is
// allowed (the PDP and the worker need no administrators), but the admin API requires them.
func TestLoadAdminAuthRefusesAPartialConfiguration(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequireAdminAuth() == nil {
		t.Error("the admin API must require ADMIN_AUTH__*")
	}
	for name, env := range map[string]map[string]string{
		"only the issuer":        {"ADMIN_AUTH__ISSUER": "https://idp"},
		"no audience":            {"ADMIN_AUTH__ISSUER": "https://idp", "ADMIN_AUTH__JWKS_URL": "https://idp/keys"},
		"a jwks_url that is not": {"ADMIN_AUTH__ISSUER": "https://idp", "ADMIN_AUTH__AUDIENCE": "a", "ADMIN_AUTH__JWKS_URL": "keys"},
	} {
		for k, v := range env {
			t.Setenv(k, v)
		}
		if _, err := config.Load(); err == nil {
			t.Errorf("%s was accepted", name)
		}
		for k := range env {
			t.Setenv(k, "")
		}
	}
}

// The OAuth client of the self-service is gone; a deployment that still sets it is told.
func TestDeprecatedSettingsNameTheOAuthClientOfTheSelfService(t *testing.T) {
	t.Setenv("PLATFORM__CLIENT_SECRET", "s")
	t.Setenv("PLATFORM__ISSUER", "https://idp")
	got := config.DeprecatedSettings()
	if !slices.Contains(got, "PLATFORM__CLIENT_SECRET") || !slices.Contains(got, "PLATFORM__ISSUER") {
		t.Errorf("DeprecatedSettings = %v", got)
	}
}
