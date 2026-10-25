//go:build integration

package api_test

// The processes need no Redis: `licetum pdp`, `licetum worker` and `licetum-admin admin` are built, started with the
// settings their role needs and no REDIS__* in the environment, and must become ready.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	coreModule  = "../../../../core"
	adminModule = "../../.."
)

// binaries are the programs built by buildBinary, once per run.
var binaries sync.Map // module dir + "\x00" + package -> *builtBinary

type builtBinary struct {
	once sync.Once
	path string
	err  error
}

// buildBinary builds the main package pkg of the module in dir (relative to this directory) and returns the path of the
// binary. Each program is built once per test run.
func buildBinary(t *testing.T, dir, pkg string) string {
	t.Helper()
	v, _ := binaries.LoadOrStore(dir+"\x00"+pkg, &builtBinary{})
	b := v.(*builtBinary)
	b.once.Do(func() {
		out, err := os.MkdirTemp("", "licetum-bin")
		if err != nil {
			b.err = err
			return
		}
		b.path = filepath.Join(out, filepath.Base(pkg))
		cmd := exec.Command("go", "build", "-o", b.path, pkg)
		cmd.Dir = dir
		if combined, err := cmd.CombinedOutput(); err != nil {
			b.err = fmt.Errorf("build %s: %w\n%s", pkg, err, combined)
		}
	})
	if b.err != nil {
		t.Fatal(b.err)
	}
	return b.path
}

// freeAddr is a loopback address nothing listens on at the moment.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// withoutRedis is the environment of this test process with every REDIS__ variable taken out, so that a process that
// still asks for Redis fails whatever the machine has set.
func withoutRedis(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "REDIS__") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// processEnv is what the three processes need from the test database: the connection, the schema of the synchronization
// engine and, for the PDP and the admin API, the settings of their role.
func (e *env) processEnv(extra ...string) []string {
	settings := []string{"DB__URI=" + e.cfg.DB.URI, "SYNC__DBOS_SCHEMA=" + e.cfg.Sync.DBOSSchema}
	if e.cfg.DB.PgBouncer {
		settings = append(settings, "DB__PGBOUNCER=true")
	}
	if e.cfg.DB.MigrateURI != "" {
		settings = append(settings, "DB__MIGRATE_URI="+e.cfg.DB.MigrateURI)
	}
	return withoutRedis(append(settings, extra...)...)
}

// started is the outcome of one start of a program.
type started struct {
	out   *lockedBuffer
	ready bool  // GET url answered 200
	ended error // the process ended before it was ready (only when ready is false)
}

// startOnce starts the program and waits for GET url to answer 200, for the process to end, or for a minute to pass (which
// fails the test). The process is killed when the test ends, and the wait for it is bounded: the output is a buffer, so
// Wait would otherwise stay on the pipes for as long as anything that inherited them lives.
func startOnce(t *testing.T, bin string, args, env []string, url string) started {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// done is closed when the process has been waited for, so that the loop below and the cleanup can both wait on it (a
	// value sent on a channel would be received by one of them only).
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case <-done:
			return started{out: out, ended: waitErr}
		default:
		}
		if resp, err := client.Get(url); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return started{out: out, ready: true}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not ready within a minute:\n%s", url, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runUntilReady starts the program that prepare describes and waits for it to become ready; the output of the process is in the
// failure message and is returned, for a test that reads what the process logged while it started. prepare picks the
// addresses, so that a start can be repeated on others: a free address is free only until the program binds it, and a
// program that finds it taken ("address already in use") is started again, up to three times, whatever else ended it is a
// failure.
func runUntilReady(t *testing.T, bin string, prepare func() (args, env []string, url string)) *lockedBuffer {
	t.Helper()
	const attempts = 3
	for attempt := 1; ; attempt++ {
		args, env, url := prepare()
		s := startOnce(t, bin, args, env, url)
		if s.ready {
			return s.out
		}
		if attempt < attempts && strings.Contains(s.out.String(), "address already in use") {
			t.Logf("attempt %d: an address was taken before the program bound it, starting again:\n%s", attempt, s.out)
			continue
		}
		t.Fatalf("the process ended before it was ready (%v):\n%s", s.ended, s.out)
	}
}

func TestProcessPDPIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), func() ([]string, []string, string) {
		public, ops := freeAddr(t), freeAddr(t)
		return []string{"pdp", "-addr", public},
			e.processEnv("PDP__PUBLIC_URL=http://"+public, "PDP__OPS_ADDR="+ops), "http://" + ops + "/readyz"
	})
}

func TestProcessWorkerIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), func() ([]string, []string, string) {
		addr := freeAddr(t)
		return []string{"worker", "-addr", addr}, e.processEnv(), "http://" + addr + "/readyz"
	})
}

func TestProcessAdminIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	runUntilReady(t, buildBinary(t, adminModule, "./cmd/licetum-admin"), func() ([]string, []string, string) {
		addr := freeAddr(t)
		return []string{"admin", "-addr", addr}, e.processEnv(e.adminAuthEnv()...), "http://" + addr + "/readyz"
	})
}

// adminAuthEnv is the sign-in configuration the admin API needs to start.
func (e *env) adminAuthEnv() []string {
	return []string{
		"ADMIN_AUTH__ISSUER=" + e.cfg.AdminAuth.Issuer, "ADMIN_AUTH__AUDIENCE=" + e.cfg.AdminAuth.Audience,
		"ADMIN_AUTH__JWKS_URL=" + e.cfg.AdminAuth.JWKSURL, "ADMIN_AUTH__SUBJECT_CLAIM=" + e.cfg.AdminAuth.SubjectClaim,
	}
}

// removedSettings are the settings that were removed with Redis, the read replica and the table synchronization engine.
var removedSettings = []string{
	"REDIS__URI", "REDIS__HOT_TIMEOUT",
	"DB__READ_URI", "DB__MAX_REPLICA_LAG", "DB__LAG_ACTION", "DB__LAG_INTERVAL",
	"SYNC__ENGINE", "SYNC__FETCH_TTL", "SYNC__LOCK_TTL", "SYNC__LOCK_WAIT", "WORKER__REGION",
}

// removedEnv sets every removed setting to a value that would have been refused or obeyed before they were removed.
func removedEnv() []string {
	var env []string
	for _, name := range removedSettings {
		env = append(env, name+"=x")
	}
	return env
}

// assertOneWarningEach checks that the output of a process that started with removedEnv carries exactly one warning per removed
// setting: the processes that run for a long time (pdp, admin, worker) call the warning at their start, and a start that does
// not call it would be silent for the operator who still sets the variables.
func assertOneWarningEach(t *testing.T, output string) {
	t.Helper()
	for _, name := range removedSettings {
		re := regexp.MustCompile(`the setting no longer has an effect.*"?setting"?[=:]"?` + name + `\b`)
		if n := len(re.FindAllString(output, -1)); n != 1 {
			t.Errorf("%s: %d warnings, want 1; the output of the process:\n%s", name, n, output)
		}
	}
}

// The three processes start with every removed setting set, and each of them logs one warning per setting.
func TestProcessesStartWithTheRemovedSettingsAndWarnOnceForEach(t *testing.T) {
	e := setup(t)
	t.Run("pdp", func(t *testing.T) {
		out := runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), func() ([]string, []string, string) {
			public, ops := freeAddr(t), freeAddr(t)
			return []string{"pdp", "-addr", public},
				e.processEnv(append(removedEnv(), "PDP__PUBLIC_URL=http://"+public, "PDP__OPS_ADDR="+ops)...), "http://" + ops + "/readyz"
		})
		assertOneWarningEach(t, out.String())
	})
	t.Run("worker", func(t *testing.T) {
		out := runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), func() ([]string, []string, string) {
			addr := freeAddr(t)
			return []string{"worker", "-addr", addr}, e.processEnv(removedEnv()...), "http://" + addr + "/readyz"
		})
		assertOneWarningEach(t, out.String())
	})
	t.Run("admin", func(t *testing.T) {
		out := runUntilReady(t, buildBinary(t, adminModule, "./cmd/licetum-admin"), func() ([]string, []string, string) {
			addr := freeAddr(t)
			return []string{"admin", "-addr", addr}, e.processEnv(append(removedEnv(), e.adminAuthEnv()...)...), "http://" + addr + "/readyz"
		})
		assertOneWarningEach(t, out.String())
	})
}
