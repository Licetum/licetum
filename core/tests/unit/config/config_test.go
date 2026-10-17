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
	if cfg.Platform.IsConfigured() {
		t.Error("platform without OAuth2 must not be considered configured")
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

func TestKnownSubjectsAreOffUnlessAskedFor(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Subjects.Enabled || cfg.Subjects.RetentionDays != 90 {
		t.Errorf("defaults: %+v", cfg.Subjects)
	}

	t.Setenv("SUBJECTS__ENABLED", "true")
	t.Setenv("SUBJECTS__RETENTION_DAYS", "0")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Subjects.Enabled || cfg.Subjects.RetentionDays != 0 {
		t.Errorf("set: %+v", cfg.Subjects)
	}
}

func TestLoadPublicURL(t *testing.T) {
	t.Setenv("HTTP__PUBLIC_URL", "https://licetum.example.com/")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.PublicURL != "https://licetum.example.com" {
		t.Errorf("the trailing slash must go: %q", cfg.HTTP.PublicURL)
	}

	for _, bad := range []string{"licetum.example.com", "ftp://x", "https://x/?a=1", "https://x/#f", "https://x/licetum"} {
		t.Setenv("HTTP__PUBLIC_URL", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
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
