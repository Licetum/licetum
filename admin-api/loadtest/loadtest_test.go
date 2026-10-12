package loadtest

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseMix(t *testing.T) {
	m, err := ParseMix("permission=2, check1=3")
	if err != nil {
		t.Fatalf("ParseMix: %v", err)
	}
	if m[opPermission] != 2 || m[opCheck1] != 3 || m[opAuthzen] != 0 {
		t.Fatalf("weights = %v", m)
	}
	if got := m.String(); got != "permission=2,check1=3" {
		t.Fatalf("String = %q", got)
	}
	for _, bad := range []string{"nope=1", "check", "check1=x", "check1=-1", "check1=0"} {
		if _, err := ParseMix(bad); err == nil {
			t.Errorf("ParseMix(%q) accepted", bad)
		}
	}
	if _, err := ParseMix(DefaultMix); err != nil {
		t.Fatalf("the default mix does not parse: %v", err)
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

// TestMinSamples pins the rule of the report: a quantile is published from 100/(1-q) samples.
func TestMinSamples(t *testing.T) {
	cases := map[float64]int{0.5: 200, 0.9: 1000, 0.95: 2000, 0.99: 10000, 0.999: 100000}
	for q, want := range cases {
		if got := MinSamples(q); got != want {
			t.Errorf("MinSamples(%v) = %d, want %d", q, got, want)
		}
	}
	l := Latency{Samples: 9999, P99: 42}
	if _, ok := l.Quantile(0.99, l.P99); ok {
		t.Error("a p99 from 9,999 samples was published")
	}
	l.Samples = 10000
	if v, ok := l.Quantile(0.99, l.P99); !ok || v != 42 {
		t.Errorf("a p99 from 10,000 samples was not published: %v %v", v, ok)
	}
}

func TestJudgeMeasurement(t *testing.T) {
	ok := Measurement{K6Cores: 2, K6CPUShare: 0.4}
	ok.judgeMeasurement()
	if !ok.Valid {
		t.Fatalf("a window within its limits is not valid: %v", ok.InvalidReasons)
	}
	bad := Measurement{K6Cores: 2, K6CPUShare: 0.9, Dropped: 3, Oracle: Oracle{Checked: 10, Mismatches: 1}}
	bad.judgeMeasurement()
	if bad.Valid || len(bad.InvalidReasons) != 3 {
		t.Fatalf("dropped, a busy generator and a wrong answer should each invalidate: %+v", bad)
	}
}

// TestSwapInvalidates checks that the pages the host swapped are read from /proc/vmstat, and that a window during which
// they grew is not valid, with its reason.
func TestSwapInvalidates(t *testing.T) {
	const vmstat = "nr_free_pages 1000\npswpin 12\npswpout 30\npgpgin 99\n"
	if got := parseSwapPages(vmstat); got != 42 {
		t.Fatalf("parseSwapPages = %v, want 42", got)
	}
	swapped := Measurement{K6Cores: 2, HostSwapPages: 8}
	swapped.judgeMeasurement()
	if swapped.Valid || len(swapped.InvalidReasons) != 1 {
		t.Fatalf("a window that swapped should be invalid with one reason: %+v", swapped)
	}
	quiet := Measurement{K6Cores: 2}
	quiet.judgeMeasurement()
	if !quiet.Valid {
		t.Fatalf("a window that did not swap is not valid: %v", quiet.InvalidReasons)
	}
}

func TestJudgeStep(t *testing.T) {
	step := Measurement{RateAsked: 50, AchievedRPS: 49, ErrorRate: 0, Overall: Latency{P99: 40, Samples: 2000}, Valid: true}
	step.judgeStep(0.01, 100*time.Millisecond)
	if !step.passes() {
		t.Fatalf("a step within its objectives failed: %v", step.FailReasons)
	}
	slow := Measurement{RateAsked: 50, AchievedRPS: 49, Overall: Latency{P99: 250, Samples: 10000}, Valid: true}
	slow.judgeStep(0.01, 100*time.Millisecond)
	late := Measurement{RateAsked: 50, AchievedRPS: 30, ErrorRate: 0.05, Overall: Latency{P99: 40}, Valid: true}
	late.judgeStep(0.01, 100*time.Millisecond)
	if slow.passes() || late.passes() || len(late.FailReasons) != 2 {
		t.Fatalf("slow %+v, late %+v: want both failed, the late one for errors and for the rate", slow, late)
	}
	// A short step (750 requests, as a 15-second step at 50 requests/s) has no published p99, so a high p99 does not fail it.
	short := Measurement{RateAsked: 50, AchievedRPS: 49, Overall: Latency{P99: 250, Samples: 750}, Valid: true}
	short.judgeStep(0.01, 100*time.Millisecond)
	if !short.passes() {
		t.Fatalf("a step without a published p99 was failed on it: %v", short.FailReasons)
	}
}

func TestCPUCores(t *testing.T) {
	cases := map[string]int{"": 0, "10-11": 2, "0,2-4": 4, "7": 1}
	for set, want := range cases {
		if set == "" {
			continue
		}
		if got := cpuCores(set); got != want {
			t.Errorf("cpuCores(%q) = %d, want %d", set, got, want)
		}
	}
}

func TestPreallocated(t *testing.T) {
	cases := []struct{ rate, maxVUs, want int }{
		{5, 250, 50},    // the floor
		{120, 250, 120}, // the rate itself
		{400, 250, 250}, // never more than the most users
		{400, 50, 50},   // and never more than that either
	}
	for _, c := range cases {
		if got := preallocated(c.rate, c.maxVUs); got != c.want {
			t.Errorf("preallocated(%d, %d) = %d, want %d", c.rate, c.maxVUs, got, c.want)
		}
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

// TestExpectedAllowed checks the oracle on a policy small enough to work out by hand. Four subjects, two departments,
// twenty rules and one folder: rules 0-9 belong to department 0, rules 10-19 to department 1, and every rule grants or
// denies the single folder. Rule 0 is the owner pattern; rule 8 denies department 0 the folder; rule 18 denies
// department 1; rule 9 (an exception for subject 1 on document 9) lifts the denial for that document.
func TestExpectedAllowed(t *testing.T) {
	p := Profile{Name: "hand", Users: 4, Groups: 2, Rules: 20, Resources: 20}
	cases := []struct {
		u, d int
		want bool
		why  string
	}{
		{0, 4, false, "owned by subject 0, but department 0 is denied the folder"},
		{1, 0, false, "department 1 has grants on the folder, but it is denied and no exception names document 0"},
		{1, 9, true, "the exception of rule 9 lifts the denial for subject 1 on document 9"},
		{2, 2, false, "owned by subject 2, but department 0 is denied the folder"},
	}
	for _, c := range cases {
		if got := p.expectedAllowed(c.u, c.d); got != c.want {
			t.Errorf("subject %d, document %d: got %v, want %v (%s)", c.u, c.d, got, c.want, c.why)
		}
	}
}

// TestOracleSampleInRange checks that the sampled pairs name subjects and documents the policy has.
func TestOracleSampleInRange(t *testing.T) {
	p := Profile{Name: "range", Users: 50, Groups: 5, Rules: 300, Resources: 400}
	rnd := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		u, d := p.oracleSample(rnd)
		if u < 0 || u >= p.Users || d < 0 || d >= p.documents() {
			t.Fatalf("pair (%d, %d) is outside the policy", u, d)
		}
	}
}

// TestRuleMix checks the policy each rule kind builds: the owner pattern first, then the mix of the rest, and each
// department gets all the kinds (the department of a rule is taken from its block of ten).
func TestRuleMix(t *testing.T) {
	p := Profile{Users: 200, Groups: 10, Rules: 1_000, Resources: 10_000}
	fx := &fixture{folders: p.folders(), documents: p.documents(), folderType: [16]byte{1}, documentType: [16]byte{2}}
	for range p.Groups {
		fx.groups = append(fx.groups, [16]byte{3})
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
	// Department 7 gets the group grants (kinds 0-3) as well, not only one kind: 1000 rules over 10 departments.
	kinds := map[int]bool{}
	for i := range p.Rules {
		if i == 0 {
			continue
		}
		if r := ruleFor(i, p, fx); r.MemberOfGroupID != nil && *r.MemberOfGroupID == fx.groups[7] {
			kinds[i%10] = true
		}
	}
	if len(kinds) < 3 {
		t.Errorf("department 7 has rules of %d kinds, want the mix", len(kinds))
	}
}

// TestMeasurementJSON checks that a window survives the round trip to result.json, with its samples.
func TestMeasurementJSON(t *testing.T) {
	m := Measurement{RateAsked: 10, Operations: []Operation{{Name: "check1", Requests: 5, Latency: Latency{Samples: 5, P50: 2}}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Measurement
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Operations[0].Latency.Samples != 5 || math.Abs(back.Operations[0].Latency.P50-2) > 1e-9 {
		t.Fatalf("round trip lost the samples: %+v", back)
	}
}
