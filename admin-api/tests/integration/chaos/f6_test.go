//go:build chaos

package chaos_test

// F6-8 and F6-9: three regions (three check replicas, each reading the policy from the read replica, with Redis shared here)
// under load, split 50/30/20; then one region is removed and the other two take its traffic. They need a streaming replica
// (scripts/pg-replica.sh, LICETUM_TEST_REPLICA_ADDR). LICETUM_F6_LOAD_SECONDS sets the length of the steady part (the multi-region
// environment runs ten minutes).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

func loadSeconds(def int) time.Duration {
	if v, err := strconv.Atoi(os.Getenv("LICETUM_F6_LOAD_SECONDS")); err == nil && v > 0 {
		return time.Duration(v) * time.Second
	}
	return time.Duration(def) * time.Second
}

// regionsLoad starts the three regions and a load that picks the region of each request by the weights, among the regions that are
// up. It returns the outcomes of the load (when it ends) and the oracle's count of wrong answers.
type regionsLoad struct {
	c       *harness.Cluster
	tokens  []string
	ref     string // the service the load asks about
	want    string
	weights []int
	up      []atomic.Bool
	client  *http.Client
	wrong   atomic.Int64
	perReg  []atomic.Int64
}

func newRegionsLoad(t *testing.T, weights []int) *regionsLoad {
	c := harness.StartReplicas(t, infra, 3, harness.WithReadReplica(), harness.WithHotRedis(hotTimeout),
		harness.WithConfig(func(cfg *config.Config) { cfg.DB.LagInterval = 500 * time.Millisecond }))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, 30)
	l := &regionsLoad{c: c, tokens: tokens, ref: p.Slug, weights: weights, up: make([]atomic.Bool, 3), perReg: make([]atomic.Int64, 3),
		client: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}, Timeout: 10 * time.Second}}
	t.Cleanup(l.client.CloseIdleConnections)
	var wantIDs []string
	for i, r := range c.Replicas {
		r.Warm(c, harness.Branches(p.Slug, tokens[0]), tokens[0])
		for _, token := range tokens {
			res := r.Permission(c, p.Slug, token)
			if i == 0 && token == tokens[0] {
				l.want, wantIDs = string(res.Body), res.IDs(t)
			}
		}
		l.up[i].Store(true)
	}
	if len(wantIDs) == 0 {
		t.Fatalf("the oracle has no access: %s", l.want)
	}
	return l
}

// region picks the region of request i by the weights, among those that are up.
func (l *regionsLoad) region(i int) int {
	total := 0
	for r, w := range l.weights {
		if l.up[r].Load() {
			total += w
		}
	}
	if total == 0 {
		return -1
	}
	n := (i * 7919) % total
	for r, w := range l.weights {
		if !l.up[r].Load() {
			continue
		}
		if n < w {
			return r
		}
		n -= w
	}
	return -1
}

func (l *regionsLoad) do(i int) (int, error) {
	r := l.region(i)
	if r < 0 {
		return 0, fmt.Errorf("no region is up")
	}
	l.perReg[r].Add(1)
	token := l.tokens[i%len(l.tokens)]
	req, err := harness.HTTPRequest(context.Background(), l.c.Replicas[r].Server.URL, harness.Branches(l.ref, token), token)
	if err != nil {
		return 0, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && string(body) != l.want {
		l.wrong.Add(1) // the oracle: every user of the load has the same access
	}
	return resp.StatusCode, nil
}

// F6-8: 600 requests a second over three regions split 50/30/20: at most 0.1 % errors, a p99 within the budget, 0 wrong answers.
func TestF6_8_SixHundredRequestsASecondOverThreeRegions(t *testing.T) {
	l := newRegionsLoad(t, []int{50, 30, 20})
	rps := 600 / loadDivisor
	d := loadSeconds(20)
	results := openLoop(rps, d, 800, l.do)

	failed := failures(results)
	lat := latencies(results)
	p99 := percentile(lat, 0.99)
	t.Logf("%d requests in %v, %d failed, p99 %v, per region %d/%d/%d, wrong answers %d",
		len(results), d, failed, p99, l.perReg[0].Load(), l.perReg[1].Load(), l.perReg[2].Load(), l.wrong.Load())
	if float64(failed) > 0.001*float64(len(results)) {
		t.Errorf("%d of %d requests failed, want at most 0.1 %%", failed, len(results))
	}
	if limit := 50 * time.Millisecond * slowdown; p99 > limit {
		t.Errorf("p99 = %v, want within the budget %v", p99, limit)
	}
	if n := l.wrong.Load(); n != 0 {
		t.Errorf("%d wrong answers", n)
	}
	split := func(r int) float64 { return float64(l.perReg[r].Load()) / float64(len(results)) }
	if split(0) < 0.45 || split(0) > 0.55 || split(2) < 0.15 || split(2) > 0.25 {
		t.Errorf("the split was %.2f/%.2f/%.2f, want 0.50/0.30/0.20", split(0), split(1), split(2))
	}
}

// F6-9: one region is removed: the other two take its traffic (2x the load on one of them), p99 at most twice the baseline and no
// errors beyond the 15 s of reconnection.
func TestF6_9_ARegionIsRemovedAndTheOthersTakeItsTraffic(t *testing.T) {
	l := newRegionsLoad(t, []int{50, 30, 20})
	rps := 600 / loadDivisor
	steady := loadSeconds(12)
	total := steady + 12*time.Second

	var removedAt time.Time
	start := time.Now()
	go func() {
		time.Sleep(steady)
		removedAt = time.Now()
		l.up[2].Store(false) // the balancer sends no more to the region
		_ = l.c.Replicas[2].Stop(500*time.Millisecond, 5*time.Second)
	}()
	results := openLoop(rps, total, 800, l.do)

	cut := removedAt.Sub(start)
	before, after := between(results, 0, cut), between(results, cut, total)
	baseline := percentile(latencies(before), 0.99)
	took := percentile(latencies(after), 0.99)
	t.Logf("baseline p99 %v over %d requests; after the removal p99 %v over %d requests, %d failed", baseline, len(before), took, len(after), failures(after))
	if limit := max(2*baseline, 20*time.Millisecond*slowdown); took > limit {
		t.Errorf("p99 after the removal = %v, want at most %v (twice the baseline)", took, limit)
	}
	// Errors are allowed in the 15 s of the reconnection that follows the removal; there is no more than that to look at here, and
	// the region is drained, so none is expected at all.
	if n := failures(between(results, cut+15*time.Second, total+time.Second)); n != 0 {
		t.Errorf("%d requests failed more than 15 s after the removal", n)
	}
	if n := failures(after); n != 0 {
		t.Errorf("%d requests failed after the removal", n)
	}
	if n := l.wrong.Load(); n != 0 {
		t.Errorf("%d wrong answers", n)
	}
}

// F6-7: an admin write issued from a region that is not the primary's (its database link has the latency of the region) completes
// correctly, and the write itself costs at most k round trips (k <= 6): the statements of its transaction, one after the other.
// The authorization of the request and the refresh of the policy after the write are the admin API's own reads, which cross the
// link too; the guide says to run the admin API in the region of the primary for that reason.
func TestF6_7_AnAdminWriteFromAFarRegionCostsFewRoundTrips(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)

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
