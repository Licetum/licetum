package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ScopeGlobal is the only revision-state row.
const ScopeGlobal = "global"

// BumpRevision adds one to the revision of the scope and returns the new value. q is the transaction of the write the
// revision numbers: the revision commits with it, or not at all. Writers of one scope queue on its row, which is accepted:
// administrators write rarely, and a synchronization writes once per change of a service's data.
func BumpRevision(ctx context.Context, q Querier, scope string) (int64, error) {
	var revision int64
	err := q.QueryRow(ctx, bumpRevisionSQL).Scan(&revision)
	return revision, err
}

const bumpRevisionSQL = `UPDATE revision_state SET revision = revision + 1 WHERE scope = 'global' RETURNING revision`

// QueueBumpRevision adds the bump of the scope to a batch that runs in the transaction of the write, so that it travels with the
// other statements of the batch in one round trip; the new revision is the result of the statement (a row with one column).
func QueueBumpRevision(b *pgx.Batch, scope string) { b.Queue(bumpRevisionSQL) }

// DefaultFailoverJump is how far Failover raises the revisions: well beyond the revisions a primary can have given out that a
// replica missed, and small enough to stay far from the end of an int64.
const DefaultFailoverJump int64 = 1_000_000

// BumpAllRevisions adds jump to every revision of revision_state, and creates the revision of the rules when there is none. It is
// what an operator does after promoting a replica (licetum failover): a replica that missed the last transactions of the old
// primary missed their revisions too, and would number its next writes with revisions the regions already used for other content.
func BumpAllRevisions(ctx context.Context, q Querier, jump int64) (map[string]int64, error) {
	if jump < 1 {
		return nil, fmt.Errorf("the jump must be positive, got %d", jump)
	}
	rows, err := q.Query(ctx, `
		WITH ensure AS (INSERT INTO revision_state (scope, revision) VALUES ($2, 0) ON CONFLICT (scope) DO NOTHING)
		UPDATE revision_state SET revision = revision + $1 RETURNING scope, revision`, jump, ScopeGlobal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var scope string
		var revision int64
		if err := rows.Scan(&scope, &revision); err != nil {
			return nil, err
		}
		out[scope] = revision
	}
	return out, rows.Err()
}

// Revisions are the revisions of the policy as of one reading.
type Revisions struct {
	Epoch      int32
	Revision   int64
	LogVersion int64
}

// LoadRevisions reads the global position and log-settings version in one snapshot.
func LoadRevisions(ctx context.Context, q Querier) (Revisions, error) {
	var out Revisions
	err := q.QueryRow(ctx, `SELECT epoch, revision, (SELECT version FROM log_settings WHERE id = 1) FROM revision_state WHERE scope = 'global'`).Scan(&out.Epoch, &out.Revision, &out.LogVersion)
	return out, err
}
