package loadtest

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseMix(t *testing.T) {
	m, err := ParseMix("permission=2, check=3")
	if err != nil {
		t.Fatalf("ParseMix: %v", err)
	}
	if m[opPermission] != 2 || m[opCheck] != 3 || m[opAuthzen] != 0 {
		t.Fatalf("weights = %v", m)
	}
	if got := m.String(); got != "permission=2,check=3" {
		t.Fatalf("String = %q", got)
	}
	for _, bad := range []string{"nope=1", "check", "check=x", "check=-1", "check=0"} {
		if _, err := ParseMix(bad); err == nil {
			t.Errorf("ParseMix(%q) accepted", bad)
		}
	}
}

func TestCountDecisions(t *testing.T) {
	cases := []struct {
		name             string
		body             string
		decisions, allow int
	}{
		{"check", `{"results":[{"allowed":true},{"allowed":false}]}`, 2, 1},
		{"authzen", `{"decision":true}`, 1, 1},
		{"authzen batch", `{"evaluations":[{"decision":false},{"decision":true},{"decision":true}]}`, 3, 2},
		{"permission has no answers", `{"branch":{"type":"sync","resources":[{"id":"b1","access":["read"]}]}}`, 0, 0},
	}
	for _, c := range cases {
		d, a := countDecisions([]byte(c.body))
		if d != c.decisions || a != c.allow {
			t.Errorf("%s: got %d decisions, %d allowed; want %d, %d", c.name, d, a, c.decisions, c.allow)
		}
	}
}

func TestParseExposition(t *testing.T) {
	const body = `# HELP licetum_permission_cache_total Permission result cache lookups.
# TYPE licetum_permission_cache_total counter
licetum_permission_cache_total{result="hit"} 12
licetum_permission_cache_total{result="miss"} 3
licetum_permission_compute_seconds_sum 1.5
licetum_permission_compute_seconds_count 3
not a sample
`
	out, err := parseExposition(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseExposition: %v", err)
	}
	if out[keyPermHit] != 12 || out[keyPermMiss] != 3 {
		t.Errorf("labelled series = %v", out)
	}
	if out[keyComputeSum] != 1.5 || out[keyComputeCount] != 3 {
		t.Errorf("histogram sum and count = %v", out)
	}
	if len(out) != 4 {
		t.Errorf("kept %d series, want 4", len(out))
	}
}

func TestJudge(t *testing.T) {
	ok := Step{TargetRPS: 100, AchievedRPS: 99, Latency: Latency{P99: 40}}
	ok.judge(0.01, 100*time.Millisecond)
	if !ok.Passed {
		t.Fatalf("a step within its objectives failed: %v", ok.Reasons)
	}

	slow := Step{TargetRPS: 100, AchievedRPS: 99, Latency: Latency{P99: 250}}
	slow.judge(0.01, 100*time.Millisecond)
	failing := Step{TargetRPS: 100, AchievedRPS: 80, ErrorRate: 0.05, Latency: Latency{P99: 40}}
	failing.judge(0.01, 100*time.Millisecond)
	for name, s := range map[string]Step{"slow": slow, "late and failing": failing} {
		if s.Passed || len(s.Reasons) == 0 {
			t.Errorf("%s step passed: %+v", name, s)
		}
	}
	if len(failing.Reasons) != 2 {
		t.Errorf("reasons = %v, want the error budget and the late answers", failing.Reasons)
	}
}

func TestSince(t *testing.T) {
	before := statementCounts{
		1: {Query: "SELECT 1", Calls: 10, TotalMS: 100},
		2: {Query: "SELECT 2", Calls: 5, TotalMS: 50},
	}
	after := statementCounts{
		1: {Query: "SELECT 1", Calls: 20, TotalMS: 400},
		2: {Query: "SELECT 2", Calls: 5, TotalMS: 50},
		3: {Query: "SELECT 3", Calls: 2, TotalMS: 30},
	}
	got := since(before, after, 10)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want the two that ran: %+v", len(got), got)
	}
	if got[0].Query != "SELECT 1" || got[0].Calls != 10 || got[0].TotalMS != 300 || got[0].MeanMS != 30 {
		t.Errorf("most expensive = %+v", got[0])
	}
	if got := since(before, after, 1); len(got) != 1 {
		t.Errorf("limit ignored: %d statements", len(got))
	}
}

func TestProcCPU(t *testing.T) {
	cpu, err := procCPU(os.Getpid())
	if err != nil {
		t.Fatalf("procCPU: %v", err)
	}
	if cpu < 0 {
		t.Fatalf("cpu = %v", cpu)
	}
}

func TestProfileVolumes(t *testing.T) {
	p := Profile{Users: 1, Groups: 1, Rules: 1, Resources: 10_000}
	if p.folders() != 500 || p.documents() != 9_500 {
		t.Fatalf("folders %d, documents %d", p.folders(), p.documents())
	}
	if err := (Profile{Users: 1, Groups: 1, Rules: 1, Resources: 5}).validate(); err == nil {
		t.Error("a profile with 5 resources was accepted")
	}
}

// TestRuleMix checks the policy each rule kind builds: the owner pattern first, then the mix of the rest.
func TestRuleMix(t *testing.T) {
	p := Profile{Users: 200, Groups: 10, Rules: 1_000, Resources: 10_000}
	fx := &fixture{folders: p.folders(), documents: p.documents(), folderType: uuid.New(), documentType: uuid.New()}
	for range p.Groups {
		fx.groups = append(fx.groups, uuid.New())
	}

	owner := ruleFor(0, p, fx)
	if owner.ResourceFromClaim == nil || owner.ResourceFromClaim.MatchBy != "meta.owner" {
		t.Fatalf("rule 0 is not the owner pattern: %+v", owner)
	}
	if denial := ruleFor(8, p, fx); denial.Effect != "deny" || denial.MemberOfGroupID == nil {
		t.Errorf("rule 8 is not a department denial: %+v", denial)
	}
	if exc := ruleFor(9, p, fx); !exc.Exception || exc.Effect != "allow" {
		t.Errorf("rule 9 is not an exception: %+v", exc)
	}
	if grant := ruleFor(4, p, fx); grant.Subject == nil || *grant.Subject != "user-4" {
		t.Errorf("rule 4 is not a grant to user-4: %+v", grant)
	}
}
