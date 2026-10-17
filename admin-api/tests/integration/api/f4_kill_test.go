//go:build integration

package api_test

// F4-2 with a real process: `kill -9` of a process that records delegated calls in async mode. The test binary starts
// itself as the child (the same pattern as the stdlib's os/exec tests), so no service or identity provider is needed.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
)

const (
	auditChildEnv  = "LICETUM_F4_AUDIT_CHILD"
	auditCallsEach = time.Millisecond // 1000 calls a second: about one flush window of 500 records every 500 ms
)

// TestF4_2_AuditChild is the child process of TestF4_2_AsyncAuditAfterKillNineLosesAtMostTheLastWindows. It does nothing
// when the test binary runs on its own.
func TestF4_2_AuditChild(t *testing.T) {
	org := os.Getenv(auditChildEnv)
	if org == "" {
		t.Skip("child of TestF4_2_AsyncAuditAfterKillNineLosesAtMostTheLastWindows")
	}
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := redisx.Connect(ctx, cfg.Redis.URI)
	if err != nil {
		t.Fatal(err)
	}
	app := engine.New(pool, rdb, cfg)
	serviceID := uuid.MustParse(org)
	for i := 0; ; i++ {
		err := app.RecordDelegatedCall(ctx, serviceID, engine.DelegatedCall{
			CallerClient: "docs", Subject: fmt.Sprintf("u%d", i), Operation: "check", Questions: 1, Allowed: 1})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("answered", i+1) // the call was answered: the record is the sink's business from here on
		time.Sleep(auditCallsEach)
	}
}

// F4-2 (async): after `kill -9` the records lost are at most those of the last flush windows: the one being written and
// the one being filled (each at most 500 records, the size that triggers a flush), and what was flushed earlier is stored.
func TestF4_2_AsyncAuditAfterKillNineLosesAtMostTheLastWindows(t *testing.T) {
	w := newWorld(t)
	const killAfter = 2500

	cmd := exec.Command(os.Args[0], "-test.run=^TestF4_2_AuditChild$", "-test.v")
	cmd.Env = append(os.Environ(), auditChildEnv+"="+w.org,
		"DB__URI="+w.cfg.DB.URI, "REDIS__URI="+testInfrastructure.RedisURI,
		"SYNC__ENGINE=postgres", "DELEGATION__AUDIT_MODE=async", "DELEGATION__AUDIT_BUFFER=100000")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	answered := 0
	scan := bufio.NewScanner(out)
	for scan.Scan() {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(scan.Text()), "answered %d", &n); err == nil {
			answered = n
		}
		if answered >= killAfter {
			break
		}
	}
	if answered < killAfter {
		t.Fatalf("the child answered %d calls before it ended", answered)
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL: nothing is flushed on the way out
		t.Fatal(err)
	}
	_ = cmd.Wait()

	// A statement the database was running when the child died is finished or rolled back, never half stored.
	time.Sleep(500 * time.Millisecond)
	stored := w.countRows(`SELECT count(*) FROM delegated_call`)
	lost := answered - stored
	t.Logf("answered (seen) %d, stored %d, lost %d", answered, stored, lost)
	if stored == 0 {
		t.Fatal("nothing was stored: the sink never flushed")
	}
	if lost > 1000 {
		t.Fatalf("%d records lost after kill -9, want at most the last two windows of 500", lost)
	}
}
