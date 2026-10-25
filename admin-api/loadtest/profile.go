// Package loadtest measures the check API of Licetum at a given size of policy. It seeds a service through the admin
// application (resource types synchronized from a PostgreSQL source, groups by a claim, assignment rules), starts the
// check binary as a separate process, and sends it requests at a growing rate until the latency or the error budget
// breaks. The result says how many answers per second each volume sustains, and what the server spent them on.
package loadtest

import (
	"fmt"
	"sort"
)

// Profile is the size of a run: the subjects that send requests, and the policy and the resources they meet.
type Profile struct {
	Name      string `json:"name"`
	Users     int    `json:"users"`     // subjects; each one has the claims of its department
	Groups    int    `json:"groups"`    // departments: one group per department, its members are picked by the department claim
	Rules     int    `json:"rules"`     // assignment rules, in the mix of ruleFor
	Resources int    `json:"resources"` // folders and documents of the synchronized source; a folder is 1 in 20 of them
}

// profiles are the named sizes. smoke runs on every change of the Go code; medium and large run nightly.
var profiles = map[string]Profile{
	"smoke":  {Name: "smoke", Users: 200, Groups: 10, Rules: 1_000, Resources: 10_000},
	"medium": {Name: "medium", Users: 5_000, Groups: 50, Rules: 10_000, Resources: 100_000},
	"large":  {Name: "large", Users: 50_000, Groups: 200, Rules: 100_000, Resources: 1_000_000},
	"500k":   {Name: "500k", Users: 25_000, Groups: 100, Rules: 50_000, Resources: 500_000},
}

// ProfileNames lists the named sizes in order.
func ProfileNames() []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LookupProfile returns the named size.
func LookupProfile(name string) (Profile, error) {
	p, ok := profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("unknown profile %q (one of %v)", name, ProfileNames())
	}
	return p, nil
}

func (p Profile) folders() int   { return max(1, p.Resources/20) }
func (p Profile) documents() int { return p.Resources - p.folders() }

func (p Profile) validate() error {
	if p.Users < 1 || p.Groups < 1 || p.Rules < 1 || p.Resources < 20 {
		return fmt.Errorf("profile %s: users, groups and rules must be at least 1 and resources at least 20", p.Name)
	}
	return nil
}
