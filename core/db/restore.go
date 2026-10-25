package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/model"
)

// RestoreReport says what RestoreAdminAccess did.
type RestoreReport struct {
	// DisabledDenials is the number of deny rules on the platform policies that were switched off.
	DisabledDenials int
	// Members is the number of membership rows that now put someone in the admin group.
	Members int
}

// RestoreAdminAccess puts the access of the platform administrators back, for the case where nobody can manage Licetum
// any more (the admin group was emptied or deleted, its rules were removed or switched off, a denial covers them).
// The seed does not do this on purpose — an admin who removed the access must not get it back by a deploy — so this is
// a command somebody with access to the database runs deliberately (`licetum restore-admin`).
//
// It makes sure the platform service, its policy types and their sources exist and are not deleted, that the admin
// group exists with a membership for PLATFORM__ADMIN_CLAIM_VALUES and for each of users (matched by the claim of
// ADMIN_AUTH__SUBJECT_CLAIM, preferred_username by default),
// that each policy has its admin rule in force (allow, read and write, the whole type, no guard), and it switches off the
// deny rules that cover a policy. Everything else is left as it is. Safe to re-run.
func RestoreAdminAccess(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, users []string) (RestoreReport, error) {
	p := cfg.Platform
	var report RestoreReport
	if !cfg.AdminAuth.IsConfigured() {
		return report, errors.New("ADMIN_AUTH__* is not configured: the self-service cannot be restored without it " +
			"(ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE, ADMIN_AUTH__JWKS_URL)")
	}
	if len(p.AdminClaimValues) == 0 && len(users) == 0 {
		return report, errors.New("nobody to make administrator: set PLATFORM__ADMIN_CLAIM_VALUES or pass -user")
	}
	// Creates what is missing (a fresh database, a policy added by a newer release).
	if err := SeedPlatform(ctx, pool, cfg); err != nil {
		return report, err
	}

	typeIDs := make([]string, 0, len(model.PlatformTypes))
	syncIDs := make([]string, 0, len(model.PlatformTypes))
	for _, t := range model.PlatformTypes {
		typeIDs = append(typeIDs, model.PlatformTypeResourceID(t).String())
		syncIDs = append(syncIDs, model.PlatformSynchronizerID(t).String())
	}

	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		report = RestoreReport{}
		if _, err := tx.Exec(ctx, `UPDATE service SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`,
			model.PlatformServiceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE type_resource SET deleted_at = NULL WHERE id::text = ANY($1) AND deleted_at IS NOT NULL`,
			typeIDs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE synchronizer s SET deleted_at = NULL
			WHERE s.id::text = ANY($1) AND s.deleted_at IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM synchronizer o
			                  WHERE o.type_resource_id = s.type_resource_id AND o.deleted_at IS NULL)`, syncIDs); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `INSERT INTO user_group (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
			model.PlatformAdminGroupID, p.AdminGroupName); err != nil {
			if IsUniqueViolation(err) {
				return fmt.Errorf("a group named %q already exists — set another PLATFORM__ADMIN_GROUP_NAME", p.AdminGroupName)
			}
			return err
		}

		upsertMember := func(id any, predicate []model.PredicateCondition) error {
			raw, err := json.Marshal(predicate)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO group_member (id, group_id, member_group_id, predicate, enabled)
				VALUES ($1, $2, NULL, $3::jsonb, true)
				ON CONFLICT (id) DO UPDATE SET group_id = EXCLUDED.group_id, member_group_id = NULL,
				    predicate = EXCLUDED.predicate, enabled = true, valid_from = NULL, expires_at = NULL`,
				id, model.PlatformAdminGroupID, string(raw))
			if err == nil {
				report.Members++
			}
			return err
		}
		if len(p.AdminClaimValues) > 0 {
			if err := upsertMember(model.PlatformAdminGroupMemberID, []model.PredicateCondition{
				{Attr: p.AdminClaim, Op: model.OpIn, Value: model.PredicateValue{List: p.AdminClaimValues, IsList: true}},
			}); err != nil {
				return err
			}
		}
		subjectClaim := cfg.AdminAuth.SubjectClaim
		if subjectClaim == "" {
			subjectClaim = config.DefaultSubjectClaim
		}
		for _, user := range users {
			if err := upsertMember(model.PlatformRestoredMemberID(user), []model.PredicateCondition{
				{Attr: subjectClaim, Op: model.OpEq, Value: model.PredicateValue{Str: user}},
			}); err != nil {
				return err
			}
		}

		for _, t := range model.PlatformTypes {
			if _, err := tx.Exec(ctx, `
				INSERT INTO assignment_rule (id, type_resource_id, resource_id, access, subject, predicate, enabled)
				VALUES ($1, $2, NULL, $3, $4, '[]'::jsonb, true)
				ON CONFLICT (id) DO UPDATE SET
				    type_resource_id = EXCLUDED.type_resource_id, resource_id = NULL, resource_from_claim = NULL,
				    resource_where = NULL, role_id = NULL, access = EXCLUDED.access, subject = EXCLUDED.subject,
				    predicate = '[]'::jsonb, member_of_group_id = NULL, enabled = true, effect = 'allow',
				    exception = false, valid_from = NULL, expires_at = NULL, requires_predicate = '[]'::jsonb,
				    requires_group_id = NULL, on_missing_attribute = 'skip'`,
				model.PlatformAdminRuleID(t), model.PlatformTypeResourceID(t), adminAccess,
				model.GroupSubject(model.PlatformAdminGroupID),
			); err != nil {
				return fmt.Errorf("rule of policy %s: %w", t, err)
			}
		}

		tag, err := tx.Exec(ctx, `
			UPDATE assignment_rule SET enabled = false
			WHERE enabled AND effect = 'deny' AND type_resource_id::text = ANY($1)`, typeIDs)
		if err != nil {
			return err
		}
		report.DisabledDenials = int(tag.RowsAffected())
		// The rules changed: every replica must read them again.
		_, bumpErr := BumpRevision(ctx, tx, ScopeRules)
		return bumpErr
	})
	return report, err
}
