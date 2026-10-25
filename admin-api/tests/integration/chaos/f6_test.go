//go:build chaos

package chaos_test

// F6-7 needs no read replica: the latency of a far region is injected on the link to PostgreSQL.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

// F6-7: an admin write issued from a region that is not the primary's (its database link has the latency of the region) completes
// correctly, and the write itself costs at most k round trips (k <= 6): the statements of its transaction, one after the other.
// The authorization of the request and the refresh of the policy after the write are the admin API's own reads, which cross the
// link too; the guide says to run the admin API in the region of the primary for that reason.
func TestF6_7_AnAdminWriteFromAFarRegionCostsFewRoundTrips(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org")

	cfg := *c.Cfg
	cfg.DB.URI = c.PostgresFault.Rewrite(c.Cfg.DB.URI)
	cfg.DB.PgBouncer = os.Getenv("LICETUM_F6_DEBUG") != ""
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rdb, err := redisx.Connect(ctx, c.Infra.RedisURI)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	app := service.New(engine.New(pool, rdb, &cfg))
	t.Cleanup(app.Close)

	var lastOps map[string]int
	write := func(user string) (time.Duration, int) {
		ctx, trace := engine.WithTrace(ctx)
		subject := user
		branch, enabled := uuid.MustParse(p.Branch), true
		input := &model.AssignmentRuleInput{TypeResourceID: &branch, Access: []string{"read"}, Subject: &subject, Enabled: &enabled, Effect: "allow"}
		if err := input.Validate(); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if _, err := app.CreateAssignmentRule(ctx, input); err != nil {
			t.Fatalf("write %s: %v", user, err)
		}
		lastOps = trace.DBQueryOps()
		return time.Since(start), trace.DBQueries()
	}
	for i := range 3 { // warm: the first calls open connections
		write(fmt.Sprintf("warm-%d", i))
	}

	const rtt = 150 * time.Millisecond
	c.PostgresFault.Latency(rtt/2, 0)
	var worst time.Duration
	var statements int
	for i := range 5 {
		took, n := write(fmt.Sprintf("far-%d", i))
		worst, statements = max(worst, took), max(statements, n)
	}
	k := float64(worst) / float64(rtt)
	t.Logf("the slowest of 5 writes with a round trip of %v to the primary: %v = %.1f round trips (%d statements: %v)", rtt, worst.Round(time.Millisecond), k, statements, lastOps)
	// Six round trips and the time the processes take themselves.
	if limit := 6*rtt + 100*time.Millisecond*slowdown; worst > limit {
		t.Errorf("an admin write costs %.1f round trips (%v), want at most 6 (%v)", k, worst.Round(time.Millisecond), limit)
	}
	var stored int
	if err := c.Pool.QueryRow(ctx, `SELECT count(*) FROM assignment_rule WHERE subject LIKE 'far-%'`).Scan(&stored); err != nil || stored != 5 {
		t.Fatalf("stored %d of the 5 writes: %v", stored, err)
	}
}
