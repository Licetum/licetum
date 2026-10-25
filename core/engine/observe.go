package engine

import (
	"context"
	"time"

	"github.com/licetum/licetum/core/db"
)

// observer feeds what the database pool reports into the metrics of the App and into the Trace of the request the call
// belongs to (when the call ran with the context of a request).
type observer struct{ a *App }

func (o observer) ObserveQuery(ctx context.Context, operation string, took time.Duration, err error) {
	if err == nil || db.IsUnavailable(err) {
		o.a.health.postgres.note(err != nil)
	}
	if m := o.a.metrics; m != nil {
		m.dbQueries.WithLabelValues(operation, resultOf(err)).Inc()
		m.dbQueryDuration.WithLabelValues(operation).Observe(took.Seconds())
	}
	if t := TraceFrom(ctx); t != nil {
		t.addDB(operation)
	}
}

func resultOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
