package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// AuditModeStrict answers a delegated call only after its record is committed.
	AuditModeStrict = "strict"
	// AuditModeAsync buffers the record and writes it in batches, off the request path.
	AuditModeAsync = "async"

	auditFlushEvery = 500 * time.Millisecond
	auditBatch      = 500
	auditFlushLimit = 10 * time.Second
)

// auditRecord is a buffered delegated call.
type auditRecord struct {
	at        time.Time
	serviceID uuid.UUID
	call      DelegatedCall
	requestID string
}

// auditSink is the buffer of the async audit mode: records wait in memory and one goroutine writes them with a single
// multi-row INSERT every auditFlushEvery, or as soon as auditBatch of them are waiting.
type auditSink struct {
	app *App
	cap int

	mu     sync.Mutex
	buf    []auditRecord
	closed bool

	flusher *flusher
}

// audit returns the sink of the async mode, started on first use; nil in strict mode.
func (a *App) audit() *auditSink {
	if a.Cfg.Delegation.AuditMode != AuditModeAsync {
		return nil
	}
	a.auditOnce.Do(func() {
		s := &auditSink{app: a, cap: max(a.Cfg.Delegation.AuditBuffer, 1)}
		if a.metrics != nil {
			a.metrics.registry.MustRegister(
				prometheus.NewGaugeFunc(prometheus.GaugeOpts{
					Name: "licetum_audit_buffer_records",
					Help: "Delegated-call records waiting in the async audit buffer.",
				}, func() float64 { return float64(s.len()) }),
				prometheus.NewGaugeFunc(prometheus.GaugeOpts{
					Name: "licetum_audit_lag_seconds",
					Help: "Age of the oldest delegated-call record not yet written (0 when the buffer is empty).",
				}, s.lag),
			)
		}
		s.flusher = startFlusher(auditFlushEvery, s.flush)
		a.auditSink.Store(s)
	})
	return a.auditSink.Load()
}

func (s *auditSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

func (s *auditSink) lag() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return 0
	}
	return time.Since(s.buf[0].at).Seconds()
}

func (s *auditSink) drop(n int) {
	if n > 0 && s.app.metrics != nil {
		s.app.metrics.auditDropped.Add(float64(n))
	}
}

// add buffers a record; a record that does not fit (or comes after Close) is dropped and counted.
func (s *auditSink) add(r auditRecord) {
	s.mu.Lock()
	if s.closed || len(s.buf) >= s.cap {
		s.mu.Unlock()
		s.drop(1)
		return
	}
	s.buf = append(s.buf, r)
	full := len(s.buf) >= auditBatch
	s.mu.Unlock()
	if full {
		s.flusher.kick()
	}
}

// flush writes the buffer in statements of at most auditBatch records. A statement that fails puts its records back
// (older first) for the next attempt; at the final flush they are dropped and counted.
func (s *auditSink) flush(final bool) {
	for {
		s.mu.Lock()
		if final {
			s.closed = true
		}
		n := min(len(s.buf), auditBatch)
		if n == 0 {
			s.mu.Unlock()
			return
		}
		batch := append([]auditRecord(nil), s.buf[:n]...)
		s.buf = s.buf[n:]
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), auditFlushLimit)
		err := s.write(ctx, batch)
		cancel()
		if err == nil {
			continue
		}
		slog.Warn("failed to write the delegated-call audit", "records", len(batch), "error", err)
		s.mu.Lock()
		if final {
			lost := len(batch) + len(s.buf)
			s.buf = nil
			s.mu.Unlock()
			s.drop(lost)
			return
		}
		merged := append(batch, s.buf...)
		if over := len(merged) - s.cap; over > 0 {
			merged = merged[:s.cap]
			s.drop(over)
		}
		s.buf = merged
		s.mu.Unlock()
		return
	}
}

// write inserts a batch with one statement.
func (s *auditSink) write(ctx context.Context, batch []auditRecord) error {
	n := len(batch)
	at := make([]time.Time, n)
	service := make([]string, n)
	client := make([]string, n)
	callerSubject := make([]string, n)
	subject := make([]string, n)
	operation := make([]string, n)
	questions := make([]int, n)
	allowed := make([]int, n)
	requestID := make([]string, n)
	for i, r := range batch {
		at[i], service[i], client[i] = r.at, r.serviceID.String(), r.call.CallerClient
		callerSubject[i], subject[i], operation[i] = r.call.CallerSubject, r.call.Subject, r.call.Operation
		questions[i], allowed[i], requestID[i] = r.call.Questions, r.call.Allowed, r.requestID
	}
	_, err := s.app.DB.Exec(ctx, `
		INSERT INTO delegated_call (at, service_id, caller_client, caller_subject, subject, operation, questions, allowed, request_id)
		SELECT at, service_id, caller_client, NULLIF(caller_subject, ''), subject, operation, questions::int, allowed::int, NULLIF(request_id, '')
		FROM unnest($1::timestamptz[], $2::uuid[], $3::text[], $4::text[], $5::text[], $6::text[], $7::bigint[], $8::bigint[], $9::text[])
		     AS t(at, service_id, caller_client, caller_subject, subject, operation, questions, allowed, request_id)`,
		at, service, client, callerSubject, subject, operation, questions, allowed, requestID)
	return err
}

// close flushes what is left and stops the sink.
func (s *auditSink) close() {
	s.flusher.close(auditFlushLimit + time.Second)
}
