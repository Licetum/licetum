package engine

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// A * denial denies the levels that flow to a node, and every level that includes one of them: through a read-only
// link it denies read, and approve where approve includes read (found by DECP, seed 16).
func TestAWildcardDenialDeniesTheLevelsThatIncludeAFlowingOne(t *testing.T) {
	in := &Instance{Deny: true, Levels: []string{model.AllLevels}, AnchorType: uuid.New()}
	readOnly := LevelsOf("read")
	implies := map[string][]string{"approve": {"read"}}
	for _, tc := range []struct {
		level string
		want  []string
	}{
		{"read", []string{"read"}},
		{"approve", []string{"read"}},
		{"write", nil},
	} {
		if got := denyMatches(in, readOnly, tc.level, implies); !slices.Equal(got, tc.want) {
			t.Errorf("* through a read-only mask, %s: got %v, want %v", tc.level, got, tc.want)
		}
	}
	if got := denyMatches(in, AllLevels, "write", nil); !slices.Equal(got, []string{"write"}) {
		t.Errorf("* through every level, write: got %v", got)
	}
}

// A denial may cover a node both for real and as assumed (its anchor is a known ancestor, and a missing parent could
// lie below it too): the levels it reaches for real are denied for real and reported once, the others are assumed.
func TestADenialCoveringForRealAndAsAssumedReportsEachLevelOnce(t *testing.T) {
	campus, room, u1 := uuid.New(), uuid.New(), uuid.New()
	readWrite := []string{"read", "write"}
	for _, tc := range []struct {
		name    string
		deny    []string
		implies map[string][]string
		real    LevelSet
		want    []levelVerdict
	}{
		{
			name: "levels",
			deny: readWrite,
			real: LevelsOf("write"),
			want: []levelVerdict{
				{Level: "read", GrantedBy: []int{0}, Denials: []denial{{Inst: 1, Level: "read", Assumed: true}}},
				{Level: "write", GrantedBy: []int{0}, Denials: []denial{{Inst: 1, Level: "write"}}},
			},
		},
		{
			// write includes read: the * reaches write for real, and read, which denies write as well, as assumed.
			name:    "* with write including read, write known",
			deny:    []string{model.AllLevels},
			implies: map[string][]string{"write": {"read"}},
			real:    LevelsOf("write"),
			want: []levelVerdict{
				{Level: "read", GrantedBy: []int{0}, Denials: []denial{{Inst: 1, Level: "read", Assumed: true}}},
				{Level: "write", GrantedBy: []int{0}, Denials: []denial{
					{Inst: 1, Level: "write"},
					{Inst: 1, Level: "read", Assumed: true},
				}},
			},
		},
		{
			// The * reaches read for real, and with it write; only the level write itself is assumed.
			name:    "* with write including read, read known",
			deny:    []string{model.AllLevels},
			implies: map[string][]string{"write": {"read"}},
			real:    LevelsOf("read"),
			want: []levelVerdict{
				{Level: "read", GrantedBy: []int{0}, Denials: []denial{{Inst: 1, Level: "read"}}},
				{Level: "write", GrantedBy: []int{0}, Denials: []denial{
					{Inst: 1, Level: "read"},
					{Inst: 1, Level: "write", Assumed: true},
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newTypeGraph([]*TypeNode{
				{ID: campus},
				{ID: room, Parents: []typeEdge{{Parent: campus, Inherit: AllLevels}}, Implies: tc.implies},
			})
			e := newEngine(g, []Instance{
				{Levels: readWrite, AnchorType: room},
				{Deny: true, Levels: tc.deny, AnchorType: campus, AnchorRow: &u1, AnchorExt: "U1"},
			}, nil)
			got := e.decide(room, []Cover{
				{Inst: 0, Mask: AllLevels},
				{Inst: 1, Mask: tc.real},
				{Inst: 1, Mask: LevelsOf(readWrite...), Assumed: true},
			})
			want := nodeVerdict{Blocked: readWrite, Levels: tc.want}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v\nwant %+v", got, want)
			}
		})
	}
}

// TypesBetween is structural: a type on a route no level flows through still lies between, and the order of the links
// does not matter.
func TestTypesBetweenFollowsTheLinksWhateverFlows(t *testing.T) {
	zone, team, space, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := newTypeGraph([]*TypeNode{
		{ID: zone},
		{ID: team, Parents: []typeEdge{{Parent: zone, Inherit: LevelsOf("read")}}},
		{ID: space, Parents: []typeEdge{{Parent: team, Inherit: LevelsOf("write")}}},
		{ID: other},
	})
	got := g.TypesBetween(space, zone)
	slices.SortFunc(got, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	want := []uuid.UUID{zone, team, space}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(got, want) {
		t.Fatalf("between space and zone: got %v, want %v", got, want)
	}
	if got := g.TypesBetween(zone, space); got != nil {
		t.Fatalf("zone is above space, not below: %v", got)
	}
	if got := g.TypesBetween(space, other); got != nil {
		t.Fatalf("an unrelated type: %v", got)
	}
	if got := g.TypesBetween(team, team); !slices.Equal(got, []uuid.UUID{team}) {
		t.Fatalf("the type itself: %v", got)
	}
}
