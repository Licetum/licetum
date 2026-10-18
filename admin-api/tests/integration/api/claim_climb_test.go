//go:build integration

package api_test

// Integration test of what a resource_from_claim lookup needs synchronized: every type its climb passes. Same
// Testcontainers environment as hierarchy_test.go; the resources come from the fake HTTP source of core/testsupport.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

// A resource_from_claim rule finds its anchors by climbing the resource links from the matched resource up to the rule's
// type, so the request must synchronize every type on the way (TypeGraph.TypesBetween in prepare), also one no level flows
// through and no rule or question names. Without its data the link below it stays unresolved, the climb stops there and a
// denial found that way has no anchor: it fails open.
//
// zone <- team (inherit read) <- squad (inherit write) <- space. Read flows from a zone to its teams, write from a team to its
// squads, nothing from a zone to a squad or a space: the squad, of another service, lies between the space a claim names and
// the zone the denial is anchored on, and only the climb reaches it (read is granted on the zones as a whole: a rule on the
// teams would bring the squads in as its descendants, and the questions name teams only). The answer then reads the data of
// the squads' service, so a change of that data alone (the squad, and with it the space, moves under another team) reaches
// the cached answers without consistency=higher: at once in the process that ran the synchronization, within the poll
// interval in another.
func TestIntegrationClaimLookupSynchronizesTheTypesItClimbs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	src := support.NewSource(t)
	src.Set("zone", map[string]any{"id": "Z1", "name": "Zone 1"}, map[string]any{"id": "Z2", "name": "Zone 2"})
	src.Set("team",
		map[string]any{"id": "T1", "name": "Team 1", "parent_id": "Z1"},
		map[string]any{"id": "T2", "name": "Team 2", "parent_id": "Z2"})
	src.Set("squad", map[string]any{"id": "Q1", "name": "Squad 1", "parent_id": "T1"})
	src.Set("space", map[string]any{"id": "S1", "name": "Space 1", "parent_id": "Q1"})

	org := e.must("POST", "/service", servicePayload(e, "org"))["id"].(string)
	squads := e.must("POST", "/service", servicePayload(e, "squads"))["id"].(string)
	types := map[string]string{}
	newType := func(service, name, probe, parent string, inherit []string) {
		t.Helper()
		body := map[string]any{"name": name}
		if parent != "" {
			body["parents"] = []any{map[string]any{"type_resource_id": types[parent], "inherit": inherit}}
		}
		id := e.must("POST", "/service/"+service+"/type_resource", body)["id"].(string)
		types[name] = id
		// The probe asks the source for one resource; nothing reaches the local copy before a synchronization.
		e.must("POST", "/service/"+service+"/type_resource/"+id+"/resource/synchronizer", map[string]any{
			"config":            map[string]any{"kind": "http", "url": src.ListURL(name), "cache_ttl": 0},
			"probe_resource_id": probe,
		})
	}
	newType(org, "zone", "Z1", "", nil)
	newType(org, "team", "T1", "zone", []string{"read"})
	newType(squads, "squad", "Q1", "team", []string{"write"})
	newType(org, "space", "S1", "squad", nil)

	allow := e.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": types["zone"], "subject": "alice", "access": []string{"read"}})["id"].(string)
	deny := e.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": types["zone"], "subject": "alice", "access": []string{"read"}, "effect": "deny",
		"resource_from_claim": map[string]any{"claim": "blocked", "lookup_type_resource_id": types["space"], "match_by": "id"},
	})["id"].(string)

	typeIDs := []string{types["zone"], types["team"], types["squad"], types["space"]}
	var states, rows int
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sync_state WHERE type_resource_id = ANY($1::uuid[])),
		       (SELECT count(*) FROM resource_cache WHERE type_resource_id = ANY($1::uuid[]))`, typeIDs).Scan(&states, &rows); err != nil {
		t.Fatal(err)
	}
	if states != 0 || rows != 0 {
		t.Fatalf("something was synchronized before the first request: %d sync states, %d resources", states, rows)
	}

	token := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub", "blocked": "S1"})
	type teamAnswer struct{ decision, deniedAt string }
	// ask asks the check API at base, with the default consistency, about read on T1 and T2 as alice, whose claim names S1.
	ask := func(base string) [2]teamAnswer {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"explain": true, "checks": []map[string]any{
			{"type": "team", "resource_id": "T1", "action": "read"},
			{"type": "team", "resource_id": "T2", "action": "read"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, base+"/service/"+org+"/check", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("check: %d %s", resp.StatusCode, body)
		}
		// An answer made before the synchronizations finished lacks data for another reason than the one tested here.
		if resp.Header.Get("X-Authz-Sync") == "pending" {
			t.Fatalf("the answer was made before the synchronization finished: %s", body)
		}
		var out model.CheckResponse
		if err := json.Unmarshal(body, &out); err != nil || len(out.Results) != 2 {
			t.Fatalf("check answer: %v (%s)", err, body)
		}
		var got [2]teamAnswer
		for i, r := range out.Results {
			got[i].decision = r.Explanation.Decision
			for _, d := range r.Explanation.Denials {
				if d.Rule.RuleID.String() != deny || d.Rule.Anchor.ResourceID == nil || d.UnresolvedParent || len(d.LiftedBy) != 0 {
					t.Fatalf("question %d: an unexpected denial %+v (%s)", i, d, body)
				}
				got[i].deniedAt = *d.Rule.Anchor.ResourceID
			}
			if len(r.Explanation.GrantedBy) != 1 || r.Explanation.GrantedBy[0].RuleID.String() != allow {
				t.Fatalf("question %d: read must be granted by the rule on the zones: %s", i, body)
			}
		}
		return got
	}
	before := [2]teamAnswer{{"denied", "Z1"}, {"allowed", ""}}
	after := [2]teamAnswer{{"allowed", ""}, {"denied", "Z2"}}

	// The first request synchronizes what it needs and waits for it: the squad too, so the climb S1 -> Q1 -> T1 -> Z1
	// reaches the zone and the denial stands on Z1, which passes read down to T1.
	if got := ask(e.check.URL); got != before {
		t.Fatalf("T1 lies under the zone above S1 and must be denied: got %+v, want %+v (the squad's source was asked %d times)",
			got, before, src.CallsTo("squad"))
	}
	// Another check process answers the same, and keeps the answer.
	replica := e.checkReplica(nil)
	if got := ask(replica.url); got != before {
		t.Fatalf("the replica: got %+v, want %+v", got, before)
	}

	revisions := func() (orgData, squadsData int64) {
		t.Helper()
		rev, err := db.LoadRevisions(ctx, e.pool)
		if err != nil {
			t.Fatal(err)
		}
		return rev.Data[org], rev.Data[squads]
	}
	orgBefore, squadsBefore := revisions()

	// The source of the squads moves Q1, and with it S1, under T2 in Z2; the squads are refreshed through the admin API.
	src.Set("squad", map[string]any{"id": "Q1", "name": "Squad 1", "parent_id": "T2"})
	e.must("POST", "/type_resource/"+types["squad"]+"/resource/refresh", nil)
	orgAfter, squadsAfter := revisions()
	// Only the data of the squads' service moved: the cached answers are dropped only if they recorded it.
	if squadsAfter <= squadsBefore || orgAfter != orgBefore {
		t.Fatalf("data revisions: org %d -> %d, squads %d -> %d; want only the squads' to move",
			orgBefore, orgAfter, squadsBefore, squadsAfter)
	}

	// The process that ran the synchronization reads the revisions again at once.
	if got := ask(e.check.URL); got != after {
		t.Fatalf("after the move, at once: got %+v, want %+v", got, after)
	}
	// Another process sees the data revision within the poll interval (REVISION__POLL_INTERVAL, 250 ms); a kept answer
	// that did not record the squads' data would be served for its whole life, up to a minute.
	deadline := time.Now().Add(5 * time.Second)
	for got := ask(replica.url); got != after; got = ask(replica.url) {
		if time.Now().After(deadline) {
			t.Fatalf("after the move, the replica still answers %+v, want %+v", got, after)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
