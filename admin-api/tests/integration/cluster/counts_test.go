//go:build integration

package cluster_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

// waitForKeys waits until Redis holds a key of each pattern: the answer and the verified token are written in the
// background, after the request that computed them.
func waitForKeys(t *testing.T, c *harness.Cluster, patterns ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, pattern := range patterns {
		for {
			keys, err := c.Redis.Keys(context.Background(), pattern).Result()
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no Redis key matches %s", pattern)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// F0-2: a test can assert the exact number of database queries and Redis round trips of one request, and the same
// counters are in /metrics.
//
// The numbers are those of the fast read path (F1): a request whose token and answer are cached sends no query to
// PostgreSQL and makes no round trip to Redis (before it, a cache hit read PostgreSQL twice, for the slug and for the
// service, and made three round trips to Redis).
func TestF0_2_ARequestHasExactDatabaseAndRedisCounts(t *testing.T) {
	// /metrics counts every statement of the replica, the request's and the background ones; the Trace counts the request's.
	// The background statement of an idle replica is the poll of the revision (WatchRevision, every REVISION__POLL_INTERVAL
	// of 250 ms, see F2-10): one that falls between the two scrapes below is in the delta of /metrics and in no Trace, and the
	// test fails with "/metrics counted db=1, the Trace db=0". Through PgBouncer, where the scrape reads its gauges through the
	// pooler, that happened in two runs out of four on a loaded machine. The replicas poll once a minute here; the requests
	// that ask for a newer revision still read it at once (Replica.Permission asks for the revision of the last write).
	c := startReplicas(t, 2, harness.WithConfig(func(cfg *config.Config) { cfg.Revision.PollInterval = time.Minute }))
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	first, second := c.Replicas[0], c.Replicas[1]

	// The first request computes the answer and verifies the token; both are cached in the background.
	cold := first.Permission(c, p.Slug, alice)
	if cold.Status != http.StatusOK {
		t.Fatalf("permission: %d %s", cold.Status, cold.Body)
	}
	if cold.Trace.DBQueries() <= 2 || cold.Trace.RedisRoundTrips() <= 3 {
		t.Fatalf("a cold request must cost more than a hit: db=%d %v redis=%d %v",
			cold.Trace.DBQueries(), cold.Trace.DBQueryOps(), cold.Trace.RedisRoundTrips(), cold.Trace.RedisOps())
	}
	waitForKeys(t, c, "authz:permission:*", "authz:token:*")

	for _, r := range []*harness.Replica{first, second} {
		r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)
		beforeMetrics := r.Metrics(c)
		hit := r.Permission(c, p.Slug, alice)
		if hit.Status != http.StatusOK {
			t.Fatalf("replica %d: %d %s", r.Index, hit.Status, hit.Body)
		}
		if got := hit.Trace.DBQueries(); got != 0 {
			t.Errorf("replica %d: a cache hit made %d database queries (%v), want 0", r.Index, got, hit.Trace.DBQueryOps())
		}
		if got := hit.Trace.RedisRoundTrips(); got != 0 {
			t.Errorf("replica %d: a cache hit made %d Redis round trips (%v), want 0", r.Index, got, hit.Trace.RedisOps())
		}

		// The same counts are in /metrics of the replica (the scrape itself is not counted).
		afterMetrics := r.Metrics(c)
		dbDelta := harness.MetricValue(afterMetrics, "licetum_db_queries_total") - harness.MetricValue(beforeMetrics, "licetum_db_queries_total")
		redisDelta := harness.MetricValue(afterMetrics, "licetum_redis_ops_total") - harness.MetricValue(beforeMetrics, "licetum_redis_ops_total")
		if int(dbDelta) != hit.Trace.DBQueries() || int(redisDelta) != hit.Trace.RedisRoundTrips() {
			t.Errorf("replica %d: /metrics counted db=%v redis=%v, the Trace db=%d redis=%d",
				r.Index, dbDelta, redisDelta, hit.Trace.DBQueries(), hit.Trace.RedisRoundTrips())
		}
	}

	// The counters of one replica do not count the requests of the other.
	metrics := first.Metrics(c)
	for _, name := range []string{"licetum_db_queries_total{", "licetum_db_query_seconds_count{", "licetum_redis_ops_total{", "licetum_redis_op_seconds_count{"} {
		if !strings.Contains(metrics, name) {
			t.Errorf("/metrics lacks %s", name)
		}
	}
}

// A Trace created outside the server keeps counting through it (the way a test or a middleware reads a request's cost).
func TestF0_2_ATraceOfTheCallerCountsTheWholeRequest(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	r.Permission(c, p.Slug, alice)
	waitForKeys(t, c, "authz:permission:*", "authz:token:*")

	// The engine itself, called directly: the type graph is two queries.
	ctx, trace := engine.WithTrace(context.Background())
	if _, err := r.App.LoadTypeGraph(ctx); err != nil {
		t.Fatal(err)
	}
	if trace.DBQueries() != 2 || trace.DBQueryOps()["select"] != 2 || trace.RedisRoundTrips() != 0 {
		t.Fatalf("LoadTypeGraph: db=%d %v redis=%d", trace.DBQueries(), trace.DBQueryOps(), trace.RedisRoundTrips())
	}
}
