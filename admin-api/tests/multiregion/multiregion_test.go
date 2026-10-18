//go:build multiregion

package multiregion_test

// The scenarios of F6 (issue #38) on the multi-region environment, with the latency of the links between the regions and the
// primary. The in-process versions of most of them (cluster/f6_test.go, chaos/f6_test.go) run against a real streaming replica
// without Docker; these add the real network between the regions, the Redis of each region, and the source that only region A can
// reach. Names carry the criterion they prove.

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const farLink = 150 * time.Millisecond

func sleep(d time.Duration) { time.Sleep(d) }

func rule(subject string, extra map[string]any) map[string]any {
	body := map[string]any{"type_resource_id": policy.branchType, "access": []string{"read"}, "subject": subject}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestF6_1_AWarmRequestInARegion150msFromThePrimaryIsFast(t *testing.T) {
	defer resetLinks(t)
	setLink(t, regionB, false, farLink)
	setLink(t, regionB, true, farLink)
	base := regionB.checks[0]
	permission(base, "u1", 0)
	var took []time.Duration
	for range 1000 {
		a := permission(base, "u1", 0)
		if a.status != http.StatusOK {
			t.Fatalf("%d %s", a.status, a.body)
		}
		took = append(took, a.took)
	}
	if got := p99(took); got > 30*time.Millisecond {
		t.Fatalf("p99 = %v, want at most 30 ms", got)
	}
}

func TestF6_2_ADenyRuleOfRegionAIsEnforcedInRegionBWithinTheSLO(t *testing.T) {
	defer resetLinks(t)
	setLink(t, regionB, false, farLink)
	setLink(t, regionB, true, farLink)
	n := envInt("LICETUM_F6_REVOCATIONS", 200)
	var took []time.Duration
	for i := range n {
		user := fmt.Sprintf("revoke%d", i)
		mustAdmin(t, regionA.admin, "POST", "/assignment_rule", rule(user, nil))
		eventually(t, 30*time.Second, "the grant to reach region B", func() bool { return permission(regionB.checks[0], user, 0).hasAccess() })
		mustAdmin(t, regionA.admin, "POST", "/assignment_rule", rule(user, map[string]any{"effect": "deny"}))
		start := time.Now()
		eventually(t, 30*time.Second, "the denial to reach region B", func() bool {
			return permission(regionB.checks[0], user, 0).denies()
		})
		took = append(took, time.Since(start))
	}
	if got := p99(took); got > 5*time.Second {
		t.Fatalf("p99 of %d revocations = %v, want at most 5 s", n, got)
	}
}

func TestF6_3_AStrongReadInRegionBIsCurrentWhileItsReplicaLags(t *testing.T) {
	defer resetLinks(t)
	setLink(t, regionB, false, farLink)
	setLink(t, regionB, true, 2*time.Second) // a round trip of 2 s: the stream to the replica of B takes 1 s
	n := envInt("LICETUM_F6_STRONG_READS", 200)
	for i := range n {
		user := fmt.Sprintf("strong%d", i)
		_, rev := mustAdmin(t, regionA.admin, "POST", "/assignment_rule", rule(user, nil))
		if a := permission(regionB.checks[i%2], user, rev); !a.hasAccess() {
			t.Fatalf("iteration %d: a read that asked for revision %d did not see the write: %d %s", i, rev, a.status, a.body)
		}
	}
}

func TestF6_4_LagMakesRegionBNotReadyAndItRecovers(t *testing.T) {
	defer resetLinks(t)
	// The stack runs with DB__MAX_REPLICA_LAG of 5 s. setLink takes a round trip and delays each direction by half of it: the
	// WAL reaches the replica of B 6 s after the commit, so its revision stays behind the primary's for longer than the limit.
	setLink(t, regionB, true, 12*time.Second)
	mustAdmin(t, regionA.admin, "POST", "/assignment_rule", rule("lagging", nil))
	eventually(t, 15*time.Second, "region B to report itself not ready", func() bool {
		return ready(regionB.checks[0]) == http.StatusServiceUnavailable
	})
	setLink(t, regionB, true, 0)
	eventually(t, 60*time.Second, "region B to be ready again", func() bool { return ready(regionB.checks[0]) == http.StatusOK })
}

func TestF6_5_ACutLinkFailsRegionBClosedAfterTheLimitAndItCatchesUp(t *testing.T) {
	defer resetLinks(t)
	cut := time.Duration(envInt("LICETUM_F6_CUT_SECONDS", 60)) * time.Second // the stack runs with POLICY__MAX_STALENESS of 20 s
	base := regionB.checks[0]
	// Someone the seeded rule lets read, outside u0..u49: the load of F6_8 and F6_9 expects the answer of u1 for those, and the
	// denial made here stays.
	const person = "u907"
	if !permission(base, person, 0).hasAccess() {
		t.Fatal("region B does not answer before the cut")
	}
	cutLink(t, regionB, true)
	cutAt := time.Now()
	mustAdmin(t, regionA.admin, "POST", "/assignment_rule", rule(person, map[string]any{"effect": "deny"}))
	var answered, refused int
	for time.Since(cutAt) < cut {
		a := permission(base, person, 0)
		switch {
		case a.status == http.StatusOK && refused == 0:
			answered++
		case a.status == http.StatusServiceUnavailable:
			refused++
		default:
			t.Fatalf("%v after the cut: %d %s", time.Since(cutAt), a.status, a.body)
		}
		sleep(100 * time.Millisecond)
	}
	if answered == 0 || refused == 0 {
		t.Fatalf("answered %d, refused %d: the region must answer within the limit and fail closed beyond it", answered, refused)
	}
	cutLink(t, regionB, false)
	start := time.Now()
	eventually(t, 5*time.Second+30*time.Second, "the denial made during the cut to be enforced", func() bool {
		return permission(base, person, 0).denies()
	})
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the revocation took %v after the link returned, want within the SLO of 5 s", took)
	}
}

func TestF6_6_TheSourceOfRegionAIsAskedByItsWorkerOnly(t *testing.T) {
	out, err := composeOutput("logs", "--no-log-prefix", "source")
	if err != nil {
		t.Fatalf("logs of the source: %v\n%s", err, out)
	}
	callers := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && strings.Contains(line, "GET /src") {
			callers[f[0]]++
		}
	}
	if len(callers) == 0 {
		t.Fatal("the source was never asked")
	}
	t.Logf("callers of the source: %v (the worker of A synchronizes; the admin API of A probed the source when the synchronizer was created; the other regions cannot even reach it: it is on the network of region A only)", callers)
	for _, name := range []string{"check-a1", "check-a2", "check-b1", "check-b2", "check-c1", "check-c2", "admin-b"} {
		out, err := exec.Command("docker", "compose", "-f", composeFile, "ps", "-q", name).Output()
		if err != nil || len(out) == 0 {
			continue
		}
		ip, err := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", strings.TrimSpace(string(out))).Output()
		if err != nil {
			continue
		}
		for _, addr := range strings.Fields(string(ip)) {
			if callers[addr] > 0 {
				t.Errorf("%s (%s) asked the source %d times", name, addr, callers[addr])
			}
		}
	}
}

func TestF6_7_AnAdminWriteFromRegionBIsSlowByTheLinkOnly(t *testing.T) {
	defer resetLinks(t)
	setLink(t, regionB, false, farLink)
	for i := range 2 { // warm
		mustAdmin(t, regionB.admin, "POST", "/assignment_rule", rule(fmt.Sprintf("far-warm%d", i), nil))
	}
	var worst time.Duration
	for i := range 5 {
		start := time.Now()
		mustAdmin(t, regionB.admin, "POST", "/assignment_rule", rule(fmt.Sprintf("far%d", i), nil))
		worst = max(worst, time.Since(start))
	}
	// The write itself is six round trips (chaos/f6_test.go, F6-7, asserts it); the request also authorizes the caller and refreshes the
	// policy of the administration, which cross the link too. Reported, not asserted: the guide puts the administration in the
	// region of the primary.
	t.Logf("an admin write from region B with %v to the primary: %v (%.1f round trips)", farLink, worst.Round(time.Millisecond), float64(worst)/float64(farLink))
}

// regionLoad sends rps requests a second for d, split over the regions by weights, and counts what came back.
func regionLoad(t *testing.T, rps int, d time.Duration, weights []int, alive func(i int) bool) (sent, failed, wrong int64, took []time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var sentN, failedN, wrongN atomic.Int64
	want := permission(regionA.checks[0], "u1", 0).body
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	defer ticker.Stop()
	stop := time.After(d)
	total := 0
	for _, w := range weights {
		total += w
	}
	for i := 0; ; i++ {
		select {
		case <-stop:
			wg.Wait()
			return sentN.Load(), failedN.Load(), wrongN.Load(), took
		case <-ticker.C:
		}
		n := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// the region by weight, among the regions the balancer still sends to
			pick := (n * 7919) % total
			r := 0
			for ; r < len(weights)-1 && pick >= weights[r]; r++ {
				pick -= weights[r]
			}
			for !alive(r) {
				r = (r + 1) % len(weights)
			}
			a := permission(regions[r].checks[n%2], fmt.Sprintf("u%d", n%50), 0)
			sentN.Add(1)
			switch {
			case a.status != http.StatusOK:
				failedN.Add(1)
			case a.body != want:
				wrongN.Add(1)
			}
			mu.Lock()
			took = append(took, a.took)
			mu.Unlock()
		}()
	}
}

func TestF6_8_SixHundredRequestsASecondOverThreeRegions(t *testing.T) {
	defer resetLinks(t)
	for _, r := range []region{regionB, regionC} {
		setLink(t, r, false, farLink)
		setLink(t, r, true, farLink)
	}
	d := time.Duration(envInt("LICETUM_F6_LOAD_SECONDS", 600)) * time.Second
	sent, failed, wrong, took := regionLoad(t, 600, d, []int{50, 30, 20}, func(int) bool { return true })
	t.Logf("%d requests in %v, %d failed, %d wrong, p99 %v", sent, d, failed, wrong, p99(took))
	if float64(failed) > 0.001*float64(sent) {
		t.Errorf("%d of %d requests failed, want at most 0.1 %%", failed, sent)
	}
	if got := p99(took); got > 50*time.Millisecond {
		t.Errorf("p99 = %v, want within the budget of 50 ms", got)
	}
	if wrong != 0 {
		t.Errorf("%d wrong answers", wrong)
	}
}

func TestF6_9_ARegionIsRemovedAndTheOthersTakeItsTraffic(t *testing.T) {
	defer resetLinks(t)
	defer func() { _ = compose("start", "check-c1", "check-c2") }()
	var cDown atomic.Bool
	steady := time.Duration(envInt("LICETUM_F6_LOAD_SECONDS", 120)) * time.Second
	go func() {
		sleep(steady / 2)
		cDown.Store(true)
		_ = compose("stop", "-t", "5", "check-c1", "check-c2")
	}()
	sent, failed, wrong, took := regionLoad(t, 600, steady, []int{50, 30, 20}, func(r int) bool { return r != 2 || !cDown.Load() })
	t.Logf("%d requests, %d failed, %d wrong, p99 %v", sent, failed, wrong, p99(took))
	if float64(failed) > 0.001*float64(sent) {
		t.Errorf("%d of %d requests failed", failed, sent)
	}
	if wrong != 0 {
		t.Errorf("%d wrong answers", wrong)
	}
}
