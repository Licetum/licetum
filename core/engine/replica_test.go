package engine

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
)

func replicaApp(action string, maxLag, staleness time.Duration) (*App, *pgxpool.Pool, *pgxpool.Pool) {
	primary, replica := new(pgxpool.Pool), new(pgxpool.Pool)
	a := &App{DB: primary, ReadDB: replica, Cfg: &config.Config{
		DB:     config.Database{MaxReplicaLag: maxLag, LagAction: action},
		Policy: config.Policy{MaxStaleness: staleness},
	}}
	a.tune = newTuning(a.Cfg)
	return a, primary, replica
}

func (a *App) setLag(d time.Duration) {
	a.replica.lag.Store(int64(d))
	a.replica.measured.Store(true)
	a.replica.confirmedAt.Store(time.Now().UnixNano())
}

func TestReaderIsTheReplicaUnlessTheRequestNeedsThePrimary(t *testing.T) {
	a, primary, replica := replicaApp("unready", 10*time.Second, 0)
	ctx := t.Context()
	if a.reader(ctx) != replica {
		t.Fatal("a plain read does not go to the replica")
	}
	if a.reader(withPrimary(ctx)) != primary {
		t.Fatal("a read that must be current does not go to the primary")
	}
	// A lagging replica keeps the reads when the action is to take the process out of rotation.
	a.setLag(time.Minute)
	if a.reader(ctx) != replica {
		t.Fatal("with the action unready the reads stay on the replica")
	}
	if (&App{DB: primary}).reader(ctx) != primary {
		t.Fatal("without a replica the primary serves")
	}
}

func TestReadsMoveToThePrimaryWhileTheReplicaLagsAndComeBack(t *testing.T) {
	a, primary, replica := replicaApp("primary", 10*time.Second, 0)
	ctx := t.Context()
	a.setLag(5 * time.Second)
	if a.reader(ctx) != replica {
		t.Fatal("below the limit the replica serves")
	}
	a.setLag(11 * time.Second)
	if a.reader(ctx) != primary {
		t.Fatal("above the limit the primary serves")
	}
	a.setLag(time.Second)
	if a.reader(ctx) != replica {
		t.Fatal("the replica serves again once it caught up")
	}
}

func TestReplicaThatCannotBeComparedWithThePrimaryFailsClosedAfterTheLimit(t *testing.T) {
	a, _, _ := replicaApp("unready", 10*time.Second, 30*time.Second)
	a.setLag(0)
	if err := a.replicaGate(); err != nil {
		t.Fatalf("just compared: %v", err)
	}
	// The primary has not been reachable for a minute: the replica may be anywhere behind it.
	a.replica.confirmedAt.Store(time.Now().Add(-time.Minute).UnixNano())
	if err := a.replicaGate(); err == nil {
		t.Fatal("a replica unconfirmed for longer than POLICY__MAX_STALENESS must fail closed")
	}
	b, _, _ := replicaApp("unready", 10*time.Second, 0)
	b.replica.confirmedAt.Store(time.Now().Add(-time.Hour).UnixNano())
	if err := b.replicaGate(); err != nil {
		t.Fatalf("without a limit: %v", err)
	}
}

func TestReplicaBeyondTheStalenessLimitFailsClosed(t *testing.T) {
	a, _, _ := replicaApp("unready", 10*time.Second, 30*time.Second)
	if err := a.replicaGate(); err != nil {
		t.Fatalf("no measurement yet: %v", err)
	}
	a.setLag(20 * time.Second)
	if err := a.replicaGate(); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
	a.setLag(31 * time.Second)
	err := a.replicaGate()
	apiErr, ok := apperr.As(err)
	if !ok || apiErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("beyond the limit: %v, want 503", err)
	}
	// Reading from the primary is not stale, whatever the replica does.
	b, _, _ := replicaApp("primary", 10*time.Second, 30*time.Second)
	b.setLag(time.Hour)
	if err := b.replicaGate(); err != nil {
		t.Fatalf("with the reads on the primary: %v", err)
	}
	// No limit, no gate.
	c, _, _ := replicaApp("unready", 10*time.Second, 0)
	c.setLag(time.Hour)
	if err := c.replicaGate(); err != nil {
		t.Fatalf("without POLICY__MAX_STALENESS: %v", err)
	}
}
