//go:build chaos

package chaos_test

// F5 under faults: a check replica keeps serving when Redis is gone or read-only, serves from its copy of the policy
// while PostgreSQL is gone (and fails closed when it can no longer vouch for it), and a rolling restart loses no request.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	support "github.com/licetum/licetum/core/testsupport"
)

// outcome is what one request of a load came to.
type outcome struct {
	at     time.Duration // since the start of the load
	took   time.Duration
	status int
	err    error
}

func (o outcome) ok() bool { return o.err == nil && o.status == http.StatusOK }

// openLoop sends rps requests a second for d, whatever the answers take (at most maxInflight at once: a request the client
// has no room for counts as a failure, as the client could not send it). do(i) sends the i-th request.
func openLoop(rps int, d time.Duration, maxInflight int, do func(i int) (int, error)) []outcome {
	var (
		mu      sync.Mutex
		results []outcome
		wg      sync.WaitGroup
	)
	slots := make(chan struct{}, maxInflight)
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()
	begin := time.Now()
	stop := time.After(d)
	i := 0
	for {
		select {
		case <-stop:
			wg.Wait()
			return results
		case <-ticker.C:
			n := i
			i++
			select {
			case slots <- struct{}{}:
			default:
				mu.Lock()
				results = append(results, outcome{at: time.Since(begin), err: fmt.Errorf("the client has %d requests in flight", maxInflight)})
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				start := time.Now()
				status, err := do(n)
				o := outcome{at: start.Sub(begin), took: time.Since(start), status: status, err: err}
				mu.Lock()
				results = append(results, o)
				mu.Unlock()
			}()
		}
	}
}

func failures(rs []outcome) (n int) {
	for _, r := range rs {
		if !r.ok() {
			n++
		}
	}
	return n
}

func latencies(rs []outcome) []time.Duration {
	var out []time.Duration
	for _, r := range rs {
		if r.ok() {
			out = append(out, r.took)
		}
	}
	slices.Sort(out)
	return out
}

// between keeps the outcomes that began in [from, to) of the load.
func between(rs []outcome, from, to time.Duration) []outcome {
	var out []outcome
	for _, r := range rs {
		if r.at >= from && r.at < to {
			out = append(out, r)
		}
	}
	return out
}

// logCounter counts the records of level warning and above, by the second they were written in, and writes nothing.
type logCounter struct {
	mu     sync.Mutex
	bySec  map[int64]int
	byText map[string]int
}

func newLogCounter() *logCounter {
	return &logCounter{bySec: map[int64]int{}, byText: map[string]int{}}
}

func (l *logCounter) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}
func (l *logCounter) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCounter) WithGroup(string) slog.Handler      { return l }
func (l *logCounter) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bySec[r.Time.Unix()]++
	l.byText[r.Message]++
	return nil
}

// install makes the counter the default logger for the test.
func (l *logCounter) install(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(l))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// worstSecond is the most records of one second between two instants, and the total.
func (l *logCounter) between(from, to time.Time) (worst, total int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for sec, n := range l.bySec {
		if sec >= from.Unix() && sec <= to.Unix() {
			worst = max(worst, n)
			total += n
		}
	}
	return worst, total
}

func (l *logCounter) top() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for k, v := range l.byText {
		if v > 1 {
			out[k] = v
		}
	}
	return out
}

func queries(c *harness.Cluster, r *harness.Replica) float64 {
	return harness.MetricValue(r.Metrics(c), "licetum_db_queries_total")
}

// queriesBy is the statements counted so far by their first keyword (select, insert, ...).
func queriesBy(c *harness.Cluster, r *harness.Replica) map[string]float64 {
	out := map[string]float64{}
	for _, op := range []string{"select", "insert", "update", "delete", "with", "begin", "commit", "rollback", "other"} {
		out[op] = harness.MetricValue(r.Metrics(c), `licetum_db_queries_total{operation="`+op+`",result="ok"}`)
	}
	return out
}

// redisOutage is the scenario of F5-2 to F5-5: a replica under load while Redis is gone (or read-only) for outage, and
// after it. break and fix bring the fault about and end it. It returns nothing; it checks the criteria.
func redisOutage(t *testing.T, c *harness.Cluster, r *harness.Replica, p *harness.Policy, tokens []string, outage time.Duration, breakRedis, fixRedis func()) {
	t.Helper()
	const users = 30
	rps := 600 / loadDivisor
	path := "/service/" + p.Slug + "/permission"
	logs := newLogCounter()
	logs.install(t)

	// The tokens, the key set and the answers are in memory when the fault begins.
	want := map[string]string{}
	for _, token := range tokens[:users] {
		res := r.Do(c, http.MethodGet, path, token, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("warm request: %d %s", res.Status, res.Body)
		}
		want[token] = string(res.Body)
	}
	time.Sleep(time.Second)

	// About one request a second is for a subject nobody has asked for yet, in every phase: its token and its answer are in no
	// cache, so it is computed and written, which is what a fault of Redis hurts. Its answer is not compared, only given.
	coldEvery := rps
	var cold atomic.Int64
	do := func(i int) (int, error) {
		if i%coldEvery == coldEvery-1 {
			status, _, err := r.TryGet(c, path, c.UserToken("u"+strconv.FormatInt(1000+cold.Add(1), 10)), 5*time.Second)
			return status, err
		}
		token := tokens[i%users]
		status, body, err := r.TryGet(c, path, token, 5*time.Second)
		if err == nil && status == http.StatusOK && string(body) != want[token] {
			return 0, fmt.Errorf("the answer changed:\n%s\nwant\n%s", body, want[token])
		}
		return status, err
	}

	// Healthy: the figures the fault is compared with.
	const baseline = 10 * time.Second
	q0, jwks0 := queries(c, r), c.IdP.JWKSRequests()
	healthy := openLoop(rps, baseline, 400, do)
	baseQPS := (queries(c, r) - q0) / baseline.Seconds()
	t.Logf("healthy: %d requests, %d failed, p99 %v, %.1f statements/s", len(healthy), failures(healthy), percentile(latencies(healthy), 0.99), baseQPS)
	if f := failures(healthy); f > 0 {
		t.Fatalf("%d requests failed before the fault", f)
	}

	// The fault.
	breakRedis()
	faultStart := time.Now()
	q1, jwks1, by1 := queries(c, r), c.IdP.JWKSRequests(), queriesBy(c, r)
	during := openLoop(rps, outage, 400, do)
	if t.Failed() {
		return
	}
	by2 := queriesBy(c, r)
	for op, n := range by2 {
		if n-by1[op] > 0 {
			t.Logf("statements in the fault: %s %.0f", op, n-by1[op])
		}
	}
	faultEnd := time.Now()
	faultQPS := (queries(c, r) - q1) / outage.Seconds()
	f := failures(during)
	t.Logf("fault: %d requests, %d failed (%.2f%%), p99 %v, %.1f statements/s, %d key set requests (%d before)",
		len(during), f, 100*float64(f)/float64(len(during)), percentile(latencies(during), 0.99), faultQPS, c.IdP.JWKSRequests()-jwks1, jwks1-jwks0)

	// F5-2: at most one request in a hundred fails, and none for an answer that is in memory.
	if float64(f) > 0.01*float64(len(during)) {
		t.Errorf("F5-2: %d of %d requests failed (%.2f%%), want at most 1%%", f, len(during), 100*float64(f)/float64(len(during)))
	}
	for _, o := range during {
		if !o.ok() {
			t.Logf("first failure at %v: status %d, %v", o.at, o.status, o.err)
			break
		}
	}
	// F5-3: the database does not carry what Redis carried; the identity provider is not asked again; the log is quiet.
	if limit := 2 * max(baseQPS, 1); faultQPS > limit {
		t.Errorf("F5-3: %.1f statements/s in the fault, the baseline is %.1f/s: want at most %.1f/s", faultQPS, baseQPS, limit)
	}
	if n := c.IdP.JWKSRequests() - jwks1; n > 2 {
		t.Errorf("F5-3: %d key set requests during the fault, want no more than 2", n)
	}
	worst, total := logs.between(faultStart, faultEnd)
	t.Logf("log records of level warning and above: %d in the fault, at most %d in a second; repeated: %v", total, worst, logs.top())
	if worst > 5 {
		t.Errorf("F5-3: %d log lines of level warning and above in one second, want at most 5", worst)
	}

	// F5-4: when Redis is back the 99th percentile returns to the budget within thirty seconds.
	fixRedis()
	const recovery = 30 * time.Second
	after := openLoop(rps, recovery, 400, do)
	budget := (20*time.Millisecond + hotTimeout) * slowdown
	recovered := recovery
	for second := recovery - time.Second; second >= 0; second -= time.Second {
		window := latencies(between(after, second, second+time.Second))
		if len(window) == 0 || percentile(window, 0.99) > budget {
			break
		}
		recovered = second
	}
	t.Logf("recovery: %d requests, %d failed; the 99th percentile is within %v from %v on", len(after), failures(after), budget, recovered)
	if recovered > recovery-5*time.Second {
		t.Errorf("F5-4: the 99th percentile is not within the budget of %v in the last seconds after Redis came back (within it from %v)", budget, recovered)
	}
	if recovered > 30*time.Second {
		t.Errorf("F5-4: the budget was met only %v after Redis came back", recovered)
	}
	if f := failures(after); float64(f) > 0.01*float64(len(after)) {
		t.Errorf("%d of %d requests failed after Redis came back", f, len(after))
	}
}

func outageLength(def time.Duration) time.Duration {
	return outageLengthOf("LICETUM_CHAOS_REDIS_OUTAGE", def)
}

func outageLengthOf(env string, def time.Duration) time.Duration {
	if v := os.Getenv(env); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// F5-2, F5-3, F5-4: Redis gone for a minute at 600 requests a second. LICETUM_CHAOS_REDIS_OUTAGE shortens it.
func TestF5_2_RedisGoneForAMinuteUnderLoad(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(), harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, 30)
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", tokens[0])

	redisOutage(t, c, r, p, tokens, outageLength(60*time.Second), c.RedisFault.Cut, c.RedisFault.Reset)
}

// F5-2: the same when Redis swallows the traffic and every command waits for its timeout, until it is skipped.
func TestF5_2_RedisBlackHoledUnderLoad(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(), harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, 30)
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", tokens[0])

	redisOutage(t, c, r, p, tokens, outageLength(60*time.Second)/2, c.RedisFault.BlackHole, c.RedisFault.Reset)
}

// F5-5: a Redis in READONLY mode refuses the writes and keeps answering the reads: the replica behaves as with Redis gone.
func TestF5_5_ReadOnlyRedisUnderLoad(t *testing.T) {
	ctx := context.Background()
	uri, stop, err := support.StartSpareRedis(ctx)
	if err != nil {
		t.Skipf("no Redis to make read-only (set %s or run Docker): %v", support.EnvSpareRedisURI, err)
	}
	t.Cleanup(stop)
	opts, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(opts)
	t.Cleanup(func() { _ = admin.Close() })

	c := harness.StartReplicas(t, infra, 1, harness.WithReplicaRedis(uri), harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, 30)
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", tokens[0])

	redisOutage(t, c, r, p, tokens, outageLength(60*time.Second)/3,
		func() {
			if err := admin.Do(ctx, "REPLICAOF", "127.0.0.1", "1").Err(); err != nil {
				t.Fatalf("REPLICAOF: %v", err)
			}
		},
		func() {
			if err := admin.Do(ctx, "REPLICAOF", "NO", "ONE").Err(); err != nil {
				t.Fatalf("REPLICAOF NO ONE: %v", err)
			}
		})
}

// errorCodeOf is extra.code of an error body.
func errorCodeOf(body []byte) string {
	var e struct {
		Extra struct {
			Code string `json:"code"`
		} `json:"extra"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Extra.Code
}

// F5-6: PostgreSQL gone: the replica answers from its copy of the policy until it has not confirmed it for
// POLICY__MAX_STALENESS, then fails closed with 503 policy_unavailable, and recovers by itself when PostgreSQL is back.
func TestF5_6_PostgreSQLGoneTheReplicaAnswersFromItsCopyThenFailsClosed(t *testing.T) {
	const staleness = 5 * time.Second
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(), harness.WithHotRedis(hotTimeout),
		harness.WithConfig(func(cfg *config.Config) { cfg.Policy.MaxStaleness = staleness }))
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	path := "/service/" + p.Slug + "/permission"
	r.Warm(c, http.MethodGet, path, alice)
	want := string(r.Do(c, http.MethodGet, path, alice, nil).Body)
	logs := newLogCounter()
	logs.install(t)

	c.PostgresFault.Cut()
	cutAt := time.Now()
	type seen struct {
		at     time.Duration
		status int
		code   string
		body   string
	}
	var timeline []seen
	for time.Since(cutAt) < staleness+4*time.Second {
		start := time.Now()
		status, body, err := r.TryGet(c, path, alice, 3*time.Second)
		if err != nil {
			t.Fatalf("at %v: %v", time.Since(cutAt), err)
		}
		timeline = append(timeline, seen{at: start.Sub(cutAt), status: status, code: errorCodeOf(body), body: string(body)})
		time.Sleep(25 * time.Millisecond)
	}
	var firstRefusal time.Duration = -1
	for _, s := range timeline {
		switch {
		case s.status == http.StatusOK && firstRefusal >= 0:
			t.Fatalf("at %v an answer was given after the replica had refused one (at %v)", s.at, firstRefusal)
		case s.status == http.StatusOK:
			if s.body != want {
				t.Fatalf("at %v the answer changed:\n%s\nwant\n%s", s.at, s.body, want)
			}
		case s.status == http.StatusServiceUnavailable && s.code == "policy_unavailable":
			if firstRefusal < 0 {
				firstRefusal = s.at
			}
		default:
			t.Fatalf("at %v: status %d %s, want 200 or 503 policy_unavailable", s.at, s.status, s.body)
		}
	}
	t.Logf("%d requests while PostgreSQL was gone; the first refusal at %v (POLICY__MAX_STALENESS is %v)", len(timeline), firstRefusal, staleness)
	if firstRefusal < 0 {
		t.Fatalf("no request was refused in %v with POLICY__MAX_STALENESS of %v", staleness+4*time.Second, staleness)
	}
	// The copy was confirmed up to a poll or two before the cut, so the refusal comes a little before the staleness has
	// passed since the cut, and not long after it.
	if firstRefusal < staleness-time.Second || firstRefusal > staleness+3*time.Second {
		t.Errorf("the first refusal came at %v, want about %v after the cut", firstRefusal, staleness)
	}
	if res := r.Do(c, http.MethodGet, "/readyz", "", nil); res.Status != http.StatusServiceUnavailable {
		t.Errorf("/readyz with PostgreSQL gone: %d", res.Status)
	}
	if res := r.Do(c, http.MethodGet, "/healthz", "", nil); res.Status != http.StatusOK {
		t.Errorf("/healthz with PostgreSQL gone: %d", res.Status)
	}
	if age := harness.MetricValue(r.Metrics(c), "licetum_snapshot_age_seconds"); age < staleness.Seconds() {
		t.Errorf("licetum_snapshot_age_seconds is %v after %v without PostgreSQL", age, time.Since(cutAt))
	}
	if d := harness.MetricValue(r.Metrics(c), `licetum_degraded{component="postgres"}`); d != 1 {
		t.Errorf(`licetum_degraded{component="postgres"} is %v, want 1`, d)
	}
	if worst, total := logs.between(cutAt, time.Now()); worst > 5 {
		t.Errorf("%d log lines of level warning and above in one second (%d in all), want at most 5; repeated: %v", worst, total, logs.top())
	}

	// It recovers without a restart.
	c.PostgresFault.Reset()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, body, err := r.TryGet(c, path, alice, 3*time.Second)
		if err == nil && status == http.StatusOK && string(body) == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after PostgreSQL came back: status %d, %v, %s", status, err, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	eventuallyOK(t, 10*time.Second, "/readyz is 200 again", func() bool {
		return r.Do(c, http.MethodGet, "/readyz", "", nil).Status == http.StatusOK
	})
}

// F5-6: without a limit (the default) the replica answers from its copy for as long as PostgreSQL is gone.
func TestF5_6_WithoutALimitTheReplicaKeepsAnsweringFromItsCopy(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(), harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	path := "/service/" + p.Slug + "/permission"
	r.Warm(c, http.MethodGet, path, alice)
	want := string(r.Do(c, http.MethodGet, path, alice, nil).Body)

	c.PostgresFault.Cut()
	// F5-6: thirty seconds, well beyond the 2 s after which a view is no longer trusted unread, and the 10 s of a cached
	// answer. LICETUM_CHAOS_POSTGRES_OUTAGE shortens it.
	outage := outageLengthOf("LICETUM_CHAOS_POSTGRES_OUTAGE", 30*time.Second)
	until := time.Now().Add(outage)
	n := 0
	for time.Now().Before(until) {
		status, body, err := r.TryGet(c, path, alice, 3*time.Second)
		if err != nil || status != http.StatusOK || string(body) != want {
			t.Fatalf("after %d answers, with PostgreSQL gone: status %d, %v, %s", n, status, err, body)
		}
		n++
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("%d answers in %v without PostgreSQL", n, outage)
}

func eventuallyOK(t *testing.T, within time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", within, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// F5-7: a rolling restart of every replica at 600 requests a second, the way a balancer and an orchestrator do it: a pod
// says it is not ready, serves while the balancer notices, stops listening, and a new pod takes its place. No request
// meets a reset connection or a 5xx.
func TestF5_7_ARollingRestartLosesNoRequest(t *testing.T) {
	const (
		replicas = 3
		drain    = 1500 * time.Millisecond // the balancer probes every 250 ms
		probe    = 250 * time.Millisecond
	)
	rps := 600 / loadDivisor
	c := harness.StartReplicas(t, infra, replicas, harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, 30)
	path := "/service/" + p.Slug + "/permission"
	for _, r := range c.Replicas {
		r.Warm(c, http.MethodGet, path, tokens[0])
	}
	for _, token := range tokens { // warm the answers on every replica: what is measured is the restart
		for _, r := range c.Replicas {
			r.Do(c, http.MethodGet, path, token, nil)
		}
	}

	// The balancer: the replicas that answered /readyz with 200 at the last probe, over keep-alive connections.
	var (
		mu    sync.Mutex
		ready = map[string]bool{}
		urls  []string
	)
	for _, r := range c.Replicas {
		urls = append(urls, r.Server.URL)
		ready[r.Server.URL] = true
	}
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64, MaxConnsPerHost: 0}, Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	probing, stopProbing := context.WithCancel(context.Background())
	var probes sync.WaitGroup
	probes.Add(1)
	go func() {
		defer probes.Done()
		ticker := time.NewTicker(probe)
		defer ticker.Stop()
		for {
			select {
			case <-probing.Done():
				return
			case <-ticker.C:
			}
			mu.Lock()
			all := slices.Clone(urls)
			mu.Unlock()
			for _, u := range all {
				ctx, cancel := context.WithTimeout(probing, time.Second)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u+"/readyz", nil)
				resp, err := client.Do(req)
				ok := err == nil && resp.StatusCode == http.StatusOK
				if resp != nil {
					_ = resp.Body.Close()
				}
				cancel()
				mu.Lock()
				ready[u] = ok
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { stopProbing(); probes.Wait() })

	var (
		sent     atomic.Int64
		resets   atomic.Int64
		fiveXX   atomic.Int64
		noTarget atomic.Int64
		errText  sync.Map
	)
	do := func(i int) (int, error) {
		mu.Lock()
		var up []string
		for _, u := range urls {
			if ready[u] {
				up = append(up, u)
			}
		}
		mu.Unlock()
		if len(up) == 0 {
			noTarget.Add(1)
			return 0, fmt.Errorf("no replica is ready")
		}
		sent.Add(1)
		target := up[i%len(up)]
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target+path, nil)
		req.Header.Set("Authorization", "Bearer "+tokens[i%30])
		resp, err := client.Do(req)
		if err != nil {
			resets.Add(1)
			errText.Store(err.Error(), true)
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body) // read to the end, so that the connection is kept for the next request
		if resp.StatusCode >= 500 {
			fiveXX.Add(1)
		}
		return resp.StatusCode, nil
	}

	var results []outcome
	done := make(chan struct{})
	go func() {
		defer close(done)
		results = openLoop(rps, 4*time.Second+replicas*(drain+2*time.Second), 400, do)
	}()

	time.Sleep(2 * time.Second)
	for i := range replicas {
		old := c.Replicas[i]
		replacement := c.AddReplica()
		replacement.Warm(c, http.MethodGet, path, tokens[0])
		for _, token := range tokens {
			replacement.Do(c, http.MethodGet, path, token, nil)
		}
		mu.Lock()
		urls = append(urls, replacement.Server.URL)
		ready[replacement.Server.URL] = false // the balancer admits it at its next probe
		mu.Unlock()
		time.Sleep(2 * probe)

		start := time.Now()
		if err := old.Stop(drain, 10*time.Second); err != nil {
			t.Errorf("replica %d did not stop cleanly: %v", i, err)
		}
		t.Logf("replica %d stopped after %v", i, time.Since(start))
		// The balancer is not told: it finds out by its probes, as long as which it goes on sending requests to the replica.
	}
	<-done

	var texts []string
	errText.Range(func(k, _ any) bool { texts = append(texts, k.(string)); return true })
	t.Logf("%d requests, %d failed; connection errors %d, 5xx %d, no replica ready %d; %v", len(results), failures(results), resets.Load(), fiveXX.Load(), noTarget.Load(), texts)
	if resets.Load() > 0 || fiveXX.Load() > 0 {
		t.Errorf("F5-7: %d connection errors and %d 5xx during the rolling restart, want none", resets.Load(), fiveXX.Load())
	}
	if f := failures(results); f > 0 {
		t.Errorf("F5-7: %d of %d requests failed", f, len(results))
	}
}
