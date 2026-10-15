package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

func ptr[T any](v T) *T { return &v }

func ruleOn(id string, typ uuid.UUID, subject *string) model.AssignmentRule {
	return model.AssignmentRule{ID: uuid.MustParse(id), TypeResourceID: &typ, Subject: subject, Enabled: true, Effect: model.EffectAllow}
}

func idsOf(rules []model.AssignmentRule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.ID.String())
	}
	return out
}

func TestSnapshotApplicableKeepsTheOrderOfTheQueries(t *testing.T) {
	typ, other := uuid.New(), uuid.New()
	now := time.Now()
	snap := &snapshot{direct: map[string][]model.AssignmentRule{}}
	add := func(r model.AssignmentRule) {
		if r.Subject != nil {
			snap.direct[*r.Subject] = append(snap.direct[*r.Subject], r)
		} else {
			snap.scanned = append(snap.scanned, r)
		}
	}
	add(ruleOn("00000000-0000-0000-0000-000000000003", typ, ptr("alice")))
	add(ruleOn("00000000-0000-0000-0000-000000000001", typ, ptr("group:g")))
	add(ruleOn("00000000-0000-0000-0000-000000000002", typ, ptr("bob")))
	add(ruleOn("00000000-0000-0000-0000-000000000004", other, ptr("alice"))) // a type nobody reaches
	add(ruleOn("00000000-0000-0000-0000-000000000005", typ, nil))
	add(ruleOn("00000000-0000-0000-0000-000000000006", typ, nil))
	expired := ruleOn("00000000-0000-0000-0000-000000000007", typ, ptr("alice"))
	expired.ExpiresAt = ptr(now.Add(-time.Second))
	add(expired)
	future := ruleOn("00000000-0000-0000-0000-000000000008", typ, nil)
	future.ValidFrom = ptr(now.Add(time.Hour))
	add(future)
	role := model.AssignmentRule{ID: uuid.MustParse("00000000-0000-0000-0000-000000000009"), RoleID: ptr(uuid.New()), Subject: ptr("alice"), Enabled: true}
	add(role)

	direct, scanned := snap.applicable(map[uuid.UUID]bool{typ: true}, []string{"alice", "group:g"}, now)
	// The rules of the subjects of the identity set, by id; then the scanned ones, by id (the order of the SQL).
	if got, want := idsOf(direct), []string{
		"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000009",
	}; !slices.Equal(got, want) {
		t.Errorf("direct = %v, want %v", got, want)
	}
	// The scanned rules keep the order of the snapshot, which loadSnapshot reads ORDER BY id.
	if got, want := idsOf(scanned), []string{"00000000-0000-0000-0000-000000000005", "00000000-0000-0000-0000-000000000006"}; !slices.Equal(got, want) {
		t.Errorf("scanned = %v, want %v", got, want)
	}
}

func TestSnapshotTTLIsTheNextValidityBoundary(t *testing.T) {
	typ, other := uuid.New(), uuid.New()
	now := time.Now()
	snap := &snapshot{bounds: []validityBound{
		{at: now.Add(-time.Hour), typeID: &typ},             // past
		{at: now.Add(30 * time.Minute), typeID: &other},     // a type nobody reaches
		{at: now.Add(20 * time.Second), typeID: &typ},       // the next one
		{at: now.Add(10 * time.Second), role: true},         // a role assignment counts for every type
		{at: now.Add(5 * time.Second), member: true},        // so does a membership
		{at: now.Add(500 * time.Millisecond), typeID: &typ}, // under a second: a second at least
	}}
	relevant := map[uuid.UUID]bool{typ: true}
	if got := snap.ttl(relevant, now); got != time.Second {
		t.Errorf("ttl = %v, want the one-second floor", got)
	}
	snap.bounds = snap.bounds[:5]
	if got := snap.ttl(relevant, now); got != 5*time.Second {
		t.Errorf("ttl = %v, want 5s (the membership)", got)
	}
	snap.bounds = snap.bounds[:2]
	if got := snap.ttl(relevant, now); got != ResultCacheTTL {
		t.Errorf("ttl = %v, want the default %v when nothing is near", got, ResultCacheTTL)
	}
}

func TestSnapshotExpandRolesReplacesAssignmentsByTheirItems(t *testing.T) {
	typ, deadType, offType := uuid.New(), uuid.New(), uuid.New()
	roleID := uuid.New()
	graph := newTypeGraph([]*TypeNode{{ID: typ, Name: "t"}, {ID: deadType, Name: "d", Deleted: true}, {ID: offType, Name: "o"}})
	snap := &snapshot{graph: graph, roleItems: map[uuid.UUID][]model.RolePermission{roleID: {
		{ID: uuid.New(), RoleID: roleID, TypeResourceID: typ, Access: []string{"read"}},
		{ID: uuid.New(), RoleID: roleID, TypeResourceID: deadType, Access: []string{"read"}}, // deleted type: no effect
		{ID: uuid.New(), RoleID: roleID, TypeResourceID: offType, Access: []string{"read"}},  // type the service does not reach
	}}}
	assignment := model.AssignmentRule{ID: uuid.New(), RoleID: &roleID, Subject: ptr("alice"), Effect: model.EffectDeny}
	inline := ruleOn("00000000-0000-0000-0000-000000000001", typ, ptr("alice"))

	got := snap.expandRoles([]model.AssignmentRule{inline, assignment}, map[uuid.UUID]bool{typ: true, deadType: true})
	if len(got) != 2 {
		t.Fatalf("got %d rules, want the inline rule and one item", len(got))
	}
	item := got[1]
	if item.ID != assignment.ID || item.Effect != model.EffectDeny || *item.TypeResourceID != typ || !slices.Equal(item.Access, []string{"read"}) {
		t.Errorf("the item keeps the assignment's id and effect and takes the item's type and access: %+v", item)
	}
}

func TestSnapshotLookupsOfServices(t *testing.T) {
	id := uuid.New()
	snap := &snapshot{
		services: map[uuid.UUID]*model.Service{id: {ID: id, Slug: "org", Name: "Org"}},
		bySlug:   map[string]uuid.UUID{"org": id},
	}
	if got, err := snap.serviceID("org"); err != nil || got != id {
		t.Errorf("serviceID = %v, %v", got, err)
	}
	if _, err := snap.serviceID("gone"); err == nil {
		t.Error("an unknown slug must be a not found")
	}
	svc, err := snap.service(id)
	if err != nil {
		t.Fatal(err)
	}
	svc.Name = "changed" // the caller's copy
	if snap.services[id].Name != "Org" {
		t.Error("a caller changed the service of the snapshot")
	}
	if _, err := snap.service(uuid.New()); err == nil {
		t.Error("an unknown id must be a not found")
	}
}
