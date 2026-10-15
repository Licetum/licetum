package harness

import (
	"net/http"
	"strconv"
	"strings"
)

// Policy is a small policy for the tests: a service with a division type and a branch type under it, both fed by the
// fake source over HTTP, and a rule that gives the user "alice" read on every branch.
type Policy struct {
	ServiceID, Slug string
	Division        string // type ids
	Branch          string
	Rule            string
}

// ServicePayload is the body of POST /service for a service that trusts the fake identity provider.
func (c *Cluster) ServicePayload(name string) map[string]any {
	return map[string]any{
		"name": name, "slug": name, "url": c.IdP.AppURL(), "icon": c.IdP.IconURL(),
		"authorization_url": c.IdP.AuthorizationURL(), "token_url": c.IdP.TokenURL(), "jwks_url": c.IdP.JWKSURL(),
		"issuer": c.IdP.Issuer(), "client_id": name, "client_secret": "s",
	}
}

// SeedPolicy registers the service slug with the two types and puts the given rows in the fake source: divisions as
// {id, name} and branches as {id, name, parent_id}. cacheTTL is the synchronizers' cache_ttl in seconds; 0 makes every
// synchronization ask the source itself.
func (c *Cluster) SeedPolicy(slug string, cacheTTL int) *Policy {
	c.T.Helper()
	return c.SeedPolicyOn(slug, cacheTTL, "division", "branch")
}

// SeedPolicyOn is SeedPolicy with the sources the two types read named by divisionKind and branchKind: a second service
// reads sources of its own, so that a change of one service's data is not a change of the other's.
func (c *Cluster) SeedPolicyOn(slug string, cacheTTL int, divisionKind, branchKind string) *Policy {
	c.T.Helper()
	c.Source.Set(divisionKind,
		map[string]any{"id": "d1", "name": "North"}, map[string]any{"id": "d2", "name": "South"})
	c.Source.Set(branchKind,
		map[string]any{"id": "b1", "name": "Branch 1", "parent_id": "d1"},
		map[string]any{"id": "b2", "name": "Branch 2", "parent_id": "d1"},
		map[string]any{"id": "b3", "name": "Branch 3", "parent_id": "d2"})

	p := &Policy{Slug: slug}
	p.ServiceID = c.Must(http.MethodPost, "/service", c.ServicePayload(slug))["id"].(string)
	p.Division = c.Must(http.MethodPost, "/service/"+p.ServiceID+"/type_resource", map[string]any{"name": "division"})["id"].(string)
	p.Branch = c.Must(http.MethodPost, "/service/"+p.ServiceID+"/type_resource",
		map[string]any{"name": "branch", "type_resource_parent_id": p.Division})["id"].(string)
	for _, t := range []struct{ id, kind, probe string }{{p.Division, divisionKind, "d1"}, {p.Branch, branchKind, "b1"}} {
		c.Must(http.MethodPost, "/service/"+p.ServiceID+"/type_resource/"+t.id+"/resource/synchronizer", map[string]any{
			"config":            map[string]any{"kind": "http", "url": c.Source.ListURL(t.kind), "cache_ttl": cacheTTL},
			"probe_resource_id": t.probe,
		})
	}
	p.Rule = c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Branch, "access": []string{"read"}, "subject": "alice",
	})["id"].(string)
	// The probes above asked the source; the tests count from here.
	c.Source.Reset()
	return p
}

// MetricValue returns the sum of the samples of a metric in a Prometheus text whose line starts with prefix (the
// name, optionally with labels up to a comma or brace), or 0 when there is none.
func MetricValue(text, prefix string) float64 {
	var sum float64
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(line[i+1:], 64); err == nil {
			sum += v
		}
	}
	return sum
}
