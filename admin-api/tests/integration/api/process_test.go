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
			b.err = fmt.Errorf("build %s: %v\n%s", pkg, err, combined)
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

// runUntilReady starts the program and waits for GET url to answer 200; the output of the process is in the failure message.
func runUntilReady(t *testing.T, bin string, args, env []string, url string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case err := <-exited:
			t.Fatalf("the process ended before it was ready (%v):\n%s", err, out)
		default:
		}
		if resp, err := client.Get(url); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not ready within a minute:\n%s", url, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProcessPDPIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	public, ops := freeAddr(t), freeAddr(t)
	runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), []string{"pdp", "-addr", public},
		e.processEnv("PDP__PUBLIC_URL=http://"+public, "PDP__OPS_ADDR="+ops), "http://"+ops+"/readyz")
}

func TestProcessWorkerIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	addr := freeAddr(t)
	runUntilReady(t, buildBinary(t, coreModule, "./cmd/licetum"), []string{"worker", "-addr", addr},
		e.processEnv(), "http://"+addr+"/readyz")
}

func TestProcessAdminIsReadyWithoutRedis(t *testing.T) {
	e := setup(t)
	addr := freeAddr(t)
	runUntilReady(t, buildBinary(t, adminModule, "./cmd/licetum-admin"), []string{"admin", "-addr", addr},
		e.processEnv(
			"ADMIN_AUTH__ISSUER="+e.cfg.AdminAuth.Issuer, "ADMIN_AUTH__AUDIENCE="+e.cfg.AdminAuth.Audience,
			"ADMIN_AUTH__JWKS_URL="+e.cfg.AdminAuth.JWKSURL, "ADMIN_AUTH__SUBJECT_CLAIM="+e.cfg.AdminAuth.SubjectClaim,
		), "http://"+addr+"/readyz")
}
