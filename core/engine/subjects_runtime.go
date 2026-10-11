package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// subjectNoteEvery is how often one subject is written: a busy API verifies the same token many times a minute.
	subjectNoteEvery = 10 * time.Minute
	// subjectNoteLimit bounds the in-process memory of what was noted when.
	subjectNoteLimit = 100_000
	// subjectPruneEvery is how often the retention is applied.
	subjectPruneEvery = time.Hour

	subjectMaxClaims    = 32
	subjectMaxValue     = 1024
	subjectMaxListItems = 64
	// EvaluationLimit bounds how many known subjects an evaluation (who does a rule reach) goes through.
	EvaluationLimit = 5000
)

// subjectNotes remembers when each subject was last written, in this process.
type subjectNotes struct {
	mu   sync.Mutex
	seen map[string]time.Time
	// pruned is when the retention was last applied (unix nanoseconds).
	pruned atomic.Int64
}

func (n *subjectNotes) due(subject string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil || len(n.seen) >= subjectNoteLimit {
		n.seen = map[string]time.Time{}
	}
	if last, ok := n.seen[subject]; ok && now.Sub(last) < subjectNoteEvery {
		return false
	}
	n.seen[subject] = now
	return true
}

// NoteSubject remembers the subject of a verified token. It costs a map lookup unless the subject is due; the write
// runs in the background and a failure is only logged: the registry is a convenience, never a reason to refuse a request.
func (a *App) NoteSubject(subject string, claims Claims) {
	if !a.Cfg.Subjects.Enabled || subject == "" || !a.subjectNotes.due(subject, time.Now()) {
		return
	}
	name := displayName(claims)
	a.Background(func(ctx context.Context) {
		if err := a.recordSubject(ctx, subject, name, claims); err != nil {
			slog.WarnContext(ctx, "failed to note a subject", "error", err)
		}
	})
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

func (a *App) recordSubject(ctx context.Context, subject string, name *string, claims Claims) error {
	attrs, err := a.RequestAttributes(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(keptClaims(claims, attrs))
	if err != nil {
		return err
	}
	if _, err := a.DB.Exec(ctx, `
		INSERT INTO known_subject (subject, display_name, claims) VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (subject) DO UPDATE SET display_name = EXCLUDED.display_name, claims = EXCLUDED.claims,
		    last_seen = now(), seen_count = known_subject.seen_count + 1`, subject, name, string(raw)); err != nil {
		return err
	}
	a.pruneSubjects(ctx)
	return nil
}

// pruneSubjects applies the retention, at most once an hour per process.
func (a *App) pruneSubjects(ctx context.Context) {
	days := a.Cfg.Subjects.RetentionDays
	now := time.Now()
	last := a.subjectNotes.pruned.Load()
	if days <= 0 || now.Sub(time.Unix(0, last)) < subjectPruneEvery || !a.subjectNotes.pruned.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	if _, err := a.DB.Exec(ctx, `DELETE FROM known_subject WHERE last_seen < now() - make_interval(days => $1)`, days); err != nil {
		slog.WarnContext(ctx, "failed to apply the retention of known subjects", "error", err)
	}
}
