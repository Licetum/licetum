package db

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Scopes of the revision_state table (see migration 00002).
const (
	// ScopeRules numbers the changes of everything a decision is made from except the resource data: rules, memberships,
	// groups, roles and their items, access groups, services, types and synchronizers.
	ScopeRules = "rules"
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
	err := q.QueryRow(ctx, `
		INSERT INTO revision_state (scope, revision) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET revision = revision_state.revision + 1
		RETURNING revision`, scope).Scan(&revision)
	return revision, err
}

// Revisions are the revisions of the policy as of one reading.
type Revisions struct {
	// Rules is the revision of ScopeRules.
	Rules int64
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
		}
	}
	return out, rows.Err()
}
