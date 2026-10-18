//go:build multiregion

package multiregion_test

// The multi-region environment (compose.yml) as the tests see it: the three regions with their check replicas, the administration
// of region A and B, the proxy that carries the latency of the links, and the identity provider, which is this process.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	support "github.com/licetum/licetum/core/testsupport"
)

type region struct {
	name   string
	checks []string // the base URLs of the check replicas
	admin  string   // the admin API of the region ("" when it has none)
	proxy  string   // the suffix of its proxies in toxiproxy.json (b, c); "" for the region of the primary
}

var (
	regionA = region{name: "a", checks: []string{"http://127.0.0.1:18011", "http://127.0.0.1:18012"}, admin: "http://127.0.0.1:18000"}
	regionB = region{name: "b", checks: []string{"http://127.0.0.1:18021", "http://127.0.0.1:18022"}, admin: "http://127.0.0.1:18010", proxy: "b"}
	regionC = region{name: "c", checks: []string{"http://127.0.0.1:18031", "http://127.0.0.1:18032"}, proxy: "c"}
	regions = []region{regionA, regionB, regionC}
)

const (
	toxiproxyAPI = "http://127.0.0.1:8474"
	composeFile  = "compose.yml"
)

var (
	idp         *support.IdP
	adminToken  string
	policy      seeded
	httpClient  = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 128}}
	keepStack   = os.Getenv("MR_KEEP") != ""
	externalEnv = os.Getenv("MR_EXTERNAL") != "" // the stack is already up (and its IdP address is MR_IDP_URL)
)

// mainTB lets the identity provider live as long as the test binary: it is started before any test.
type mainTB struct{ testing.TB }

func (mainTB) Helper()                   {}
func (mainTB) Cleanup(func())            {}
func (mainTB) Fatalf(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

func TestMain(m *testing.M) {
	idp = support.NewIdPForContainers(mainTB{}, "host.docker.internal")
	adminToken = idp.Token(jwt.MapClaims{"preferred_username": "admin", "sub": "admin-sub"})
	if !externalEnv {
		os.Setenv("MR_IDP_URL", idp.URL())
		os.Setenv("MR_MAX_REPLICA_LAG", "5s")
		os.Setenv("MR_MAX_STALENESS", "20s")
		if err := compose("up", "-d"); err != nil {
			fmt.Fprintln(os.Stderr, "compose up:", err)
			os.Exit(1)
		}
	}
	code := 1
	func() {
		defer func() {
			if !keepStack && !externalEnv {
				_ = compose("down", "-v", "--remove-orphans")
			}
		}()
		if err := waitForStack(5 * time.Minute); err != nil {
			fmt.Fprintln(os.Stderr, "the stack did not come up:", err)
			_ = compose("logs", "--tail=50")
			return
		}
		var err error
		if policy, err = seed(); err != nil {
			fmt.Fprintln(os.Stderr, "seed:", err)
			return
		}
		code = m.Run()
	}()
	os.Exit(code)
}

func compose(args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose", "-f", composeFile}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func composeOutput(args ...string) (string, error) {
	out, err := exec.Command("docker", append([]string{"compose", "-f", composeFile}, args...)...).CombinedOutput()
	return string(out), err
}

func waitForStack(limit time.Duration) error {
	deadline := time.Now().Add(limit)
	var pending []string
	for time.Now().Before(deadline) {
		pending = pending[:0]
		urls := []string{regionA.admin}
		for _, r := range regions {
			urls = append(urls, r.checks...)
		}
		for _, u := range urls {
			resp, err := httpClient.Get(u + "/readyz")
			if err != nil || resp.StatusCode != http.StatusOK {
				pending = append(pending, u)
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
		if len(pending) == 0 {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("not ready after %v: %v", limit, pending)
}

// ----- the admin API

func adminDo(base, method, path string, body any) (int, []byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Header, nil
}

func mustAdmin(t testing.TB, base, method, path string, body any) (map[string]any, int64) {
	t.Helper()
	status, raw, header, err := adminDo(base, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %v %s", method, path, status, err, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	rev, _ := strconv.ParseInt(header.Get("X-Authz-Revision"), 10, 64)
	return out, rev
}

type seeded struct{ slug, serviceID, branchType string }

// seed registers the service "org" with its type "branch", whose source only region A reaches (and only the worker of A
// synchronizes: the synchronizer lists the region a), and a rule that gives everybody named u<number> read.
func seed() (seeded, error) {
	t := &fatalTB{}
	svc, _ := mustAdmin(t, regionA.admin, "POST", "/service", map[string]any{
		"name": "org", "slug": "org", "url": idp.AppURL(), "icon": idp.IconURL(),
		"authorization_url": idp.AuthorizationURL(), "token_url": idp.TokenURL(), "jwks_url": idp.JWKSURL(), "issuer": idp.Issuer(),
		"client_id": "org", "client_secret": "s",
	})
	id := svc["id"].(string)
	typ, _ := mustAdmin(t, regionA.admin, "POST", "/service/"+id+"/type_resource", map[string]any{"name": "branch"})
	branch := typ["id"].(string)
	mustAdmin(t, regionA.admin, "POST", "/service/"+id+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": "http://source/src", "cache_ttl": 0},
		"probe_resource_id": "b1",
		"regions":           []string{"a"},
	})
	mustAdmin(t, regionA.admin, "POST", "/assignment_rule", map[string]any{
		"type_resource_id": branch, "access": []string{"read"},
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^u[0-9]+$"}},
	})
	if t.err != nil {
		return seeded{}, t.err
	}
	// The replicas of B and C learn the policy from the stream, so the service is there when the replica has replayed the
	// seed: the scenarios measure what they mean from the first request, not what the replication does on the way.
	for _, r := range []region{regionB, regionC} {
		for _, base := range r.checks {
			if err := waitForService(base, "org", 2*time.Minute); err != nil {
				return seeded{}, err
			}
		}
	}
	return seeded{slug: "org", serviceID: id, branchType: branch}, nil
}

// waitForService asks a check replica until it answers the service with 200, for at most limit.
func waitForService(base, slug string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		req, _ := http.NewRequest(http.MethodGet, base+"/service/"+slug+"/permission", nil)
		req.Header.Set("Authorization", "Bearer "+token("u1"))
		resp, err := httpClient.Do(req)
		status := 0
		var body []byte
		if err == nil {
			status = resp.StatusCode
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		if status == http.StatusOK {
			return nil
		}
		if time.Now().After(deadline) {
			last := err
			if last == nil {
				last = fmt.Errorf("status %d: %s", status, body)
			}
			return fmt.Errorf("%s does not know the service %q after %v: %w", base, slug, limit, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type fatalTB struct {
	testing.TB
	err error
}

func (f *fatalTB) Helper() {}
func (f *fatalTB) Fatalf(format string, args ...any) {
	if f.err == nil {
		f.err = fmt.Errorf(format, args...)
	}
	panic(f.err)
}

// ----- the check API

func token(user string) string {
	return idp.Token(jwt.MapClaims{"preferred_username": user, "sub": user + "-sub"})
}

type answer struct {
	status int
	body   string
	took   time.Duration
}

func (a answer) hasAccess() bool {
	return a.status == http.StatusOK && strings.Contains(a.body, `"read"`)
}

// permission asks a check replica; atLeast, when positive, is the revision the answer must reflect.
func permission(base, user string, atLeast int64) answer {
	u := base + "/service/" + policy.slug + "/permission"
	if atLeast > 0 {
		u += "?at_least_as_fresh=" + strconv.FormatInt(atLeast, 10)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+token(user))
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return answer{status: 0, body: err.Error(), took: time.Since(start)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return answer{status: resp.StatusCode, body: string(raw), took: time.Since(start)}
}

func ready(base string) int {
	resp, err := httpClient.Get(base + "/readyz")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func eventually(t *testing.T, within time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", within, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func p99(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)*99/100]
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// ----- the links (toxiproxy)

func proxyCall(method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, toxiproxyAPI+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("toxiproxy %s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return nil
}

// setLink gives the link between the primary and a region (the path of the writes, and the replication stream when
// replication is true) a round trip of rtt: half of it in each direction. rtt 0 removes the latency.
func setLink(t testing.TB, r region, replication bool, rtt time.Duration) {
	t.Helper()
	name := "primary-" + r.proxy
	if replication {
		name = "replication-" + r.proxy
	}
	for _, dir := range []string{"upstream", "downstream"} {
		toxic := "latency-" + dir
		_ = proxyCall(http.MethodDelete, "/proxies/"+name+"/toxics/"+toxic, nil)
		if rtt > 0 {
			err := proxyCall(http.MethodPost, "/proxies/"+name+"/toxics", map[string]any{
				"name": toxic, "type": "latency", "stream": dir, "toxicity": 1.0,
				"attributes": map[string]any{"latency": int(rtt.Milliseconds() / 2), "jitter": 0},
			})
			if err != nil {
				t.Fatalf("%v", err)
			}
		}
	}
}

// cutLink disables (cut) or enables the proxies of a region; the replica itself keeps running.
func cutLink(t testing.TB, r region, cut bool) {
	t.Helper()
	for _, name := range []string{"primary-" + r.proxy, "replication-" + r.proxy} {
		if err := proxyCall(http.MethodPost, "/proxies/"+name, map[string]any{"enabled": !cut}); err != nil {
			t.Fatalf("%v", err)
		}
	}
}

func resetLinks(t testing.TB) {
	for _, r := range []region{regionB, regionC} {
		setLink(t, r, false, 0)
		setLink(t, r, true, 0)
		cutLink(t, r, false)
	}
}
