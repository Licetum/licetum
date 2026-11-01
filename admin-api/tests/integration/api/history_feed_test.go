//go:build integration

package api_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
)

func TestContract_HIS05_ConcurrentFollowerNeverSkipsOrRepeatsRecords(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base, err := db.LoadLocalVersions(ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 10
	errs := make(chan error, writers)
	ids := make(chan uuid.UUID, 2*writers)
	var wg sync.WaitGroup
	for n := range writers {
		wg.Go(func() {
			ops := []model.BatchOperation{}
			for i := range 2 {
				ops = append(ops, model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchRole, Body: map[string]any{"name": fmt.Sprintf("follower-%d-%d", n, i), "service_id": model.PlatformServiceID}})
			}
			results, err := e.app.RunBatch(ctx, ops)
			if err == nil {
				for _, r := range results {
					ids <- r.ID
				}
			}
			errs <- err
		})
	}
	filter := service.ChangeFilter{AfterRevision: base.Revision, AfterSeq: 2147483647, Entity: "role", Limit: 3}
	seen := map[string]bool{}
	got := map[uuid.UUID]bool{}
	for len(got) < 2*writers {
		records, err := e.app.ListChanges(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range records {
			key := fmt.Sprintf("%d.%d", r.Revision, r.Seq)
			if seen[key] || r.EntityID == nil {
				t.Fatalf("duplicate or missing identity: %+v", r)
			}
			if r.Revision < filter.AfterRevision || (r.Revision == filter.AfterRevision && r.Seq <= filter.AfterSeq) {
				t.Fatal("cursor regressed")
			}
			seen[key] = true
			got[*r.EntityID] = true
			filter.AfterRevision, filter.AfterSeq = r.Revision, r.Seq
		}
		if len(records) == 0 {
			select {
			case <-ctx.Done():
				t.Fatal("follower missed committed records")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if !got[id] {
			t.Fatalf("missed committed entity %s", id)
		}
	}
}
