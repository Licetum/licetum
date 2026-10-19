package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
)

// The state of the synchronization of a type (when it was applied, what it was applied with, when it is due again)
// lives in the sync_state table, in step with the data it describes: the worker writes both in one transaction. A
// Redis flush or a failover loses nothing and starts no synchronization.

// Role is what a process does with the synchronization. Only a worker applies it; the PDP and the admin processes ask
// for it (see Ensure) and read what the worker wrote.
type Role int

const (
	// RoleAny is an engine without a restriction: tests, tools and library use.
	RoleAny Role = iota
	// RolePDP is the regional PDP (licetum pdp): it never writes the resource graph.
	RolePDP
	// RoleAdmin is the admin API: it asks for synchronizations, and refreshes a single resource inline (resolve).
	RoleAdmin
	// RoleWorker executes the synchronization jobs.
	RoleWorker
)

func (r Role) String() string {
	switch r {
	case RolePDP:
		return "pdp"
	case RoleAdmin:
		return "admin"
	case RoleWorker:
		return "worker"
	}
	return "any"
}

const (
	// syncFormat is the version of the digest (data hash, graph signature) the state is written with. A state written
	// with the previous format is still read: the digest has the same shape, so it is rewritten only when the data or the
	// graph signature changed. Anything else counts as never synchronized.
	syncFormat         = 5
	syncFormatPrevious = 4

	// syncTTL is how long a type's source is not asked again after a synchronization (the throttle).
	syncTTL = 30 * time.Second

	// syncLockTimeout is how long a synchronization transaction waits for a lock before it gives up (and the job is
	// tried again); it never holds a pooled connection waiting for longer.
	syncLockTimeout = 5 * time.Second

	// maxJobClaims is how many times a job is claimed before it is given up (a job that kills its worker every time).
	maxJobClaims = 5
)

// syncState is the row of sync_state of a type, with the comparisons against the clock made by the database.
type syncState struct {
	TypeID    uuid.UUID
	Format    int
	DataHash  string
	GraphSig  string
	Applied   bool // applied_at is set: the data of the type was brought up to its source at least once
	Due       bool // next_due_at has come
	Backoff   bool // the source failed lately and next_due_at is the end of the backoff
	Attempts  int
	LastError string
	// Version is the version of the row (its xmin): every write of the row changes it, and every job that ends writes the
	// row of each type it was due for (see jobName).
	Version string
}

// usable says whether the stored digest can be compared: written by this release or the one before it.
func (s syncState) usable() bool {
	return s.Applied && s.DataHash != "" && (s.Format == syncFormat || s.Format == syncFormatPrevious)
}

// loadSyncStates reads the state of the given types. A type without a row is absent from the result.
func loadSyncStates(ctx context.Context, q db.Querier, typeIDs []uuid.UUID) (map[uuid.UUID]syncState, error) {
	rows, err := q.Query(ctx, `
		SELECT type_resource_id, format, data_hash, graph_sig, applied_at IS NOT NULL,
		       next_due_at <= now(), attempts > 0 AND next_due_at > now(), attempts, COALESCE(last_error, ''), xmin::text
		FROM sync_state WHERE type_resource_id = ANY($1)`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]syncState{}
	for rows.Next() {
		var s syncState
		if err := rows.Scan(&s.TypeID, &s.Format, &s.DataHash, &s.GraphSig, &s.Applied, &s.Due, &s.Backoff, &s.Attempts, &s.LastError, &s.Version); err != nil {
			return nil, err
		}
		out[s.TypeID] = s
	}
	return out, rows.Err()
}

// invalidatePlatformSync makes the types of the platform itself due now. Their rows are written by the admin API, and a
// write must show at once: the next request that reads them waits for a synchronization that started after the write.
// It is the only thing a write does to the synchronization — the data of the other services is not asked for.
func (a *App) invalidatePlatformSync(ctx context.Context) error {
	_, err := a.DB.Exec(ctx, `
		UPDATE sync_state st SET next_due_at = now(), invalidated_at = now()
		FROM type_resource t
		WHERE t.id = st.type_resource_id AND t.service_id = $1`, model.PlatformServiceID)
	return err
}

// syncPool is the pool the synchronization transactions run on: the separate one when the process has it.
func (a *App) syncPool() *pgxpool.Pool {
	if a.SyncDB != nil {
		return a.SyncDB
	}
	return a.DB
}

// ComponentKeys names the connected components of the (undirected) type graph that the given types belong to, as the
// advisory lock keys they are written under. The closure rows of a resource only change inside the component of its
// type, so two synchronizations in different components never wait for each other. The keys are sorted: a transaction
// that takes several takes them in one order.
func ComponentKeys(g *TypeGraph, typeIDs []uuid.UUID) []int64 {
	adj := map[uuid.UUID][]uuid.UUID{}
	for id, n := range g.nodes {
		for _, e := range n.Parents {
			adj[id] = append(adj[id], e.Parent)
			adj[e.Parent] = append(adj[e.Parent], id)
		}
	}
	seen := map[int64]bool{}
	var keys []int64
	for _, start := range typeIDs {
		visited := map[uuid.UUID]bool{start: true}
		stack := []uuid.UUID{start}
		rep := start.String()
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if s := cur.String(); s < rep {
				rep = s
			}
			for _, next := range adj[cur] {
				if !visited[next] {
					visited[next] = true
					stack = append(stack, next)
				}
			}
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte("licetum.sync.component:" + rep))
		key := int64(h.Sum64()) // #nosec G115 -- a hash, wrapping is fine
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// syncTx runs fn in a transaction of the synchronization pool that holds the advisory locks of the given components.
// The transaction waits for a lock for at most syncLockTimeout and runs each statement for at most the statement timeout,
// so it cannot hold a pooled connection for ever.
func (a *App) syncTx(ctx context.Context, keys []int64, fn func(tx pgx.Tx) error) error {
	return db.InTx(ctx, a.syncPool(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true), set_config('statement_timeout', $2, true)`,
			fmt.Sprintf("%dms", a.lockTimeout().Milliseconds()), fmt.Sprintf("%dms", a.statementTimeout().Milliseconds())); err != nil {
			return err
		}
		for _, key := range keys {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func (a *App) lockTimeout() time.Duration {
	if a.Hooks.LockTimeout > 0 {
		return a.Hooks.LockTimeout
	}
	return syncLockTimeout
}

// isLockTimeout reports a lock wait that ran out (SQLSTATE 55P03), or a statement that did (57014).
func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "55P03" || pgErr.Code == "57014")
}
