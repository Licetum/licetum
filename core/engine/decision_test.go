package engine

import (
	"slices"
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
