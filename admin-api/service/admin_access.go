package service

import (
	"context"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// adminAccessSQL is true when somebody can still change the access rules of the platform, or when the platform is not
// seeded (then the admin API is closed anyway and there is nothing to protect). "Can change" means: a rule in force
// that allows write on the platform policy `assignment_rule` as a whole — directly or through a role — and applies to
// somebody: a named subject (a group needs a live member), a predicate over claims, or a group with a live member.
// A predicate or a nested group is taken as non-empty: whether anybody matches it cannot be known here.
const adminAccessSQL = `
SELECT NOT EXISTS (SELECT 1 FROM type_resource WHERE id = $1 AND deleted_at IS NULL)
    OR EXISTS (
	SELECT 1 FROM assignment_rule ar
	WHERE ar.enabled
	  AND (ar.valid_from IS NULL OR ar.valid_from <= now())
	  AND (ar.expires_at IS NULL OR ar.expires_at > now())
	  AND ar.effect = 'allow' AND NOT ar.exception
	  AND (
	    (ar.role_id IS NULL AND ar.type_resource_id = $1 AND ar.resource_id IS NULL
	       AND ar.resource_from_claim IS NULL AND ar.resource_where IS NULL AND 'write' = ANY(ar.access))
	    OR (ar.role_id IS NOT NULL AND EXISTS (
	       SELECT 1 FROM role_permission rp
	       WHERE rp.role_id = ar.role_id AND rp.type_resource_id = $1 AND rp.resource_id IS NULL
	         AND rp.resource_from_claim IS NULL AND rp.resource_where IS NULL AND 'write' = ANY(rp.access)))
	  )
	  AND (
	    (ar.subject IS NOT NULL AND (ar.subject NOT LIKE 'group:%' OR EXISTS (
	       SELECT 1 FROM group_member gm
	       WHERE gm.group_id::text = substr(ar.subject, 7) AND gm.enabled
	         AND (gm.valid_from IS NULL OR gm.valid_from <= now())
	         AND (gm.expires_at IS NULL OR gm.expires_at > now()))))
	    OR (ar.subject IS NULL AND ar.member_of_group_id IS NULL AND ar.predicate <> '[]'::jsonb)
	    OR (ar.member_of_group_id IS NOT NULL AND EXISTS (
	       SELECT 1 FROM group_member gm
	       WHERE gm.group_id = ar.member_of_group_id AND gm.enabled
	         AND (gm.valid_from IS NULL OR gm.valid_from <= now())
	         AND (gm.expires_at IS NULL OR gm.expires_at > now())))
	  )
)`

// protectsAdminAccess reports whether a change can take admin access away: a rule, a membership, a group or a role
// that is edited or deleted. A create only adds.
func protectsAdminAccess(c change) bool {
	if c.action == actionCreate {
		return false
	}
	switch c.entity {
	case "assignment_rule", "group_member", "group", "role", "role_permission":
		return true
	}
	return false
}

func adminAccessRemains(ctx context.Context, q db.Querier) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, adminAccessSQL, model.PlatformTypeResourceID(model.PlatformAssignmentRule)).Scan(&ok)
	return ok, err
}

// guardAdminAccess refuses a write (inside its transaction q) that leaves nobody able to change the access rules of
// the platform while somebody could before. Where nobody could already, the write is let through: refusing it would
// block the very changes that repair the state.
func (a *App) guardAdminAccess(ctx context.Context, q db.Querier) error {
	remains, err := adminAccessRemains(ctx, q)
	if err != nil || remains {
		return err
	}
	// The committed state is the one before this write.
	could, err := adminAccessRemains(ctx, a.DB)
	if err != nil {
		return err
	}
	if could {
		return apperr.Conflict(i18n.LastAdminAccess)
	}
	return nil
}
