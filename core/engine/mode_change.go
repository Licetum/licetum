package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
)

// A type is sync while it has a live synchronizer and not_sync without one (TypeGraph.IsSync). Its rows stay in the local copy
// when the synchronizer is deleted, but they are not resources then, and the closure of the types below it does not lead
// through them (rebuildClosureSQL). The closure is stored, so the change of the mode has to reach it: a synchronization of the
// types below would do it, but only when their sources answer and a worker runs, and until then a grant above would keep
// flowing through the rows that are not there (or a denial above would stop reaching the rows that are back). ChangeMode
// makes the closure follow in the transaction that changes the mode.

// ClosureMove says which types had the closure of their resources rebuilt by ChangeMode: the type itself and the types below
// it. The caller hands it to AfterClosureMove once the transaction has committed.
type ClosureMove struct {
	g     *TypeGraph
	types []uuid.UUID
}

// ChangeMode runs write, the creation or the deletion of the synchronizer of a type, in the transaction tx, and rebuilds the
// closure of the resources of the type and of the resources below them (found by the declared links) before the transaction commits, under the lock of the
// component of the type that a synchronization takes (ComponentKeys) and with its timeouts: a synchronization that is
// writing the same component finishes first, and one that starts later reads the mode this transaction wrote
// (rebuildClosure reads the types that have resources in its own transaction). The data revision of every service that owns
// one of the rebuilt types moves in the same transaction, so that no answer made from the closure as it was outlives the
// commit. The result is nil when the type has no rows: there is nothing to rebuild (the first synchronizer of a type finds
// the links to it unresolved, and its first synchronization resolves them).
//
// write returns the error of the statement as it is: the caller tells a unique violation from the others.
func (a *App) ChangeMode(ctx context.Context, tx db.Querier, typeID uuid.UUID, write func() error) (*ClosureMove, error) {
	g, err := loadTypeGraph(ctx, tx)
	if err != nil {
		return nil, err
	}
	var move *ClosureMove
	err = a.withSyncTimeouts(ctx, tx, func() error {
		for _, key := range ComponentKeys(g, []uuid.UUID{typeID}) {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
				return err
			}
		}
		if err := write(); err != nil {
			return err
		}
		// The rows of the type and every row that hangs below them by the declared links. The closure cannot say who hangs
		// below: it was cut when the synchronizer was deleted, and a route that is back has no row in it yet.
		rows, err := tx.Query(ctx, `
			WITH RECURSIVE below(id) AS (
				SELECT id FROM resource_cache WHERE type_resource_id = $1 AND active
				UNION
				SELECT c.id FROM below b
				JOIN resource_edge e ON e.parent_id = b.id
				JOIN resource_cache c ON c.id = e.child_id AND c.active
			)
			SELECT id FROM below`, typeID)
		if err != nil {
			return err
		}
		touched := map[uuid.UUID]struct{}{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			touched[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(touched) == 0 {
			return nil
		}
		if err := rebuildClosure(ctx, tx, touched); err != nil {
			return err
		}
		types := []uuid.UUID{typeID}
		for id := range g.DescendantsOf(typeID) {
			types = append(types, id)
		}
		if err := bumpDataRevisions(ctx, tx, g, types); err != nil {
			return err
		}
		move = &ClosureMove{g: g, types: types}
		return nil
	})
	return move, err
}

// AfterClosureMove is for the process that committed a ChangeMode: it reads the revisions again, and tells the replicas of
// the previous release, which follow the data versions in Redis. A nil move (nothing was rebuilt) does nothing.
func (a *App) AfterClosureMove(ctx context.Context, move *ClosureMove) {
	if move == nil {
		return
	}
	a.invalidateView()
	pipe := a.Redis.Pipeline()
	recordDataChange(ctx, pipe, move.g, move.types)
	if _, err := pipe.Exec(ctx); err != nil {
		logRedis(ctx, slog.LevelWarn, "failed to record the resource data change for the previous release", err)
	}
}

// withSyncTimeouts runs fn in the transaction tx with the lock and statement timeouts of a synchronization, and puts the
// ones the transaction had back: the statements that follow (the change record, the bump of the revision) are those of
// an ordinary write.
func (a *App) withSyncTimeouts(ctx context.Context, tx db.Querier, fn func() error) error {
	var lock, statement string
	if err := tx.QueryRow(ctx, `SELECT current_setting('lock_timeout'), current_setting('statement_timeout')`).Scan(&lock, &statement); err != nil {
		return err
	}
	set := func(lock, statement string) error {
		_, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true), set_config('statement_timeout', $2, true)`, lock, statement)
		return err
	}
	if err := set(fmt.Sprintf("%dms", a.lockTimeout().Milliseconds()), fmt.Sprintf("%dms", a.statementTimeout().Milliseconds())); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return set(lock, statement)
}
