package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/outbox"
)

// ScopeGlobal is the only revision-state row.
const ScopeGlobal = "global"

// BumpRevision adds one to the revision of the scope and returns the new value. q is the transaction of the write the
// revision numbers: the revision commits with it, or not at all. Writers of one scope queue on its row, which is accepted:
// administrators write rarely, and a synchronization writes once per change of a service's data.
func BumpRevision(ctx context.Context, q Querier, scope string) (int64, error) {
	if tx, ok := q.(pgx.Tx); ok {
		p, err := TakeRevision(ctx, tx, outbox.Entry{})
		return p.Revision, err
	}
	if pool, ok := q.(*pgxpool.Pool); ok {
		var p Position
		err := InTx(ctx, pool, func(tx pgx.Tx) error { var err error; p, err = TakeRevision(ctx, tx, outbox.Entry{}); return err })
		return p.Revision, err
	}
	return 0, fmt.Errorf("revision bump requires a transaction or pool")
}

// DefaultFailoverJump is how far Failover raises the revisions: well beyond the revisions a primary can have given out that a
// replica missed, and small enough to stay far from the end of an int64.
const DefaultFailoverJump int64 = 1_000_000

// StartEpoch fences a promoted primary with a new epoch and an atomic outbox
// marker. Call before reopening traffic; P6 supplies divergence reconciliation.
func StartEpoch(ctx context.Context, tx pgx.Tx, jump int64) (Position, error) {
	if jump < 1 {
		return Position{}, fmt.Errorf("the jump must be positive, got %d", jump)
	}
	var p Position
	err := tx.QueryRow(ctx, `WITH r AS (
 UPDATE revision_state SET epoch=epoch+1, revision=revision+$1 WHERE scope='global' RETURNING epoch,revision
 ) INSERT INTO outbox(revision,epoch,kind,committed_at,services,streams,policy,chunk_count)
 SELECT revision,epoch,'epoch_start',clock_timestamp(),'{}'::uuid[],'{}'::text[],'{}'::jsonb,0 FROM r
 RETURNING epoch,revision`, jump).Scan(&p.Epoch, &p.Revision)
	if err != nil {
		return Position{}, err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify('licetum_outbox',$1)`, strconv.FormatInt(p.Revision, 10))
	return p, err
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
