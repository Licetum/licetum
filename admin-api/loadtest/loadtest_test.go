package loadtest

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseMix(t *testing.T) {
	m, err := ParseMix("search_resource=2, evaluation=3")
	if err != nil {
		t.Fatalf("ParseMix: %v", err)
	}
	if m[opSearchResource] != 2 || m[opEvaluation] != 3 || m[opEvaluations10] != 0 {
		t.Fatalf("weights = %v", m)
	}
	if got := m.String(); got != "search_resource=2,evaluation=3" {
		t.Fatalf("String = %q", got)
	}
	for _, bad := range []string{"nope=1", "evaluation", "evaluation=x", "evaluation=-1", "evaluation=0",
		// The requests of the check API the traffic sent before it moved to AuthZEN are gone.
		"permission=1", "check1=1", "authzen=1", "on_behalf=1"} {
		if _, err := ParseMix(bad); err == nil {
			t.Errorf("ParseMix(%q) accepted", bad)
		}
	}
	def, err := ParseMix(DefaultMix)
	if err != nil {
		t.Fatalf("the default mix does not parse: %v", err)
	}
	if def[opSearchResource] != 2 || def[opEvaluation] != 5 || def[opEvaluations10] != 2 || def[opEvaluationProperties] != 1 {
		t.Errorf("the default mix = %v, want the shares of the traffic the budgets were measured with", def)
	}
}

func TestCountDecisions(t *testing.T) {
	cases := []struct {
		name             string
		body             string
		decisions, allow int
	}{
		{"evaluation", `{"decision":true}`, 1, 1},
		{"evaluations", `{"evaluations":[{"decision":false},{"decision":true},{"decision":true}]}`, 3, 2},
		{"search has no decisions", `{"results":[{"type":"document","id":"d-1","properties":{"name":"Document 1"}}],"page":{"count":1}}`, 0, 0},
	}
	for _, c := range cases {
		d, a := countDecisions([]byte(c.body))
		if d != c.decisions || a != c.allow {
			t.Errorf("%s: got %d decisions, %d allowed; want %d, %d", c.name, d, a, c.decisions, c.allow)
		}
	}
}

// TestK6ScriptNamesTheRequestsOfTheMix keeps the script and the harness together: the harness reads the metrics of the
// requests by the names of opNames, so the script must list the same names in the same order, and its own default mix
// (used when it is run by hand) must be the default mix of the harness.
func TestK6ScriptNamesTheRequestsOfTheMix(t *testing.T) {
	script := string(k6Script)
	list := regexp.MustCompile(`(?m)^const ops = \[(.*)\];$`).FindStringSubmatch(script)
	if list == nil {
		t.Fatal("the script has no `const ops = [...]` line")
	}
	var names []string
	for _, quoted := range strings.Split(list[1], ",") {
		names = append(names, strings.Trim(strings.TrimSpace(quoted), "'"))
	}
	if !slices.Equal(names, opNames[:]) {
		t.Errorf("the script lists the requests %v, the harness %v", names, opNames)
	}
	fallback := regexp.MustCompile(`__ENV\.MIX \|\| '([^']*)'`).FindStringSubmatch(script)
	if fallback == nil {
		t.Fatal("the script has no default mix")
	}
	if fallback[1] != DefaultMix {
		t.Errorf("the script's default mix is %q, the harness's %q", fallback[1], DefaultMix)
	}
	for _, name := range opNames {
		if !strings.Contains(script, "case '"+name+"':") {
			t.Errorf("the script does not send the request %s", name)
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
	cases := []struct {
		rate, maxVUs int
		mode         string
		want         int
	}{
		{5, 250, "warm", 50},    // the floor
		{120, 250, "warm", 120}, // the rate itself
		{400, 250, "mix", 250},  // never more than the most users
		{400, 50, "warm", 50},   // and never more than that either
		{16, 250, "cold", 250},  // a cold request waits for a job of the worker: all the users are started
	}
	for _, c := range cases {
		if got := preallocated(c.rate, c.maxVUs, c.mode); got != c.want {
			t.Errorf("preallocated(%d, %d, %q) = %d, want %d", c.rate, c.maxVUs, c.mode, got, c.want)
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
	m := Measurement{RateAsked: 10, Operations: []Operation{{Name: "evaluation", Requests: 5, Latency: Latency{Samples: 5, P50: 2}}}}
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

// TestAggregateReadsTheRequestsByName checks that a run is read back under the names of the requests it was written
// with: a run of an earlier mix lists other requests, and fewer or more of them, than this version sends.
func TestAggregateReadsTheRequestsByName(t *testing.T) {
	dir := t.TempDir()
	var runs []string
	for _, seed := range []int64{1, 2} {
		rep := Report{
			Profile:  profiles["smoke"],
			Settings: Settings{Mode: "warm", Seed: seed, Mix: "earlier=1", Rate: 10},
			Measurements: []Measurement{{
				Valid: true,
				Operations: []Operation{
					{Name: "earlier", Requests: 20000, Latency: Latency{Samples: 20000, P50: 2, P90: 3, P95: 4, P99: 5, P999: 6}},
					{Name: "unused"},
				},
			}},
		}
		run := filepath.Join(dir, "run-"+string(rune('0'+seed)))
		if err := os.MkdirAll(run, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(run, "result.json"), rep); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	out, err := AggregateReport(runs)
	if err != nil {
		t.Fatalf("AggregateReport: %v", err)
	}
	if !strings.Contains(out, "| earlier | 20000–20000 |") {
		t.Errorf("the request of the earlier mix is not in the table:\n%s", out)
	}
	for _, name := range opNames {
		if strings.Contains(out, "| "+name+" |") {
			t.Errorf("%s is in the table, but the runs did not send it", name)
		}
	}
}
