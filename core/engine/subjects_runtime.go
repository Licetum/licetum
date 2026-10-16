package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	// subjectNoteEvery is how often a row of one subject changes: a busy API verifies the same token many times a minute.
	subjectNoteEvery = 10 * time.Minute
	// subjectNoteLimit bounds the subjects waiting in memory; a new subject beyond it is not noted until the buffer drains.
	subjectNoteLimit = 100_000
	// subjectFlushEvery and subjectBatch bound the batch upsert: every second, at most 500 rows per statement.
	subjectFlushEvery = time.Second
	subjectBatch      = 500
	subjectFlushLimit = 10 * time.Second
	// subjectRowGuard is how old a row must be to be written again. It is shorter than subjectNoteEvery by two flush
	// intervals: a process queues a subject subjectNoteEvery after the last time, but its flush may come a tick earlier
	// than the previous one did, and a guard of exactly subjectNoteEvery would then skip the write and delay it by
	// another whole interval.
	subjectRowGuard = subjectNoteEvery - 2*subjectFlushEvery

	subjectMaxClaims    = 32
	subjectMaxValue     = 1024
	subjectMaxListItems = 64
	// EvaluationLimit bounds how many known subjects an evaluation (who does a rule reach) goes through.
	EvaluationLimit = 5000
)

// subjectBuffer holds the subjects of verified tokens until the next batch upsert. A subject seen many times in a window
// is one entry (the last claims win).
type subjectBuffer struct {
	// flushMu lets one flush run at a time: a flush asked for (a test, the shutdown) waits for the one the ticker is in the
	// middle of, instead of finding the buffer empty and returning while the rows are still being written.
	flushMu sync.Mutex
	mu      sync.Mutex
	pending map[string]pendingSubject
	// queued is when each subject was last put in the buffer by this process: a busy API verifies the same token many times
	// a second, and a subject is queued again only after subjectNoteEvery, so a warm request costs a map lookup and no write.
	queued  map[string]time.Time
	closed  bool
	flusher *flusher
}

type pendingSubject struct {
	name   *string
	claims Claims
}

// NoteSubject remembers the subject of a verified token. It costs a map write; the batch upsert runs in the
// background and a failure is only logged: the registry is a convenience, never a reason to refuse a request.
func (a *App) NoteSubject(subject string, claims Claims) {
	if !a.Cfg.Subjects.Enabled || subject == "" {
		return
	}
	b := &a.subjects
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if b.flusher == nil {
		b.flusher = startFlusher(subjectFlushEvery, a.flushSubjects)
	}
	now := time.Now()
	if last, ok := b.queued[subject]; ok && now.Sub(last) < subjectNoteEvery {
		b.mu.Unlock()
		return
	}
	if len(b.pending) >= subjectNoteLimit {
		b.mu.Unlock()
		return
	}
	if b.pending == nil {
		b.pending = map[string]pendingSubject{}
	}
	if b.queued == nil || len(b.queued) >= subjectNoteLimit {
		b.queued = map[string]time.Time{}
	}
	b.queued[subject] = now
	b.pending[subject] = pendingSubject{name: displayName(claims), claims: claims}
	full := len(b.pending) >= subjectBatch
	f := b.flusher
	b.mu.Unlock()
	if full {
		f.kick()
	}
}

// close flushes what is left and stops the buffer.
func (b *subjectBuffer) close() {
	b.mu.Lock()
	b.closed = true
	f := b.flusher
	b.mu.Unlock()
	if f != nil {
		f.close(subjectFlushLimit + time.Second)
	}
}

// FlushSubjects writes the waiting subjects now (the flusher does it every second; tests and shutdown call it).
func (a *App) FlushSubjects(ctx context.Context) {
	a.subjects.flushMu.Lock()
	defer a.subjects.flushMu.Unlock()
	for {
		batch := a.subjects.take(subjectBatch)
		if len(batch) == 0 {
			return
		}
		if err := a.upsertSubjects(ctx, batch); err != nil {
			if rejectedByServer(err) && len(batch) > 1 {
				// One subject the database refuses (a NUL in a claim, say) must not take the others with it.
				for subject, p := range batch {
					if err := a.upsertSubjects(ctx, map[string]pendingSubject{subject: p}); err != nil {
						slog.WarnContext(ctx, "failed to note a subject", "error", err)
					}
				}
				continue
			}
			slog.WarnContext(ctx, "failed to note subjects", "subjects", len(batch), "error", err)
			return
		}
	}
}

func (a *App) flushSubjects(bool) {
	ctx, cancel := context.WithTimeout(context.Background(), subjectFlushLimit)
	defer cancel()
	a.FlushSubjects(ctx)
}

// take removes up to n subjects from the buffer.
func (b *subjectBuffer) take(n int) map[string]pendingSubject {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]pendingSubject, min(n, len(b.pending)))
	for k, v := range b.pending {
		if len(out) == n {
			break
		}
		out[k] = v
		delete(b.pending, k)
	}
	return out
}

// displayName is the name claim, or the given and family names; nil when the token has neither.
func displayName(claims Claims) *string {
	if s, ok := claims["name"].(string); ok && strings.TrimSpace(s) != "" {
		s = strings.TrimSpace(s)
		return &s
	}
	given, _ := claims["given_name"].(string)
	family, _ := claims["family_name"].(string)
	if full := strings.TrimSpace(given + " " + family); full != "" {
		return &full
	}
	return nil
}

// keptClaims reduces a token's claims to the ones the policy reads, as strings or lists of strings (the way the
// rules compare them).
func keptClaims(claims Claims, attrs []string) map[string]any {
	out := map[string]any{}
	for _, attr := range attrs {
		name, ok := strings.CutPrefix(attr, "claim:")
		if !ok || len(out) >= subjectMaxClaims {
			continue
		}
		value, found := claims[name]
		if !found || value == nil {
			continue
		}
		switch v := value.(type) {
		case []any:
			list := make([]string, 0, min(len(v), subjectMaxListItems))
			for _, item := range v {
				if len(list) == subjectMaxListItems {
					break
				}
				if s := PyStr(item); len(s) <= subjectMaxValue {
					list = append(list, s)
				}
			}
			out[name] = list
		case map[string]any:
			// An object cannot be compared by a rule.
		default:
			if s := PyStr(v); len(s) <= subjectMaxValue {
				out[name] = s
			}
		}
	}
	return out
}

// upsertSubjects writes a batch with one statement. The conflict clause only touches a row that has not been written for
// subjectNoteEvery, so N replicas noting the same person change the row once per interval cluster-wide; the other
// statements are no-ops.
func (a *App) upsertSubjects(ctx context.Context, batch map[string]pendingSubject) error {
	attrs, err := a.RequestAttributes(ctx)
	if err != nil {
		return err
	}
	subjects := make([]string, 0, len(batch))
	names := make([]*string, 0, len(batch))
	claims := make([]string, 0, len(batch))
	for subject, p := range batch {
		raw, err := json.Marshal(keptClaims(p.claims, attrs))
		if err != nil {
			return err
		}
		subjects, names, claims = append(subjects, subject), append(names, p.name), append(claims, string(raw))
	}
	_, err = a.DB.Exec(ctx, `
		INSERT INTO known_subject (subject, display_name, claims)
		SELECT subject, display_name, claims::jsonb FROM unnest($1::text[], $2::text[], $3::text[]) AS t(subject, display_name, claims)
		ON CONFLICT (subject) DO UPDATE SET display_name = EXCLUDED.display_name, claims = EXCLUDED.claims,
		    last_seen = now(), seen_count = known_subject.seen_count + 1
		WHERE known_subject.last_seen < now() - make_interval(secs => $4)`, subjects, names, claims, subjectRowGuard.Seconds())
	return err
}

// KnownSubject is a registry row: a person seen by a verified token, with the claims the policy reads.
type KnownSubject struct {
	Subject string
	Name    *string
	Claims  Claims
}

// LoadKnownSubjectsAfter reads up to limit subjects of the registry whose identifier sorts after the given one, in that
// order (the primary key serves it): a stable order that does not move when a person is seen again, so a cursor over it
// neither skips nor repeats anybody. Empty after starts from the beginning. It is empty while the registry is off.
func (a *App) LoadKnownSubjectsAfter(ctx context.Context, after string, limit int) ([]KnownSubject, error) {
	rows, err := a.DB.Query(ctx,
		`SELECT subject, display_name, claims FROM known_subject WHERE subject > $1 ORDER BY subject LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnownSubject
	for rows.Next() {
		var s KnownSubject
		var raw []byte
		if err := rows.Scan(&s.Subject, &s.Name, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &s.Claims); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
