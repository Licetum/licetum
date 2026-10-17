package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Scopes of the revision_state table (see migration 00002).
const (
	// ScopeRules numbers the changes of everything a decision is made from except the resource data: rules, memberships,
	// groups, roles and their items, access groups, services, types and synchronizers.
	ScopeRules = "rules"
	// ScopeLogSettings numbers the changes of the log settings: a process that sees it move loads them.
	ScopeLogSettings = "log_settings"
	// dataScopePrefix starts the scope of the resource data of a service.
	dataScopePrefix = "data:"
)

// DataScope is the scope of the resource data of a service.
func DataScope(serviceID uuid.UUID) string { return dataScopePrefix + serviceID.String() }

// ServiceOfDataScope is the service a data scope belongs to.
func ServiceOfDataScope(scope string) (string, bool) {
	return strings.CutPrefix(scope, dataScopePrefix)
}

// BumpRevision adds one to the revision of the scope and returns the new value. q is the transaction of the write the
// revision numbers: the revision commits with it, or not at all. Writers of one scope queue on its row, which is accepted:
// administrators write rarely, and a synchronization writes once per change of a service's data.
func BumpRevision(ctx context.Context, q Querier, scope string) (int64, error) {
	var revision int64
	err := q.QueryRow(ctx, bumpRevisionSQL, scope).Scan(&revision)
	return revision, err
}

const bumpRevisionSQL = `
	INSERT INTO revision_state (scope, revision) VALUES ($1, 1)
	ON CONFLICT (scope) DO UPDATE SET revision = revision_state.revision + 1
	RETURNING revision`

// QueueBumpRevision adds the bump of the scope to a batch that runs in the transaction of the write, so that it travels with the
// other statements of the batch in one round trip; the new revision is the result of the statement (a row with one column).
func QueueBumpRevision(b *pgx.Batch, scope string) { b.Queue(bumpRevisionSQL, scope) }

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
		UPDATE revision_state SET revision = revision + $1 RETURNING scope, revision`, jump, ScopeRules)
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
	// Rules is the revision of ScopeRules.
	Rules int64
	// LogSettings is the revision of ScopeLogSettings.
	LogSettings int64
	// Data is the revision of the resource data of each service (its id as text); a service that is absent has 0.
	Data map[string]int64
}

// LoadRevisions reads every scope in one statement.
func LoadRevisions(ctx context.Context, q Querier) (Revisions, error) {
	rows, err := q.Query(ctx, `SELECT scope, revision FROM revision_state`)
	if err != nil {
		return Revisions{}, err
	}
	defer rows.Close()
	out := Revisions{Data: map[string]int64{}}
	for rows.Next() {
		var scope string
		var revision int64
		if err := rows.Scan(&scope, &revision); err != nil {
			return Revisions{}, err
		}
		if service, ok := ServiceOfDataScope(scope); ok {
			out.Data[service] = revision
		} else if scope == ScopeRules {
			out.Rules = revision
		} else if scope == ScopeLogSettings {
			out.LogSettings = revision
		}
	}
	return out, rows.Err()
}
