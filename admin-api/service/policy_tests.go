package service

import (
	"context"
	"slices"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// RunPolicyTests answers the scenarios of a policy test the way the simulation answers them: the subjects with their
// claims and request attributes, the rules and the data as they are now. It reports which expectations hold, and which
// of the rules in force decided an answer at all (the coverage): a rule that no scenario reaches is one nothing checks.
func (a *App) RunPolicyTests(ctx context.Context, serviceID uuid.UUID, in *model.PolicyTestInput) (*model.PolicyTestReport, error) {
	report := &model.PolicyTestReport{Cases: []model.PolicyTestCaseResult{}}
	decided := map[string]bool{}
	for _, c := range in.Cases {
		requests := make([]model.CheckRequest, len(c.Checks))
		for i, check := range c.Checks {
			requests[i] = model.CheckRequest{Type: check.Type, ResourceID: check.ResourceID, Action: check.Action}
		}
		results, err := a.CheckAccess(ctx, serviceID, c.Subject, engine.NewEnv(c.Claims, c.Context), requests, true, engine.HigherConsistency)
		if err != nil {
			return nil, err
		}
		outcome := model.PolicyTestCaseResult{Name: c.Name, Passed: true, Checks: make([]model.PolicyTestCheckResult, len(c.Checks))}
		for i, check := range c.Checks {
			rules := decidingRules(results[i].Explanation)
			for _, id := range rules {
				decided[id] = true
			}
			passed := (check.Expect == "allow") == results[i].Allowed
			outcome.Checks[i] = model.PolicyTestCheckResult{Index: i, Expect: check.Expect, Decision: results[i].Explanation.Decision,
				Passed: passed, Rules: rules}
			outcome.Passed = outcome.Passed && passed
		}
		if outcome.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Cases = append(report.Cases, outcome)
	}

	ids, err := a.serviceRuleIDs(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	report.Coverage = model.PolicyCoverage{Rules: len(ids), Uncovered: []string{}}
	for _, id := range ids {
		if decided[id.String()] {
			report.Coverage.Covered++
		} else {
			report.Coverage.Uncovered = append(report.Coverage.Uncovered, id.String())
		}
	}
	slices.Sort(report.Coverage.Uncovered)
	return report, nil
}

// decidingRules lists the rules that decided an answer: the granting ones, and the denials and the exceptions that
// lifted them.
func decidingRules(e *model.Explanation) []string {
	seen := map[string]bool{}
	var out []string
	add := func(r model.RuleRef) {
		if id := r.RuleID.String(); !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, g := range e.GrantedBy {
		add(g)
	}
	for _, d := range e.Denials {
		add(d.Rule)
		for _, l := range d.LiftedBy {
			add(l)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// serviceRuleIDs returns the rules in force that apply to the resource types of a service: its inline rules, and the
// role assignments of the roles that have items on those types.
func (a *App) serviceRuleIDs(ctx context.Context, serviceID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT ar.id FROM assignment_rule ar
		JOIN type_resource t ON t.id = ar.type_resource_id
		WHERE `+engine.RuleActiveSQL+` AND ar.role_id IS NULL AND t.service_id = $1 AND t.deleted_at IS NULL
		UNION
		SELECT ar.id FROM assignment_rule ar
		JOIN role_permission rp ON rp.role_id = ar.role_id
		JOIN type_resource t ON t.id = rp.type_resource_id
		WHERE `+engine.RuleActiveSQL+` AND t.service_id = $1 AND t.deleted_at IS NULL
		ORDER BY 1`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// serviceRulesAll returns every rule that applies to the resource types of a service, whether it is in force or not
// (the linter and the export read the rules as they are written).
func (a *App) serviceRulesAll(ctx context.Context, serviceID uuid.UUID) ([]model.AssignmentRule, error) {
	return a.LoadRules(ctx, `
		SELECT `+engine.AssignmentRuleColumns+` FROM assignment_rule ar
		WHERE ar.id IN (
			SELECT ar2.id FROM assignment_rule ar2
			JOIN type_resource t ON t.id = ar2.type_resource_id
			WHERE ar2.role_id IS NULL AND t.service_id = $1 AND t.deleted_at IS NULL
			UNION
			SELECT ar3.id FROM assignment_rule ar3
			JOIN role_permission rp ON rp.role_id = ar3.role_id
			JOIN type_resource t3 ON t3.id = rp.type_resource_id
			WHERE t3.service_id = $1 AND t3.deleted_at IS NULL)
		ORDER BY ar.id`, serviceID)
}
