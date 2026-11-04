package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// ScopeGlobal is the only revision-state row.
const ScopeGlobal = "global"

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

// LocalVersions is the global publication position and log-settings version of one reading.
type LocalVersions struct {
	Epoch      int32
	Revision   int64
	LogVersion int64
}

// LoadLocalVersions reads the global position and log-settings version in one snapshot.
func LoadLocalVersions(ctx context.Context, q Querier) (LocalVersions, error) {
	var out LocalVersions
	err := q.QueryRow(ctx, `SELECT epoch, revision, (SELECT version FROM log_settings WHERE id = 1) FROM revision_state WHERE scope = 'global'`).Scan(&out.Epoch, &out.Revision, &out.LogVersion)
	return out, err
}
