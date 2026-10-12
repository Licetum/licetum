package loadtest

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Statement is one statement of the run's database, with what it cost during the run.
type Statement struct {
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_ms"`
	MeanMS  float64 `json:"mean_ms"`
}

// statementCounts are the statements of the run's database as the server counts them, by query id, at one moment.
type statementCounts map[int64]Statement

// snapshotStatements reads the statement counters of the run's database (pg_stat_statements). Only this database's
// statements are read: a server may hold other databases too.
func snapshotStatements(ctx context.Context, pool *pgxpool.Pool) (statementCounts, error) {
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(queryid, 0), query, calls, total_exec_time
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`)
	if err != nil {
		return nil, fmt.Errorf("read the statements: %w", err)
	}
	defer rows.Close()
	out := statementCounts{}
	for rows.Next() {
		var id int64
		var s Statement
		if err := rows.Scan(&id, &s.Query, &s.Calls, &s.TotalMS); err != nil {
			return nil, fmt.Errorf("scan a statement: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

// since is what the statements cost between two snapshots: the most expensive first, at most limit of them. A
// statement the server dropped from its counters between the two has no calls to count, and is left out.
func since(before, after statementCounts, limit int) []Statement {
	var out []Statement
	for id, a := range after {
		b := before[id]
		calls := a.Calls - b.Calls
		if calls <= 0 {
			continue
		}
		total := a.TotalMS - b.TotalMS
		out = append(out, Statement{
			Query:   shorten(a.Query, 160),
			Calls:   calls,
			TotalMS: total,
			MeanMS:  total / float64(calls),
		})
	}
	slices.SortFunc(out, func(x, y Statement) int { return cmp.Compare(y.TotalMS, x.TotalMS) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

var spaces = regexp.MustCompile(`\s+`)

// shorten makes a statement one line of at most n characters, for the report.
func shorten(q string, n int) string {
	q = strings.TrimSpace(spaces.ReplaceAllString(q, " "))
	if len(q) <= n {
		return q
	}
	return q[:n] + "…"
}
