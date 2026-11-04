package db

import (
	"context"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

type roundTripKey struct{}

// RoundTripCounts counts calls to PostgreSQL, with a pgx.Batch counted once.
// Write counts explicit read-write transactions. Total also includes autocommit
// work (including scenario writes), preflight reads and read-only snapshots.
type RoundTripCounts struct {
	Total        int `json:"total"`
	Write        int `json:"write"`
	Transactions int `json:"transactions"`
}

// RoundTripCounter is an opt-in, request-scoped tracer counter. It is independent
// of the statement observer used for production query metrics.
type RoundTripCounter struct {
	mu       sync.Mutex
	counts   RoundTripCounts
	writable map[*pgx.Conn]bool
}

// CountRoundTrips attaches a fresh counter to ctx; only work carrying this context
// is counted, so background pollers and other requests cannot consume its budget.
func CountRoundTrips(ctx context.Context) (context.Context, *RoundTripCounter) {
	counter := &RoundTripCounter{}
	return context.WithValue(ctx, roundTripKey{}, counter), counter
}

func (c *RoundTripCounter) Counts() RoundTripCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts
}

func traceRoundTrip(ctx context.Context, conn *pgx.Conn, sql string) {
	counter, _ := ctx.Value(roundTripKey{}).(*RoundTripCounter)
	if counter == nil {
		return
	}
	operation := Operation(sql)
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.counts.Total++
	if operation == "begin" {
		if counter.writable == nil {
			counter.writable = map[*pgx.Conn]bool{}
		}
		write := !strings.Contains(strings.ToLower(sql), "read only")
		counter.writable[conn] = write
		if write {
			counter.counts.Transactions++
		}
	}
	if counter.writable[conn] {
		counter.counts.Write++
	}
	if operation == "commit" || operation == "rollback" {
		delete(counter.writable, conn)
	}
}
