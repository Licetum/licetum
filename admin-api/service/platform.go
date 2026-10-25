package service

import (
	"context"
	"slices"
	"strings"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// GetPlatform returns the id of the self-service (Licetum registered within itself) and its policy types.
func (a *App) GetPlatform(ctx context.Context) (*model.Platform, error) {
	if _, err := a.GetExistingService(ctx, a.DB, model.PlatformServiceID); err != nil {
		if _, ok := apperr.As(err); ok {
			return nil, apperr.NotFound(i18n.PlatformNotSeeded)
		}
		return nil, err
	}
	types := make([]model.PlatformTypeInfo, 0, len(model.PlatformTypes))
	for _, t := range model.PlatformTypes {
		types = append(types, model.PlatformTypeInfo{Name: string(t), ID: model.PlatformTypeResourceID(t)})
	}
	return &model.Platform{ServiceID: model.PlatformServiceID, AdminGroupID: model.PlatformAdminGroupID, Types: types}, nil
}

// Access is what the current user may do with the platform's own resource types (the policies in
// model.PlatformTypes). The admin API checks it on every request.
type Access struct {
	Subject string
	// permission is the sync part of the permission result of the engine for the self-service: grants —
	// including whole-type ones — are already expanded into one entry per existing entity, with the
	// denials applied.
	permission map[string][]model.ResourceAccess
	// wholeType keeps the levels held on a type as a whole (denials of the type itself applied): what
	// creating new entities needs. A point denial does not touch it.
	wholeType map[string]map[string]bool
	// denied lists the entities with levels that were granted but denied.
	denied map[string][]model.ResourceAccess
}

// Authenticate verifies the token of an administrator and loads the caller's access to the platform policies. authorization is
// the raw Authorization header.
//
// The token is checked against the static issuer of ADMIN_AUTH__* (engine.VerifyAdminToken): a missing header, a malformed,
// expired or rejected token and a token for another audience are a 401 with the Bearer challenge. The permissions come from
// the same rules as for any other service, on the self-service. If ADMIN_AUTH__* is not configured, or the self-service is not
// seeded, the admin API is closed to everyone (503): there is nothing to verify the token or the permissions against.
func (a *App) Authenticate(ctx context.Context, authorization string) (*Access, error) {
	if !a.Cfg.AdminAuth.IsConfigured() {
		return nil, apperr.ServiceUnavailable(i18n.AdminAuthNotConfigured)
	}
	if _, err := a.GetExistingService(ctx, a.DB, model.PlatformServiceID); err != nil {
		if _, ok := apperr.As(err); ok {
			return nil, apperr.ServiceUnavailable(i18n.PlatformNotSeeded)
		}
		return nil, err
	}

	token := bearerToken(authorization)
	if token == "" {
		return nil, apperr.Unauthorized(i18n.AuthHeaderRequired)
	}
	verified, err := a.VerifyAdminToken(ctx, token)
	if err != nil {
		return nil, err
	}
	claims := engine.Claims(verified.Claims)
	subject, err := a.AdminSubject(claims)
	if err != nil {
		return nil, err
	}

	data, err := a.GetPermissionData(ctx, model.PlatformServiceID, subject, engine.NewEnv(claims, nil), engine.MinimizeLatency)
	if err != nil {
		return nil, err
	}
	// The platform types always have a synchronizer (seed), so what is needed is the resources of the
	// sync types.
	access := &Access{
		Subject:    subject,
		permission: map[string][]model.ResourceAccess{},
		wholeType:  map[string]map[string]bool{},
		denied:     data.Denied,
	}
	for name, entry := range data.Types {
		if entry.Type == model.TypeModeSync {
			access.permission[name] = entry.Resources
		}
	}
	for name, levels := range data.Whole {
		set := make(map[string]bool, len(levels))
		for _, l := range levels {
			set[l] = true
		}
		access.wholeType[name] = set
	}
	return access, nil
}

// bearerToken extracts the token from "Bearer <token>" (the scheme is case-insensitive).
func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// CanWholeType reports access to the type as a whole — required to create new entities.
func (a *Access) CanWholeType(t model.PlatformType, action model.Action) bool {
	return a.wholeType[string(t)][string(action)]
}

// Can reports access to a specific entity (by id). Only its own entry counts: whole-type grants are
// expanded into entries, and a denial on the entity has already taken its levels away.
func (a *Access) Can(t model.PlatformType, action model.Action, id string) bool {
	normalized, ok := normalizeID(id)
	if !ok { // not an id of any entity: nothing to deny, the handler answers 422 on it
		return a.CanWholeType(t, action)
	}
	return slices.ContainsFunc(a.permission[string(t)], func(e model.ResourceAccess) bool {
		entryID, ok := normalizeID(e.ID)
		return ok && entryID == normalized && slices.Contains(e.Access, string(action))
	})
}

// CanAny reports access to at least one entity of the type — enough to list the collection
// (the handler then filters the list with FilterAllowed).
func (a *Access) CanAny(t model.PlatformType, action model.Action) bool {
	if a.CanWholeType(t, action) {
		return true
	}
	return slices.ContainsFunc(a.permission[string(t)], func(e model.ResourceAccess) bool {
		return slices.Contains(e.Access, string(action))
	})
}

// Summary is the GET /platform/me response: the levels held on a type as a whole, the entities whose
// access differs from that (otherwise the response would grow with the number of entities), and the
// entities with denied levels.
func (a *Access) Summary() *model.PlatformAccess {
	out := &model.PlatformAccess{Subject: a.Subject, Types: map[string]model.TypeAccess{}}
	for _, t := range model.PlatformTypes {
		whole := a.wholeType[string(t)]
		entries := []model.ResourceAccess{}
		for _, e := range a.permission[string(t)] {
			if sameLevels(e.Access, whole) {
				continue
			}
			entries = append(entries, e)
		}
		denied := a.denied[string(t)]
		if denied == nil {
			denied = []model.ResourceAccess{}
		}
		if len(whole) == 0 && len(entries) == 0 && len(denied) == 0 {
			continue
		}
		wholeLevels := make([]string, 0, len(whole))
		for level := range whole {
			wholeLevels = append(wholeLevels, level)
		}
		slices.Sort(wholeLevels)
		out.Types[string(t)] = model.TypeAccess{WholeType: wholeLevels, Entries: entries, Denied: denied}
	}
	return out
}

func sameLevels(levels []string, set map[string]bool) bool {
	if len(levels) != len(set) {
		return false
	}
	for _, l := range levels {
		if !set[l] {
			return false
		}
	}
	return true
}

// FilterAllowed keeps only the items the caller has the given access to.
func FilterAllowed[T any](a *Access, t model.PlatformType, action model.Action, items []T, id func(T) uuid.UUID) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		if a.Can(t, action, id(item).String()) {
			out = append(out, item)
		}
	}
	return out
}

// normalizeID brings a platform entity id (always a uuid) to its canonical form, so the case or
// format used in a URL does not affect the comparison.
func normalizeID(id string) (string, bool) {
	u, err := uuid.Parse(id)
	if err != nil {
		return "", false
	}
	return u.String(), true
}
