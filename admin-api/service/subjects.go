package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The registry of known subjects. A subject is remembered when a token of it is verified (by the check API, and by the
// admin API for the people who manage Licetum): its name and the claims the policy reads, nothing else. It lets the
// interface say which groups a person is in and whom a rule reaches, which the rules alone cannot: a group is defined
// by predicates over claims, so its members are known only as far as someone has been seen.

// ListSubjects pages the registry, the most recently seen first. search matches the subject and the display name.
func (a *App) ListSubjects(ctx context.Context, search string, limit, offset int) (*model.KnownSubjectList, error) {
	pattern := "%" + escapeLike(strings.TrimSpace(search)) + "%"
	out := &model.KnownSubjectList{
		Enabled: a.Cfg.Subjects.Enabled, RetentionDays: a.Cfg.Subjects.RetentionDays, Items: []model.KnownSubject{},
	}
	const where = `WHERE subject ILIKE $1 ESCAPE '\' OR display_name ILIKE $1 ESCAPE '\'`
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM known_subject `+where, pattern).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `
		SELECT subject, display_name, first_seen, last_seen, seen_count FROM known_subject `+where+`
		ORDER BY last_seen DESC, subject LIMIT $2 OFFSET $3`, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s model.KnownSubject
		if err := rows.Scan(&s.Subject, &s.Name, &s.FirstSeen, &s.LastSeen, &s.SeenCount); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, s)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// knownSubject is a registry row as the evaluations read it.
type knownSubject struct {
	Subject string
	Name    *string
	Claims  engine.Claims
}

// loadKnownSubjects reads up to limit subjects, the most recently seen first.
func (a *App) loadKnownSubjects(ctx context.Context, limit int) ([]knownSubject, error) {
	rows, err := a.DB.Query(ctx,
		`SELECT subject, display_name, claims FROM known_subject ORDER BY last_seen DESC, subject LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []knownSubject
	for rows.Next() {
		var s knownSubject
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

// GetSubject returns a known subject with its claims and the groups it is in as of those claims.
func (a *App) GetSubject(ctx context.Context, subject string) (*model.KnownSubjectDetail, error) {
	var d model.KnownSubjectDetail
	var raw []byte
	err := a.DB.QueryRow(ctx, `
		SELECT subject, display_name, first_seen, last_seen, seen_count, claims FROM known_subject WHERE subject = $1`, subject,
	).Scan(&d.Subject, &d.Name, &d.FirstSeen, &d.LastSeen, &d.SeenCount, &raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound(i18n.SubjectNotFound, subject)
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &d.Claims); err != nil {
		return nil, err
	}
	if d.Claims == nil {
		d.Claims = map[string]any{}
	}
	identity, _, err := a.ResolveIdentitySet(ctx, subject, engine.NewEnv(engine.Claims(d.Claims), nil))
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for s, t := range identity {
		if t != engine.Yes {
			continue
		}
		if id, ok := strings.CutPrefix(s, model.GroupSubjectPrefix); ok {
			if gid, err := uuid.Parse(id); err == nil {
				ids = append(ids, gid)
			}
		}
	}
	d.Groups = []model.SubjectGroup{}
	if len(ids) > 0 {
		rows, err := a.DB.Query(ctx, `SELECT id, name FROM user_group WHERE id = ANY($1) ORDER BY name`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var g model.SubjectGroup
			if err := rows.Scan(&g.ID, &g.Name); err != nil {
				return nil, err
			}
			d.Groups = append(d.Groups, g)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// ForgetSubject removes a subject from the registry. It is noted again the next time one of its tokens is verified.
func (a *App) ForgetSubject(ctx context.Context, subject string) error {
	tag, err := a.DB.Exec(ctx, `DELETE FROM known_subject WHERE subject = $1`, subject)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound(i18n.SubjectNotFound, subject)
	}
	return nil
}
