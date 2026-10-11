package engine

import (
	"context"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// SimulateCheck answers checks as a subject with the given claims and request attributes would get them. It is the
// admin's simulation: the claims and the context are taken as they are given, and the answer comes from the data as
// it is now (consistency=higher).
func (a *App) SimulateCheck(ctx context.Context, serviceID uuid.UUID, subject string, env Env,
	checks []model.CheckRequest, explain bool) ([]model.CheckResult, error) {
	return a.CheckAccess(ctx, serviceID, subject, env, checks, explain, HigherConsistency)
}

// PermissionPage returns the permissions of a subject, filtered and paged (see PermissionFilter). GET /permission and
// the simulation of it both answer with this.
func (a *App) PermissionPage(ctx context.Context, serviceID uuid.UUID, subject string, env Env,
	consistency Consistency, f PermissionFilter) (PermissionResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	result, err := a.GetPermission(ctx, serviceID, subject, env, consistency)
	if err != nil {
		return nil, err
	}
	return result.Filter(f)
}
