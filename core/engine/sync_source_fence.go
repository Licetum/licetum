package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

var errSynchronizerChanged = errors.New("the synchronizer changed while its source was being fetched")

// The share locks survive until publication commits. Deleting or changing the source
// must serialize with apply; data fetched under a retired configuration never applies.
func fenceSynchronizers(ctx context.Context, tx pgx.Tx, fetched []model.Synchronizer) error {
	for _, before := range fetched {
		current, err := ScanSynchronizer(tx.QueryRow(ctx, `SELECT `+SynchronizerColumns+` FROM synchronizer WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, before.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return errSynchronizerChanged
		}
		if err != nil {
			return err
		}
		expected, err := json.Marshal(before.Config.StoredConfig())
		if err != nil {
			return err
		}
		actual, err := json.Marshal(current.Config.StoredConfig())
		if err != nil {
			return err
		}
		if current.RowVersion != before.RowVersion || current.TypeResourceID != before.TypeResourceID || current.Config.Kind != before.Config.Kind || !bytes.Equal(expected, actual) || !slices.Equal(current.Regions, before.Regions) {
			return errSynchronizerChanged
		}
	}
	return nil
}

// A full applied snapshot, including an empty one, establishes the type's mode.
// Later applies leave this generation's original timestamp unchanged.
func initializeTypeMode(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	changed, err := tx.Exec(ctx, `UPDATE type_resource SET synced_at=now() WHERE id=$1 AND synced_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return changed.RowsAffected() > 0, nil
}

// A mode restored by the first apply must restore routes below it too. The old
// closure cannot find those descendants: deletion deliberately cut those routes.
func touchModeDescendants(ctx context.Context, tx pgx.Tx, types []uuid.UUID, touched map[uuid.UUID]struct{}) ([]uuid.UUID, error) {
	if len(types) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE below(id) AS (
 SELECT id FROM resource_cache WHERE type_resource_id=ANY($1) AND active
 UNION SELECT c.id FROM below b JOIN resource_edge e ON e.parent_id=b.id JOIN resource_cache c ON c.id=e.child_id AND c.active
 ) SELECT r.id,r.type_resource_id FROM resource_cache r JOIN below b ON b.id=r.id`, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	affected := map[uuid.UUID]bool{}
	for rows.Next() {
		var id, typ uuid.UUID
		if err := rows.Scan(&id, &typ); err != nil {
			return nil, err
		}
		touched[id] = struct{}{}
		affected[typ] = true
	}
	return Keys(affected), rows.Err()
}
