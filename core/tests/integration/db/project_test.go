//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/outbox"
)

func TestCanonicalProjectExcludesSecretsAndProjectsDatabaseRows(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	if err := db.SeedPlatform(ctx, pool, &config.Config{DB: config.Database{}, Platform: config.Platform{Name: "Licetum", AdminClaim: "preferred_username", AdminClaimValues: []string{"admin"}}, AdminAuth: config.AdminAuth{Issuer: "https://idp.test", Audience: "admin", JWKSURL: "https://idp.test/jwks", SubjectClaim: "preferred_username"}}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"service", "type_resource", "synchronizer", "user_group", "assignment_rule"} {
		t.Run(table, func(t *testing.T) {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM `+table+` LIMIT 1`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			image, err := outbox.Project(ctx, pool, table, map[string]any{"id": id})
			if err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			if err := json.Unmarshal(image.Row, &row); err != nil {
				t.Fatal(err)
			}
			if row["id"] != id.String() {
				t.Fatal("projected the wrong row")
			}
			for _, secret := range []string{"client_secret", "authorization_url", "config", "salt"} {
				if _, ok := row[secret]; ok {
					t.Fatalf("projected secret %s", secret)
				}
			}
			if table == "type_resource" && row["mode"] == nil {
				t.Fatal("missing derived type mode")
			}
		})
	}
	image, err := outbox.Project(ctx, pool, "service", map[string]any{"id": uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	var deleted map[string]any
	if err := json.Unmarshal(image.Row, &deleted); err != nil || deleted["deleted"] != true {
		t.Fatal("missing tombstone", err)
	}
	services, err := outbox.ServicesBelow(ctx, pool, []uuid.UUID{model.PlatformServiceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0] != model.PlatformServiceID {
		t.Fatalf("services below platform: %v", services)
	}
	if _, err := outbox.Project(ctx, pool, `service; DROP TABLE service`, map[string]any{"id": uuid.New()}); err == nil {
		t.Fatal("accepted unregistered identifier")
	}
}
