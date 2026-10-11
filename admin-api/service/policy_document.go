package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The policy document (see model.PolicyDocument) names what the policy refers to. ExportPolicy writes the document of a
// service from the database; PlanPolicyImport works out the operations that make the database match a document.

// policyNames maps the ids of the policy to the names of the document, and back.
type policyNames struct {
	typeName  map[uuid.UUID]string
	typeID    map[string]uuid.UUID
	groupName map[uuid.UUID]string
	groupID   map[string]uuid.UUID
	roleName  map[uuid.UUID]string
	roleID    map[string]uuid.UUID
}

// loadPolicyNames reads the names of the resource types (as service/type), of the groups and of the roles.
func (a *App) loadPolicyNames(ctx context.Context) (*policyNames, error) {
	types, err := a.namedRows(ctx, `
		SELECT t.id, s.slug || '/' || t.name FROM type_resource t
		JOIN service s ON s.id = t.service_id
		WHERE t.deleted_at IS NULL AND s.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	groups, err := a.namedRows(ctx, `SELECT id, name FROM user_group`)
	if err != nil {
		return nil, err
	}
	roles, err := a.namedRows(ctx, `SELECT id, name FROM role`)
	if err != nil {
		return nil, err
	}
	return &policyNames{
		typeName: types, typeID: invertNames(types),
		groupName: groups, groupID: invertNames(groups),
		roleName: roles, roleID: invertNames(roles),
	}, nil
}

// namedRows reads (id, name) rows into a map from the id to the name.
func (a *App) namedRows(ctx context.Context, sql string) (map[uuid.UUID]string, error) {
	rows, err := a.DB.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func invertNames(m map[uuid.UUID]string) map[string]uuid.UUID {
	out := make(map[string]uuid.UUID, len(m))
	for id, name := range m {
		out[name] = id
	}
	return out
}

// ruleDocName is the name a rule has in the document: its own name, or rule-<id> when it has none.
func ruleDocName(r model.AssignmentRule) string {
	if r.Name != nil && *r.Name != "" {
		return *r.Name
	}
	return "rule-" + r.ID.String()
}

// utcPtr returns a time in UTC, so that the same instant compares equal whatever its zone.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func boolPtr(b bool) *bool { return &b }

func idOf(id uuid.UUID) *uuid.UUID { return &id }

// ExportPolicy returns the policy of a service as a document: its rules, the groups and roles they use (with the
// groups nested in them), and the access groups of the resource types involved.
func (a *App) ExportPolicy(ctx context.Context, serviceID uuid.UUID) (*model.PolicyDocument, error) {
	svc, err := a.GetExistingService(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	names, err := a.loadPolicyNames(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := a.serviceRulesAll(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	// The roles the rules grant, the groups they name, and the groups nested in those, and so on.
	var roleIDs, groupIDs []uuid.UUID
	seenRole := map[uuid.UUID]bool{}
	seenGroup := map[uuid.UUID]bool{}
	needGroup := func(id uuid.UUID) {
		if !seenGroup[id] {
			seenGroup[id] = true
			groupIDs = append(groupIDs, id)
		}
	}
	for _, r := range rules {
		if r.RoleID != nil && !seenRole[*r.RoleID] {
			seenRole[*r.RoleID] = true
			roleIDs = append(roleIDs, *r.RoleID)
		}
		for _, g := range groupsOfRule(r) {
			needGroup(g)
		}
	}

	doc := &model.PolicyDocument{Format: model.PolicyDocumentFormat, Service: svc.Slug,
		AccessGroups: []model.PolicyAccessGroup{}, Groups: []model.PolicyGroup{}, Roles: []model.PolicyRole{}, Rules: []model.PolicyRule{}}
	for i := 0; i < len(groupIDs); i++ {
		members, err := a.ListGroupMembers(ctx, groupIDs[i])
		if err != nil {
			return nil, err
		}
		group := model.PolicyGroup{Name: names.groupName[groupIDs[i]], Members: []model.PolicyGroupMember{}}
		for _, m := range members {
			if m.MemberGroupID != nil {
				needGroup(*m.MemberGroupID)
			}
			group.Members = append(group.Members, names.member(m))
		}
		doc.Groups = append(doc.Groups, group)
	}
	slices.SortFunc(doc.Groups, func(x, y model.PolicyGroup) int { return strings.Compare(x.Name, y.Name) })

	typeIDs := map[uuid.UUID]bool{}
	for _, id := range roleIDs {
		items, err := a.ListRolePermissions(ctx, id)
		if err != nil {
			return nil, err
		}
		role := model.PolicyRole{Name: names.roleName[id], Items: []model.PolicyRoleItem{}}
		for _, it := range items {
			typeIDs[it.TypeResourceID] = true
			role.Items = append(role.Items, names.item(it.RolePermission))
		}
		doc.Roles = append(doc.Roles, role)
	}
	slices.SortFunc(doc.Roles, func(x, y model.PolicyRole) int { return strings.Compare(x.Name, y.Name) })

	for _, r := range rules {
		if r.TypeResourceID != nil {
			typeIDs[*r.TypeResourceID] = true
		}
		doc.Rules = append(doc.Rules, names.rule(r))
	}
	slices.SortFunc(doc.Rules, func(x, y model.PolicyRule) int { return strings.Compare(x.Name, y.Name) })

	// The access groups of the types of the service, and of the types the roles and the rules point at.
	ids, err := a.serviceTypeIDs(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		typeIDs[id] = true
	}
	groups, err := a.accessGroupsOf(ctx, typeIDs)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		doc.AccessGroups = append(doc.AccessGroups, model.PolicyAccessGroup{Type: names.typeName[g.TypeResourceID], Name: g.Name, Levels: g.Levels})
	}
	slices.SortFunc(doc.AccessGroups, func(x, y model.PolicyAccessGroup) int {
		return strings.Compare(x.Type+"/"+x.Name, y.Type+"/"+y.Name)
	})
	return doc, nil
}

// serviceTypeIDs returns the live resource types of a service.
func (a *App) serviceTypeIDs(ctx context.Context, serviceID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := a.DB.Query(ctx, `SELECT id FROM type_resource WHERE service_id = $1 AND deleted_at IS NULL`, serviceID)
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

// accessGroupsOf returns the access groups of the given resource types.
func (a *App) accessGroupsOf(ctx context.Context, typeIDs map[uuid.UUID]bool) ([]model.AccessGroup, error) {
	if len(typeIDs) == 0 {
		return nil, nil
	}
	rows, err := a.DB.Query(ctx, `SELECT `+accessGroupColumns+` FROM access_group WHERE type_resource_id = ANY($1)`, engine.Keys(typeIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AccessGroup
	for rows.Next() {
		g, err := scanAccessGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// allAccessGroups reads every access group of the installation.
func (a *App) allAccessGroups(ctx context.Context) ([]model.AccessGroup, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+accessGroupColumns+` FROM access_group`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AccessGroup
	for rows.Next() {
		g, err := scanAccessGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// member converts a membership to the document.
func (n *policyNames) member(m model.GroupMember) model.PolicyGroupMember {
	out := model.PolicyGroupMember{Predicate: m.Predicate, Enabled: boolPtr(m.Enabled),
		ValidFrom: utcPtr(m.ValidFrom), ExpiresAt: utcPtr(m.ExpiresAt)}
	if m.MemberGroupID != nil {
		name := n.groupName[*m.MemberGroupID]
		out.Group = &name
	}
	return out
}

// item converts an item of a role to the document.
func (n *policyNames) item(p model.RolePermission) model.PolicyRoleItem {
	return model.PolicyRoleItem{Type: n.typeName[p.TypeResourceID], ResourceID: p.ResourceID,
		ResourceFromClaim: n.claim(p.ResourceFromClaim), ResourceWhere: p.ResourceWhere, Access: p.Access}
}

// claim converts a resource_from_claim to the document: its lookup type is named.
func (n *policyNames) claim(c *model.ResourceFromClaim) *model.PolicyResourceFromClaim {
	if c == nil {
		return nil
	}
	return &model.PolicyResourceFromClaim{Claim: c.Claim, LookupType: n.typeName[c.LookupTypeResourceID], MatchBy: c.MatchBy}
}

// rule converts a rule to the document: the groups, the roles and the types are named, and a group subject is
// group:<name>.
func (n *policyNames) rule(r model.AssignmentRule) model.PolicyRule {
	out := model.PolicyRule{
		Name: ruleDocName(r), Description: r.Description, Enabled: boolPtr(r.Enabled), Effect: r.Effect,
		Exception: r.Exception, ValidFrom: utcPtr(r.ValidFrom), ExpiresAt: utcPtr(r.ExpiresAt),
		OnMissingAttribute: r.OnMissingAttribute, RequiresPredicate: r.RequiresPredicate, SubjectKind: r.SubjectKind,
		ResourceID: r.ResourceID, ResourceWhere: r.ResourceWhere, Access: r.Access, Predicate: r.Predicate,
		ResourceFromClaim: n.claim(r.ResourceFromClaim),
	}
	if r.RoleID != nil {
		role := n.roleName[*r.RoleID]
		out.Role = &role
	} else if r.TypeResourceID != nil {
		typ := n.typeName[*r.TypeResourceID]
		out.Type = &typ
	}
	if r.MemberOfGroupID != nil {
		group := n.groupName[*r.MemberOfGroupID]
		out.MemberOf = &group
	}
	if r.RequiresGroupID != nil {
		group := n.groupName[*r.RequiresGroupID]
		out.RequiresGroup = &group
	}
	if r.Subject != nil {
		subject := *r.Subject
		if id, ok := strings.CutPrefix(subject, model.GroupSubjectPrefix); ok {
			if gid, err := uuid.Parse(id); err == nil && n.groupName[gid] != "" {
				subject = model.GroupSubjectPrefix + n.groupName[gid]
			}
		}
		out.Subject = &subject
	}
	return out
}

// PlanPolicyImport works out the operations that make the policy of a service match a document, and counts them. It
// applies nothing: RunBatch does, once the caller may write what the operations write.
//
// The entities the document names are matched by their names. A group, a role or an access group that Licetum does not
// have is created; one that differs is rewritten (its memberships or items are replaced). A rule is matched by its name,
// or by its id when it is named rule-<id>, and is rewritten when it differs. With prune, the rules of the service that the
// document does not have are deleted. The ids of the new entities are chosen here, so that the operations of the same
// batch can name them.
func (a *App) PlanPolicyImport(ctx context.Context, serviceID uuid.UUID, doc *model.PolicyDocument, prune bool) (*model.PolicyImportPlan, error) {
	if doc.Format != model.PolicyDocumentFormat {
		return nil, apperr.Unprocessable(i18n.PolicyFormatInvalid, doc.Format)
	}
	svc, err := a.GetExistingService(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	if doc.Service != svc.Slug {
		return nil, apperr.Unprocessable(i18n.PolicyServiceMismatch, doc.Service, svc.Slug)
	}
	if err := checkDocumentNames(doc); err != nil {
		return nil, err
	}
	names, err := a.loadPolicyNames(ctx)
	if err != nil {
		return nil, err
	}
	im := &importer{a: a, names: names, plan: &model.PolicyImportPlan{Operations: []model.BatchOperation{}}}
	if err := im.run(ctx, serviceID, doc, prune); err != nil {
		return nil, err
	}
	return im.plan, nil
}

// checkDocumentNames refuses a document that names two entities of one kind alike, or a membership that is both a
// nested group and a predicate.
func checkDocumentNames(doc *model.PolicyDocument) error {
	var groups, roles, rules, accessGroups []string
	for _, g := range doc.Groups {
		groups = append(groups, g.Name)
		for _, m := range g.Members {
			if m.Group != nil && len(m.Predicate) > 0 {
				return apperr.Unprocessable(i18n.PolicyMemberInvalid, g.Name)
			}
		}
	}
	for _, r := range doc.Roles {
		roles = append(roles, r.Name)
	}
	for _, r := range doc.Rules {
		rules = append(rules, r.Name)
	}
	for _, g := range doc.AccessGroups {
		accessGroups = append(accessGroups, g.Type+"/"+g.Name)
	}
	for _, check := range []struct {
		kind  string
		names []string
	}{{"group", groups}, {"role", roles}, {"rule", rules}, {"access_group", accessGroups}} {
		seen := map[string]bool{}
		for _, n := range check.names {
			if seen[n] {
				return apperr.Unprocessable(i18n.PolicyNameDuplicate, check.kind, n)
			}
			seen[n] = true
		}
	}
	return nil
}

// outcome is what an import does to one entity of the document.
type outcome int

const (
	outcomeCreated outcome = iota
	outcomeUpdated
	outcomeDeleted
	outcomeUnchanged
)

// importer carries the state of one plan.
type importer struct {
	a     *App
	names *policyNames
	plan  *model.PolicyImportPlan
	// the new ids of the groups and roles the document creates, by name
	groupIDs map[string]uuid.UUID
	roleIDs  map[string]uuid.UUID
}

// add appends an operation to the plan.
func (im *importer) add(op model.BatchOperation) {
	im.plan.Operations = append(im.plan.Operations, op)
}

// tally counts what an import does to one entity.
func (im *importer) tally(o outcome) {
	c := &im.plan.Counts
	switch o {
	case outcomeCreated:
		c.Created++
	case outcomeUpdated:
		c.Updated++
	case outcomeDeleted:
		c.Deleted++
	case outcomeUnchanged:
		c.Unchanged++
	}
}

// groupRef resolves a group name of the document or of the installation to its id.
func (im *importer) groupRef(name string) (uuid.UUID, error) {
	if id, ok := im.groupIDs[name]; ok {
		return id, nil
	}
	if id, ok := im.names.groupID[name]; ok {
		return id, nil
	}
	return uuid.Nil, apperr.Unprocessable(i18n.PolicyReferenceMissing, "group", name)
}

// roleRef resolves a role name of the document or of the installation to its id.
func (im *importer) roleRef(name string) (uuid.UUID, error) {
	if id, ok := im.roleIDs[name]; ok {
		return id, nil
	}
	if id, ok := im.names.roleID[name]; ok {
		return id, nil
	}
	return uuid.Nil, apperr.Unprocessable(i18n.PolicyReferenceMissing, "role", name)
}

// typeRef resolves a service/type name to the id of the resource type.
func (im *importer) typeRef(name string) (uuid.UUID, error) {
	if id, ok := im.names.typeID[name]; ok {
		return id, nil
	}
	return uuid.Nil, apperr.Unprocessable(i18n.PolicyReferenceMissing, "type", name)
}

// run builds the plan: the access groups, the roles and the groups first (created, then given their items and
// memberships), and the rules last, since they name all of them.
func (im *importer) run(ctx context.Context, serviceID uuid.UUID, doc *model.PolicyDocument, prune bool) error {
	im.groupIDs = map[string]uuid.UUID{}
	im.roleIDs = map[string]uuid.UUID{}
	for _, g := range doc.Groups {
		if _, ok := im.names.groupID[g.Name]; !ok {
			im.groupIDs[g.Name] = uuid.New()
		}
	}
	for _, r := range doc.Roles {
		if _, ok := im.names.roleID[r.Name]; !ok {
			im.roleIDs[r.Name] = uuid.New()
		}
	}
	if err := im.accessGroups(ctx, doc.AccessGroups); err != nil {
		return err
	}
	if err := im.roles(ctx, doc.Roles); err != nil {
		return err
	}
	if err := im.groups(ctx, doc.Groups); err != nil {
		return err
	}
	return im.rules(ctx, serviceID, doc.Rules, prune)
}

// accessGroups creates the access groups that the installation lacks, and rewrites the levels of the ones that differ.
func (im *importer) accessGroups(ctx context.Context, want []model.PolicyAccessGroup) error {
	all, err := im.a.allAccessGroups(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]model.AccessGroup{}
	for _, g := range all {
		byKey[im.names.typeName[g.TypeResourceID]+"/"+g.Name] = g
	}
	for _, w := range want {
		typeID, err := im.typeRef(w.Type)
		if err != nil {
			return err
		}
		body := model.AccessGroupInput{Name: w.Name, Levels: w.Levels}
		have, ok := byKey[w.Type+"/"+w.Name]
		switch {
		case !ok:
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchAccessGroup, ID: idOf(uuid.New()),
				Parent: idOf(typeID), Body: body})
			im.tally(outcomeCreated)
		case canonical(have.Levels) == canonical(w.Levels):
			im.tally(outcomeUnchanged)
		default:
			im.add(model.BatchOperation{Op: model.BatchUpdate, Entity: model.BatchAccessGroup, ID: idOf(have.ID),
				Parent: idOf(typeID), Body: body})
			im.tally(outcomeUpdated)
		}
	}
	return nil
}

// roles creates the roles that the installation lacks, and replaces the items of the ones that differ.
func (im *importer) roles(ctx context.Context, want []model.PolicyRole) error {
	for _, w := range want {
		desired := make([]string, 0, len(w.Items))
		for _, it := range w.Items {
			desired = append(desired, canonical(normalizeItem(it)))
		}
		slices.Sort(desired)

		id, exists := im.names.roleID[w.Name]
		if !exists {
			id = im.roleIDs[w.Name]
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchRole, ID: idOf(id),
				Body: model.RoleInput{Name: w.Name}})
			im.tally(outcomeCreated)
		} else {
			items, err := im.a.ListRolePermissions(ctx, id)
			if err != nil {
				return err
			}
			current := make([]string, 0, len(items))
			for _, it := range items {
				current = append(current, canonical(normalizeItem(im.names.item(it.RolePermission))))
			}
			slices.Sort(current)
			if slices.Equal(current, desired) {
				im.tally(outcomeUnchanged)
				continue
			}
			for _, it := range items {
				im.add(model.BatchOperation{Op: model.BatchDelete, Entity: model.BatchRolePermission, ID: idOf(it.ID), Parent: idOf(id)})
			}
			im.tally(outcomeUpdated)
		}
		for _, it := range w.Items {
			input, err := im.itemInput(it)
			if err != nil {
				return err
			}
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchRolePermission, ID: idOf(uuid.New()),
				Parent: idOf(id), Body: input})
		}
	}
	return nil
}

// itemInput turns an item of the document into the input of its endpoint.
func (im *importer) itemInput(it model.PolicyRoleItem) (model.RolePermissionInput, error) {
	typeID, err := im.typeRef(it.Type)
	if err != nil {
		return model.RolePermissionInput{}, err
	}
	in := model.RolePermissionInput{TypeResourceID: typeID, ResourceID: it.ResourceID,
		ResourceWhere: it.ResourceWhere, Access: it.Access}
	if in.Access == nil {
		in.Access = []string{}
	}
	if it.ResourceFromClaim != nil {
		claim, err := im.claimInput(*it.ResourceFromClaim)
		if err != nil {
			return model.RolePermissionInput{}, err
		}
		in.ResourceFromClaim = claim
	}
	return in, nil
}

// claimInput turns a resource_from_claim of the document into its input.
func (im *importer) claimInput(c model.PolicyResourceFromClaim) (*model.ResourceFromClaim, error) {
	lookup, err := im.typeRef(c.LookupType)
	if err != nil {
		return nil, err
	}
	matchBy := c.MatchBy
	if matchBy == "" {
		matchBy = model.MatchByName
	}
	return &model.ResourceFromClaim{Claim: c.Claim, LookupTypeResourceID: lookup, MatchBy: matchBy}, nil
}

// groups creates the groups that the installation lacks, and replaces the memberships of the ones that differ. The
// memberships are written after every group exists, since a membership may nest another group.
func (im *importer) groups(ctx context.Context, want []model.PolicyGroup) error {
	type pending struct {
		id      uuid.UUID
		members []model.PolicyGroupMember
	}
	var work []pending
	for _, w := range want {
		desired := make([]string, 0, len(w.Members))
		for _, m := range w.Members {
			desired = append(desired, canonical(normalizeMember(m)))
		}
		slices.Sort(desired)

		id, exists := im.names.groupID[w.Name]
		if !exists {
			id = im.groupIDs[w.Name]
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchGroup, ID: idOf(id),
				Body: model.GroupInput{Name: w.Name}})
			im.tally(outcomeCreated)
			work = append(work, pending{id: id, members: w.Members})
			continue
		}
		members, err := im.a.ListGroupMembers(ctx, id)
		if err != nil {
			return err
		}
		current := make([]string, 0, len(members))
		for _, m := range members {
			current = append(current, canonical(normalizeMember(im.names.member(m))))
		}
		slices.Sort(current)
		if slices.Equal(current, desired) {
			im.tally(outcomeUnchanged)
			continue
		}
		for _, m := range members {
			im.add(model.BatchOperation{Op: model.BatchDelete, Entity: model.BatchGroupMember, ID: idOf(m.ID), Parent: idOf(id)})
		}
		im.tally(outcomeUpdated)
		work = append(work, pending{id: id, members: w.Members})
	}
	for _, p := range work {
		for _, m := range p.members {
			input, err := im.memberInput(m)
			if err != nil {
				return err
			}
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchGroupMember, ID: idOf(uuid.New()),
				Parent: idOf(p.id), Body: input})
		}
	}
	return nil
}

// memberInput turns a membership of the document into the input of its endpoint.
func (im *importer) memberInput(m model.PolicyGroupMember) (model.GroupMemberInput, error) {
	in := model.GroupMemberInput{Predicate: m.Predicate, Enabled: boolPtr(m.Enabled == nil || *m.Enabled),
		ValidFrom: m.ValidFrom, ExpiresAt: m.ExpiresAt}
	if in.Predicate == nil {
		in.Predicate = []model.PredicateCondition{}
	}
	if m.Group != nil {
		id, err := im.groupRef(*m.Group)
		if err != nil {
			return model.GroupMemberInput{}, err
		}
		in.MemberGroupID = &id
	}
	return in, nil
}

// rules creates the rules that the installation lacks, rewrites the ones that differ, and with prune deletes the rules
// of the service that the document does not have.
func (im *importer) rules(ctx context.Context, serviceID uuid.UUID, want []model.PolicyRule, prune bool) error {
	current, err := im.a.serviceRulesAll(ctx, serviceID)
	if err != nil {
		return err
	}
	byName := map[string][]model.AssignmentRule{}
	byID := map[uuid.UUID]model.AssignmentRule{}
	for _, r := range current {
		byName[ruleDocName(r)] = append(byName[ruleDocName(r)], r)
		byID[r.ID] = r
	}
	wanted := map[string]bool{}
	matched := map[uuid.UUID]bool{}
	for _, w := range want {
		wanted[w.Name] = true
		have, err := matchRule(w, byName, byID)
		if err != nil {
			return err
		}
		desired := normalizeRule(w)
		input, err := im.ruleInput(desired)
		if err != nil {
			return err
		}
		if have == nil {
			id := uuid.New()
			if fromName, ok := strings.CutPrefix(w.Name, "rule-"); ok {
				if parsed, err := uuid.Parse(fromName); err == nil {
					id = parsed
				}
			}
			im.add(model.BatchOperation{Op: model.BatchCreate, Entity: model.BatchAssignmentRule, ID: idOf(id), Body: input})
			im.tally(outcomeCreated)
			continue
		}
		matched[have.ID] = true
		if canonical(normalizeRule(im.names.rule(*have))) == canonical(desired) {
			im.tally(outcomeUnchanged)
			continue
		}
		im.add(model.BatchOperation{Op: model.BatchUpdate, Entity: model.BatchAssignmentRule, ID: idOf(have.ID), Body: input})
		im.tally(outcomeUpdated)
	}
	if !prune {
		return nil
	}
	for _, r := range current {
		if !matched[r.ID] && !wanted[ruleDocName(r)] {
			im.add(model.BatchOperation{Op: model.BatchDelete, Entity: model.BatchAssignmentRule, ID: idOf(r.ID)})
			im.tally(outcomeDeleted)
		}
	}
	return nil
}

// matchRule finds the rule of the installation that a rule of the document stands for: by its name, or by the id of a
// rule-<id> name. A name that two rules of the service share matches neither: the import refuses to guess.
func matchRule(w model.PolicyRule, byName map[string][]model.AssignmentRule, byID map[uuid.UUID]model.AssignmentRule) (*model.AssignmentRule, error) {
	if fromName, ok := strings.CutPrefix(w.Name, "rule-"); ok {
		if id, err := uuid.Parse(fromName); err == nil {
			if r, found := byID[id]; found {
				return &r, nil
			}
		}
	}
	found := byName[w.Name]
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return &found[0], nil
	}
	return nil, apperr.Unprocessable(i18n.PolicyNameAmbiguous, w.Name)
}

// ruleInput turns a normalized rule of the document into the input of its endpoint.
func (im *importer) ruleInput(w model.PolicyRule) (model.AssignmentRuleInput, error) {
	name := w.Name
	onMissing := w.OnMissingAttribute
	in := model.AssignmentRuleInput{
		Access: w.Access, Predicate: w.Predicate, Enabled: boolPtr(*w.Enabled), Effect: w.Effect,
		Exception: w.Exception, ValidFrom: w.ValidFrom, ExpiresAt: w.ExpiresAt, Name: &name,
		Description: w.Description, OnMissingAttribute: &onMissing, RequiresPredicate: w.RequiresPredicate,
		ResourceID: w.ResourceID, ResourceWhere: w.ResourceWhere, SubjectKind: w.SubjectKind,
	}
	if in.Predicate == nil {
		in.Predicate = []model.PredicateCondition{}
	}
	if in.RequiresPredicate == nil {
		in.RequiresPredicate = []model.PredicateCondition{}
	}
	if w.Role != nil {
		id, err := im.roleRef(*w.Role)
		if err != nil {
			return in, err
		}
		in.RoleID = &id
	} else if w.Type != nil {
		id, err := im.typeRef(*w.Type)
		if err != nil {
			return in, err
		}
		in.TypeResourceID = &id
	}
	if w.ResourceFromClaim != nil {
		claim, err := im.claimInput(*w.ResourceFromClaim)
		if err != nil {
			return in, err
		}
		in.ResourceFromClaim = claim
	}
	if w.Subject != nil {
		subject := *w.Subject
		if group, ok := strings.CutPrefix(subject, model.GroupSubjectPrefix); ok {
			if _, notUUID := uuid.Parse(group); notUUID != nil {
				id, err := im.groupRef(group)
				if err != nil {
					return in, err
				}
				subject = model.GroupSubjectPrefix + id.String()
			}
		}
		in.Subject = &subject
	}
	if w.MemberOf != nil {
		id, err := im.groupRef(*w.MemberOf)
		if err != nil {
			return in, err
		}
		in.MemberOfGroupID = &id
	}
	if w.RequiresGroup != nil {
		id, err := im.groupRef(*w.RequiresGroup)
		if err != nil {
			return in, err
		}
		in.RequiresGroupID = &id
	}
	return in, nil
}

// normalizeRule gives a rule of the document the form that an exported rule has, so that the two compare equal when they
// say the same: defaults made explicit, times in UTC, empty lists as nil.
func normalizeRule(r model.PolicyRule) model.PolicyRule {
	out := r
	if out.Enabled == nil {
		out.Enabled = boolPtr(true)
	}
	if out.Effect == "" {
		out.Effect = model.EffectAllow
	}
	if out.OnMissingAttribute == "" {
		out.OnMissingAttribute = model.OnMissingSkip
		if out.Effect == model.EffectDeny {
			out.OnMissingAttribute = model.OnMissingApply
		}
	}
	if out.Description != nil && *out.Description == "" {
		out.Description = nil
	}
	out.ValidFrom = utcPtr(out.ValidFrom)
	out.ExpiresAt = utcPtr(out.ExpiresAt)
	if out.ResourceFromClaim != nil {
		out.ResourceFromClaim = normalizeClaim(*out.ResourceFromClaim)
	}
	out.Predicate = nilIfEmpty(out.Predicate)
	out.RequiresPredicate = nilIfEmpty(out.RequiresPredicate)
	out.ResourceWhere = nilIfEmpty(out.ResourceWhere)
	return out
}

// normalizeItem gives an item of a role of the document the form of an exported item.
func normalizeItem(it model.PolicyRoleItem) model.PolicyRoleItem {
	out := it
	if out.ResourceFromClaim != nil {
		out.ResourceFromClaim = normalizeClaim(*out.ResourceFromClaim)
	}
	out.ResourceWhere = nilIfEmpty(out.ResourceWhere)
	if out.Access == nil {
		out.Access = []string{}
	}
	return out
}

// normalizeMember gives a membership of the document the form of an exported membership.
func normalizeMember(m model.PolicyGroupMember) model.PolicyGroupMember {
	out := m
	out.Enabled = boolPtr(m.Enabled == nil || *m.Enabled)
	out.ValidFrom = utcPtr(m.ValidFrom)
	out.ExpiresAt = utcPtr(m.ExpiresAt)
	out.Predicate = nilIfEmpty(out.Predicate)
	return out
}

// normalizeClaim makes the default match_by explicit.
func normalizeClaim(c model.PolicyResourceFromClaim) *model.PolicyResourceFromClaim {
	if c.MatchBy == "" {
		c.MatchBy = model.MatchByName
	}
	return &c
}

// nilIfEmpty turns an empty list into nil, so that it compares like an absent one.
func nilIfEmpty[T any](list []T) []T {
	if len(list) == 0 {
		return nil
	}
	return list
}

// canonical is the JSON of a value: two values that say the same compare equal.
func canonical(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}
