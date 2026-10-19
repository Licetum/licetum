package model

import (
	"time"

	"github.com/google/uuid"
)

// AccessLookup answers "who has access to this resource": the rules in force that cover it, with the levels that reach
// it and whom each applies to. It reads the rules, not the decision: to know what one subject gets, ask the check.
type AccessLookup struct {
	Type     string        `json:"type" doc:"Name of the resource type"`
	Resource *string       `json:"resource_id" doc:"The resource asked about; null for the type as a whole"`
	Name     *string       `json:"name" doc:"Name of the resource; null for the type as a whole"`
	Entries  []AccessEntry `json:"entries" nullable:"false" doc:"The rules that cover the resource, denials included"`
	Dynamic  []AccessEntry `json:"dynamic" nullable:"false" doc:"Rules that find their resource from a claim of whoever asks: whether they reach this one depends on that person's token"`
	People   AccessPeople  `json:"people" doc:"What is known of the people the entries reach"`
}

// AccessPeople says how far the "people" of the entries can be trusted: they are the people the applications sent to the
// subject directories of the services the rules belong to.
type AccessPeople struct {
	Registry int  `json:"registry" doc:"People of the directories of the rules' services that the rules were tried on; 0 — those directories are empty, nothing is said about people"`
	Limited  bool `json:"limited" doc:"The directories are larger than what was tried"`
}

// AccessEntry is a rule that covers the resource.
type AccessEntry struct {
	Rule          RuleRef       `json:"rule" doc:"The rule, as an explanation names it"`
	Name          *string       `json:"name" doc:"The name of the rule; null when it has none"`
	Via           string        `json:"via" enum:"resource,ancestor,type,attribute,claim" doc:"How the rule reaches the resource: anchored on it (resource), on an ancestor (ancestor), on its type or a higher one (type), on the resources whose attributes match (attribute), or found by a claim (claim)"`
	Levels        []string      `json:"levels" nullable:"false" doc:"The levels that reach the resource: what the rule gives, cut by what the links let flow down"`
	Who           AccessWho     `json:"who" doc:"Whom the rule applies to"`
	Role          *NamedID      `json:"role" doc:"The role the rule grants; null for an inline rule"`
	ExpiresAt     *time.Time    `json:"expires_at" doc:"When the rule stops; null — never"`
	ValidFrom     *time.Time    `json:"valid_from" doc:"When the rule starts; null — it has started"`
	Assumed       bool          `json:"assumed" doc:"A denial that applies only because the resource has a parent that is not in the cache yet: none of its levels reaches the resource for real"`
	AssumedLevels []string      `json:"assumed_levels" nullable:"false" doc:"The levels of a denial that reach the resource only because it has a parent that is not in the cache yet (fail closed); the other levels reach it for real"`
	People        []KnownPerson `json:"people" nullable:"false" doc:"People of the directory the rule applies to (up to 20)"`
	PeopleTotal   int           `json:"people_total" doc:"How many people of the directory the rule applies to"`
}

// NamedID is an entity with its name.
type NamedID struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Id"`
	Name string    `json:"name" doc:"Name"`
}

// KnownPerson is a person of a subject directory.
type KnownPerson struct {
	Subject string  `json:"subject" doc:"The subject as the rules name it"`
	Name    *string `json:"name" doc:"The display name, when the application sent one"`
}

// AccessWho is whom a rule applies to.
type AccessWho struct {
	Kind      string               `json:"kind" enum:"user,group,condition,everyone" doc:"A subject, a group, everyone whose claims satisfy conditions, or everyone who signs in"`
	Subject   *string              `json:"subject" doc:"The subject, for kind user"`
	Group     *NamedID             `json:"group" doc:"The group, for kind group"`
	Predicate []PredicateCondition `json:"predicate" nullable:"false" doc:"The conditions, for kind condition"`
	Guarded   bool                 `json:"guarded" doc:"The rule has a guard (requires_predicate or requires_group_id) that narrows whom it reaches"`
}
