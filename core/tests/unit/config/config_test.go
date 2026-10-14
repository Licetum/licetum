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
