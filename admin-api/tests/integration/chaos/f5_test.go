//go:build chaos

package chaos_test

// F5 under faults: a check replica serves from its copy of the policy while PostgreSQL is gone (and fails closed when it can
// no longer vouch for it), and a rolling restart loses no request.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
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

func outageLengthOf(env string, def time.Duration) time.Duration {
	if v := os.Getenv(env); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// errorCodeOf is the code of an error body: extra.code of the admin API's, or, for an AuthZEN route, whose error body is the
// message alone (a JSON string), the code of the one message these tests wait for.
func errorCodeOf(body []byte) string {
	var e struct {
		Extra struct {
			Code string `json:"code"`
		} `json:"extra"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Extra.Code != "" {
		return e.Extra.Code
	}
	var message string
	if json.Unmarshal(body, &message) == nil && strings.Contains(message, "policy store cannot be reached") {
		return "policy_unavailable"
	}
	return ""
}

// F5-6: PostgreSQL gone: the replica answers from its copy of the policy until it has not confirmed it for
// POLICY__MAX_STALENESS, then fails closed with 503 policy_unavailable, and recovers by itself when PostgreSQL is back.
func TestF5_6_PostgreSQLGoneTheReplicaAnswersFromItsCopyThenFailsClosed(t *testing.T) {
	const staleness = 5 * time.Second
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(),
		harness.WithConfig(func(cfg *config.Config) { cfg.Policy.MaxStaleness = staleness }))
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	asked := harness.Branches(p.Slug, alice)
	r.Warm(c, asked, alice)
	want := string(r.Send(c, asked, alice).Body)
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
		status, body, err := r.Try(c, asked, alice, 3*time.Second)
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
		status, body, err := r.Try(c, asked, alice, 3*time.Second)
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
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	asked := harness.Branches(p.Slug, alice)
	r.Warm(c, asked, alice)
	want := string(r.Send(c, asked, alice).Body)

	c.PostgresFault.Cut()
	// F5-6: thirty seconds, well beyond the 2 s after which a view is no longer trusted unread, and the 10 s of a cached
	// answer. LICETUM_CHAOS_POSTGRES_OUTAGE shortens it.
	outage := outageLengthOf("LICETUM_CHAOS_POSTGRES_OUTAGE", 30*time.Second)
	until := time.Now().Add(outage)
	n := 0
	for time.Now().Before(until) {
		status, body, err := r.Try(c, asked, alice, 3*time.Second)
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
	c := harness.StartReplicas(t, infra, replicas)
	p := c.SeedPolicy("org")
	tokens := everyone(t, c, p, 30)
	for _, r := range c.Replicas {
		r.Warm(c, harness.Branches(p.Slug, tokens[0]), tokens[0])
	}
	for _, token := range tokens { // warm the answers on every replica: what is measured is the restart
		for _, r := range c.Replicas {
			r.Send(c, harness.Branches(p.Slug, token), token)
		}
	}

	// The balancer: the replicas that answered /readyz with 200 at the last probe, over keep-alive connections. Traffic goes to
	// the public port of a replica; its probe goes to the operations listener, which is where /readyz is.
	var (
		mu      sync.Mutex
		ready   = map[string]bool{}
		urls    []string
		probeOf = map[string]string{} // public URL -> operations URL
	)
	for _, r := range c.Replicas {
		urls = append(urls, r.Server.URL)
		ready[r.Server.URL] = true
		probeOf[r.Server.URL] = r.Ops.URL
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
				mu.Lock()
				ops := probeOf[u]
				mu.Unlock()
				ctx, cancel := context.WithTimeout(probing, time.Second)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ops+"/readyz", nil)
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
		token := tokens[i%30]
		req, err := c.HTTPRequest(context.Background(), target, harness.Branches(p.Slug, token), token)
		if err != nil {
			return 0, err
		}
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
		replacement.Warm(c, harness.Branches(p.Slug, tokens[0]), tokens[0])
		for _, token := range tokens {
			replacement.Send(c, harness.Branches(p.Slug, token), token)
		}
		mu.Lock()
		urls = append(urls, replacement.Server.URL)
		probeOf[replacement.Server.URL] = replacement.Ops.URL
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
