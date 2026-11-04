//go:build integration

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/outbox"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestTypeModeStartsOnlyWithItsFirstApplyAndKeepsItsTimestamp(t *testing.T) {
	for _, rows := range []string{`[]`, `[{"id":"one","name":"One"}]`} {
		t.Run(rows, func(t *testing.T) {
			pool, typ := backoffType(t)
			ctx := context.Background()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(rows))
			}))
			t.Cleanup(source.Close)
			id := uuid.New()
			raw, _ := json.Marshal(map[string]any{"url": source.URL})
			if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP',$3)`, id, typ, raw); err != nil {
				t.Fatal(err)
			}
			a := New(pool, &config.Config{})
			t.Cleanup(a.Close)
			before, err := a.LoadTypeGraph(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if before.IsSync(typ) {
				t.Fatal("a configured source without data was sync")
			}
			settle := func(context.Context, pgx.Tx, map[uuid.UUID]bool) error { return nil }
			if _, err := a.runSync(ctx, []uuid.UUID{typ}, false, settle); err != nil {
				t.Fatal(err)
			}
			after, err := a.LoadTypeGraph(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !after.IsSync(typ) {
				t.Fatal("successful first apply did not establish sync mode")
			}
			var first time.Time
			if err := pool.QueryRow(ctx, `SELECT synced_at FROM type_resource WHERE id=$1`, typ).Scan(&first); err != nil {
				t.Fatal(err)
			}
			var streams []string
			var policy map[string][]outbox.Image
			if err := pool.QueryRow(ctx, `SELECT streams,policy FROM outbox ORDER BY revision DESC LIMIT 1`).Scan(&streams, &policy); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(streams, outbox.Catalog) || len(policy[outbox.Catalog]) != 1 {
				t.Fatalf("first mode publication: %v %v", streams, policy)
			}
			var image map[string]any
			if err := json.Unmarshal(policy[outbox.Catalog][0].Row, &image); err != nil || image["mode"] != "sync" {
				t.Fatalf("mode image: %v %v", image, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE sync_state SET next_due_at=now()-interval '1 second' WHERE type_resource_id=$1`, typ); err != nil {
				t.Fatal(err)
			}
			if _, err := a.runSync(ctx, []uuid.UUID{typ}, false, settle); err != nil {
				t.Fatal(err)
			}
			var again time.Time
			if err := pool.QueryRow(ctx, `SELECT synced_at FROM type_resource WHERE id=$1`, typ).Scan(&again); err != nil || !again.Equal(first) {
				t.Fatalf("first apply timestamp changed: %v %v %v", first, again, err)
			}
		})
	}
}

func TestFetchCannotApplyAfterTheSynchronizerWasDeletedOrReconfigured(t *testing.T) {
	for _, change := range []string{"delete", "configuration", "configuration-restored"} {
		t.Run(change, func(t *testing.T) {
			pool, typ := backoffType(t)
			ctx := context.Background()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"retired","name":"Retired"}]`))
			}))
			t.Cleanup(source.Close)
			id := uuid.New()
			raw, _ := json.Marshal(map[string]any{"url": source.URL})
			if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP',$3)`, id, typ, raw); err != nil {
				t.Fatal(err)
			}
			a := New(pool, &config.Config{})
			t.Cleanup(a.Close)
			a.Hooks.AfterFetch = func(context.Context, []uuid.UUID) {
				sql := `UPDATE synchronizer SET deleted_at=now() WHERE id=$1`
				if change != "delete" {
					sql = `UPDATE synchronizer SET config=config || '{"url":"http://127.0.0.1:1/replacement"}'::jsonb WHERE id=$1`
				}
				if _, err := support.RawExec(ctx, pool, sql, id); err != nil {
					t.Fatal(err)
				}
				if change == "configuration-restored" {
					if _, err := support.RawExec(ctx, pool, `UPDATE synchronizer SET config=$2 WHERE id=$1`, id, raw); err != nil {
						t.Fatal(err)
					}
				}

			}
			_, err := a.runSync(ctx, []uuid.UUID{typ}, false, func(context.Context, pgx.Tx, map[uuid.UUID]bool) error { return nil })
			if !errors.Is(err, errSynchronizerChanged) {
				t.Fatalf("retired fetch was accepted: %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_cache WHERE type_resource_id=$1`, typ).Scan(&count); err != nil || count != 0 {
				t.Fatalf("retired rows applied: %d %v", count, err)
			}
			var initialized bool
			if err := pool.QueryRow(ctx, `SELECT synced_at IS NOT NULL FROM type_resource WHERE id=$1`, typ).Scan(&initialized); err != nil || initialized {
				t.Fatalf("retired fetch initialized mode: %v %v", initialized, err)
			}
		})
	}
}

func TestDeletingAndReplacingASynchronizerResetItsAppliedGeneration(t *testing.T) {
	pool, typ := backoffType(t)
	ctx := context.Background()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	t.Cleanup(source.Close)
	raw, _ := json.Marshal(map[string]any{"url": source.URL})
	old := uuid.New()
	if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP',$3)`, old, typ, raw); err != nil {
		t.Fatal(err)
	}
	a := New(pool, &config.Config{})
	t.Cleanup(a.Close)
	settle := func(context.Context, pgx.Tx, map[uuid.UUID]bool) error { return nil }
	if _, err := a.runSync(ctx, []uuid.UUID{typ}, false, settle); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	if err := pool.QueryRow(ctx, `SELECT synced_at FROM type_resource WHERE id=$1`, typ).Scan(&first); err != nil {
		t.Fatal(err)
	}
	var move *ClosureMove
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		move, err = a.ChangeMode(ctx, tx, typ, func() error {
			_, err := tx.Exec(ctx, `UPDATE synchronizer SET deleted_at=now() WHERE id=$1`, old)
			return err
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if move == nil || !slices.Contains(move.CatalogTypes, typ) {
		t.Fatal("deleting an empty applied source missed its type image")
	}
	var reset bool
	var states int
	if err := pool.QueryRow(ctx, `SELECT synced_at IS NULL FROM type_resource WHERE id=$1`, typ).Scan(&reset); err != nil || !reset {
		t.Fatalf("delete retained type mode: %v %v", reset, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sync_state WHERE type_resource_id=$1`, typ).Scan(&states); err != nil || states != 0 {
		t.Fatalf("delete retained sync state: %d %v", states, err)
	}
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := a.ChangeMode(ctx, tx, typ, func() error {
			_, err := tx.Exec(ctx, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP',$3)`, uuid.New(), typ, raw)
			return err
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	graph, err := a.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if graph.IsSync(typ) {
		t.Fatal("replacement inherited the old source's applied state")
	}
	if _, err := a.runSync(ctx, []uuid.UUID{typ}, false, settle); err != nil {
		t.Fatal(err)
	}
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT synced_at FROM type_resource WHERE id=$1`, typ).Scan(&next); err != nil || !next.After(first) {
		t.Fatalf("replacement did not get its own first apply: %v %v %v", first, next, err)
	}
}

// The point-lookup path must fence the same source generation as a full apply.
func TestInlineFetchCannotApplyARetiredSourceGeneration(t *testing.T) {
	for _, change := range []string{"delete", "configuration", "configuration-restored"} {
		t.Run(change, func(t *testing.T) {
			pool, typ := backoffType(t)
			ctx := context.Background()
			id := uuid.New()
			var raw []byte
			changed := make(chan error, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sql := `UPDATE synchronizer SET deleted_at=now() WHERE id=$1`
				if change != "delete" {
					sql = `UPDATE synchronizer SET config=config || '{"url":"http://127.0.0.1:1/replacement"}'::jsonb WHERE id=$1`
				}
				_, err := support.RawExec(ctx, pool, sql, id)
				if err == nil && change == "configuration-restored" {
					_, err = support.RawExec(ctx, pool, `UPDATE synchronizer SET config=$2 WHERE id=$1`, id, raw)
				}
				changed <- err
				_, _ = w.Write([]byte(`{"id":"retired","name":"Retired"}`))
			}))
			t.Cleanup(source.Close)
			raw, _ = json.Marshal(map[string]any{"url": source.URL})
			if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP',$3);`, id, typ, raw); err != nil {
				t.Fatal(err)
			}
			// Model an already completed generation: this read takes the point-fetch path.
			if _, err := support.RawExec(ctx, pool, `UPDATE type_resource SET synced_at=now() WHERE id=$1`, typ); err != nil {
				t.Fatal(err)
			}
			a := New(pool, &config.Config{})
			t.Cleanup(a.Close)
			_, err := a.GetResource(ctx, typ, "retired")
			select {
			case mutationErr := <-changed:
				if mutationErr != nil {
					t.Fatal(mutationErr)
				}
			default:
				t.Fatalf("point source was not called: %v", err)
			}
			if !errors.Is(err, errSynchronizerChanged) {
				t.Fatalf("retired inline fetch accepted: %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_cache WHERE type_resource_id=$1`, typ).Scan(&count); err != nil || count != 0 {
				t.Fatalf("retired row applied: %d %v", count, err)
			}
		})
	}
}

func TestResourceListsHideRetiredRowsBeforeFirstApply(t *testing.T) {
	pool, typ := backoffType(t)
	ctx := context.Background()
	if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(type_resource_id,kind,config) VALUES($1,'HTTP','{"url":"http://127.0.0.1:1/unavailable"}')`, typ); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resource_cache(type_resource_id,external_id,name,meta,active) VALUES($1,'retired','Retired','{}',true)`, typ); err != nil {
		t.Fatal(err)
	}
	inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return markUnanswered(ctx, tx, typ, "first snapshot failed")
	})
	if _, err := pool.Exec(ctx, `UPDATE sync_state SET next_due_at=now()+interval '1 hour' WHERE type_resource_id=$1`, typ); err != nil {
		t.Fatal(err)
	}
	a := New(pool, &config.Config{})
	t.Cleanup(a.Close)
	for _, includeInactive := range []bool{false, true} {
		rows, err := a.ListResources(ctx, typ, includeInactive)
		if err != nil || len(rows) != 0 {
			t.Fatalf("retired generation visible (includeInactive=%v): %v %v", includeInactive, rows, err)
		}
	}
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_cache WHERE type_resource_id=$1`, typ).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("fixture rows must remain physically retained: %d %v", retained, err)
	}
}
