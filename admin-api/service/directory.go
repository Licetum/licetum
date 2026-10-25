package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The subject directory of a service (ADR 0001, 8.8): the people it sent, to pick members from, to show whom a rule reaches and
// to answer a subject search. A write takes no revision and no change record: no decision reads the directory.

// PutDirectory writes the people of the service. An entry that is already as sent is not touched.
func (a *App) PutDirectory(ctx context.Context, serviceID uuid.UUID, in *model.DirectoryPut) (*model.DirectoryWriteResult, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	entries := make([]db.DirectoryEntry, len(in.Subjects))
	for i, e := range in.Subjects {
		entries[i] = db.DirectoryEntry{Type: e.Type, ID: e.ID, DisplayName: e.Name, Properties: e.Properties}
	}
	written, err := db.UpsertDirectory(ctx, a.DB, serviceID, entries)
	if err != nil {
		return nil, err
	}
	return &model.DirectoryWriteResult{Created: written.Created, Updated: written.Updated, Unchanged: written.Unchanged}, nil
}

// ListDirectory pages the people of the service. search matches the id and the name.
func (a *App) ListDirectory(ctx context.Context, serviceID uuid.UUID, subjectType, search string, limit, offset int) (*model.DirectoryList, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	page, err := db.SearchDirectory(ctx, a.DB, serviceID, subjectType, strings.TrimSpace(search), limit, offset)
	if err != nil {
		return nil, err
	}
	out := &model.DirectoryList{Total: page.Total, Items: make([]model.DirectoryEntry, len(page.Entries))}
	for i, e := range page.Entries {
		out.Items[i] = directoryEntryResponse(e)
	}
	return out, nil
}

func directoryEntryResponse(e db.DirectoryEntry) model.DirectoryEntry {
	return model.DirectoryEntry{Type: e.Type, ID: e.ID, Name: e.DisplayName, Properties: e.Properties, UpdatedAt: e.UpdatedAt, SentAt: e.SentAt}
}

// GetDirectoryEntry returns a person of the service with the groups they are in, decided with the stored properties as claims.
func (a *App) GetDirectoryEntry(ctx context.Context, serviceID uuid.UUID, subjectType, subjectID string) (*model.DirectoryEntryDetail, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	entry, err := db.GetDirectoryEntry(ctx, a.DB, serviceID, subjectType, subjectID)
	if err != nil {
		return nil, directoryError(err, subjectType, subjectID)
	}
	groups, err := a.groupsOf(ctx, entry.ID, entry.Properties)
	if err != nil {
		return nil, err
	}
	return &model.DirectoryEntryDetail{DirectoryEntry: directoryEntryResponse(entry), Groups: groups}, nil
}

// groupsOf lists the groups a subject is in now, nested groups included, as its claims say.
func (a *App) groupsOf(ctx context.Context, subject string, claims map[string]any) ([]model.SubjectGroup, error) {
	identity, _, err := a.ResolveIdentitySet(ctx, subject, engine.NewEnv(engine.Claims(claims), nil))
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
	groups := []model.SubjectGroup{}
	if len(ids) == 0 {
		return groups, nil
	}
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
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// DeleteDirectoryEntry removes a person from the directory of the service. They are in it again the next time they are sent.
func (a *App) DeleteDirectoryEntry(ctx context.Context, serviceID uuid.UUID, subjectType, subjectID string) error {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return err
	}
	return directoryError(db.DeleteDirectoryEntry(ctx, a.DB, serviceID, subjectType, subjectID), subjectType, subjectID)
}

// GetDirectorySettings returns the settings of the directory of the service.
func (a *App) GetDirectorySettings(ctx context.Context, serviceID uuid.UUID) (*model.DirectorySettings, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	s, err := db.GetDirectorySettings(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	return &model.DirectorySettings{RetentionDays: s.RetentionDays}, nil
}

// PutDirectorySettings replaces the settings of the directory of the service.
func (a *App) PutDirectorySettings(ctx context.Context, serviceID uuid.UUID, in *model.DirectorySettings) (*model.DirectorySettings, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	if err := db.PutDirectorySettings(ctx, a.DB, serviceID, db.DirectorySettings{RetentionDays: in.RetentionDays}); err != nil {
		return nil, err
	}
	return in, nil
}

func directoryError(err error, subjectType, subjectID string) error {
	if errors.Is(err, db.ErrDirectoryNotFound) {
		return apperr.NotFound(i18n.DirectoryEntryNotFound, subjectType, subjectID)
	}
	return err
}
