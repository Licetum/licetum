package engine

import (
	"context"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
)

// EvaluationLimit bounds how many people of the directories an evaluation of the rules (whom does a rule reach) goes through.
const EvaluationLimit = 5000

// DirectoryAfter reads up to limit people of the subject directory of the service, of the given type (empty: any), in the order
// of the scans (type, then id, bytewise) after the key (nil: from the first). The subject search decides them one by one.
func (a *App) DirectoryAfter(ctx context.Context, serviceID uuid.UUID, subjectType string, after *db.DirectoryKey, limit int) ([]db.DirectoryEntry, error) {
	return db.DirectoryAfter(ctx, a.DB, serviceID, subjectType, after, limit)
}
