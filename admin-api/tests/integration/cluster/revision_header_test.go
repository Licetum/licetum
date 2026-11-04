//go:build integration

package cluster_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// licetumRevisionForm is the value of Licetum-Revision: <epoch>.<revision> (ADR 0001, 7.7).
var licetumRevisionForm = regexp.MustCompile(`^1\.[0-9]+$`)

// TestLicetumRevisionIsOnEvery200OfTheAuthZENOperations: the position an answer was computed at is on every 200 of the five
// operations (ADR 0001, 7.7), also when no answer reads the policy: a batch of questions about a type that is not there, a
// search of an empty directory.
func TestLicetumRevisionIsOnEvery200OfTheAuthZENOperations(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org")
	r := c.Replicas[0]
	alice := c.UserToken("alice")

	for name, req := range map[string]authzenreq.Request{
		"evaluation":                       authzenreq.Evaluation(p.Slug, "alice", authzenreq.On("branch", "b1"), "read"),
		"evaluations":                      authzenreq.Evaluations(p.Slug, "alice", []authzenreq.Question{{Resource: authzenreq.On("branch", "b1"), Action: "read"}}),
		"evaluations of nothing known":     authzenreq.Evaluations(p.Slug, "alice", []authzenreq.Question{{Resource: authzenreq.On("invoice", "x"), Action: "read"}}),
		"search/resource":                  authzenreq.SearchResource(p.Slug, "alice", "branch", "read"),
		"search/action":                    authzenreq.SearchAction(p.Slug, "alice", authzenreq.On("branch", "b1")),
		"search/subject (empty directory)": authzenreq.SearchSubject(p.Slug, authzenreq.On("branch", "b1"), "read"),
	} {
		t.Run(name, func(t *testing.T) {
			res := r.Send(c, req, alice)
			if res.Status != http.StatusOK {
				t.Fatalf("%d %s", res.Status, res.Body)
			}
			if got := res.Header.Get("Licetum-Revision"); !licetumRevisionForm.MatchString(got) {
				t.Fatalf("Licetum-Revision = %q, want 1.<revision>", got)
			}
		})
	}
}
