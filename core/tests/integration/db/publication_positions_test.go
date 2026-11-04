//go:build integration

package db_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
)

func TestContract_PUB02_PolicyBindingsAndDirectoryInterleaveWithoutPositionGaps(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	before, err := db.LoadLocalVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	const writes = 24
	failures := make(chan error, writes)
	var wg sync.WaitGroup
	for i := range writes {
		wg.Go(func() {
			if i%3 == 2 {
				_, err := db.UpsertDirectory(ctx, pool, service, []db.DirectoryEntry{{Type: "user", ID: fmt.Sprintf("person-%d", i), DisplayName: "Person"}})
				failures <- err
				return
			}
			_, err := db.PolicyTx(ctx, pool, nil, func(ctx context.Context, tx pgx.Tx) ([]db.Change, error) {
				id := uuid.New()
				table := "user_group"
				var owner *uuid.UUID
				if i%3 == 0 {
					owner = &service
					if _, err := tx.Exec(ctx, `INSERT INTO user_group(id,service_id,name) VALUES($1,$2,$3)`, id, service, fmt.Sprintf("group-%d", i)); err != nil {
						return nil, err
					}
				} else {
					table = "admin_binding"
					if _, err := tx.Exec(ctx, `INSERT INTO admin_binding(id,scope_kind,role,subject) VALUES($1,'platform','platform_viewer',$2)`, id, fmt.Sprintf("viewer-%d", i)); err != nil {
						return nil, err
					}
				}
				return []db.Change{{Entity: table, ID: &id, Actor: "publication-test", Action: "create", ServiceID: owner, Touched: []db.Touched{{Table: table, Key: map[string]any{"id": id}, ServiceID: owner}}}}, nil
			})
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := pool.Query(ctx, `SELECT epoch,revision,streams,cardinality(services) FROM outbox WHERE revision>$1 ORDER BY revision`, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count, empty, policy, directory := 0, 0, 0, 0
	for rows.Next() {
		var epoch int32
		var rev int64
		var streams []string
		var services int
		if err := rows.Scan(&epoch, &rev, &streams, &services); err != nil {
			t.Fatal(err)
		}
		count++
		if epoch != before.Epoch || rev != before.Revision+int64(count) {
			t.Fatalf("position gap at %d: %d.%d after %d.%d", count, epoch, rev, before.Epoch, before.Revision)
		}
		switch {
		case len(streams) == 0:
			empty++
			if services != 0 {
				t.Fatal("binding affected decision services")
			}
		case len(streams) == 1 && streams[0] == outbox.Service(service):
			policy++
		case len(streams) == 1 && streams[0] == outbox.Directory(service):
			directory++
			if services != 0 {
				t.Fatal("directory affected policy services")
			}
		default:
			t.Fatalf("unexpected publication streams: %v", streams)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != writes || empty != writes/3 || policy != writes/3 || directory != writes/3 {
		t.Fatalf("publications: total %d empty %d policy %d directory %d", count, empty, policy, directory)
	}
	after, err := db.LoadLocalVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+writes {
		t.Fatalf("counter and outbox disagree: %+v", after)
	}
}
