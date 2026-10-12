package loadtest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TableSize is how many rows a table of the product holds after a run, and what it takes on disk (its indexes apart).
// The closure table (resource_ancestor) relative to the resources is the number the choice of materializing it rests on.
type TableSize struct {
	Table      string `json:"table"`
	Rows       int64  `json:"rows"`
	TotalBytes int64  `json:"total_bytes"`
	IndexBytes int64  `json:"index_bytes"`
}

// storedTables are the tables of the product the report sizes.
var storedTables = []string{"resource_cache", "resource_edge", "resource_ancestor", "assignment_rule", "group_member", "user_group", "type_resource"}

// tableSizes reads the rows and the sizes of the product's tables in the run's database.
func tableSizes(ctx context.Context, pool *pgxpool.Pool) ([]TableSize, error) {
	out := make([]TableSize, 0, len(storedTables))
	for _, table := range storedTables {
		var t TableSize
		t.Table = table
		if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&t.Rows); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		if err := pool.QueryRow(ctx, `SELECT pg_total_relation_size($1::regclass), pg_indexes_size($1::regclass)`, table).
			Scan(&t.TotalBytes, &t.IndexBytes); err != nil {
			return nil, fmt.Errorf("size %s: %w", table, err)
		}
		out = append(out, t)
	}
	return out, nil
}
