package model

// GrantPreview is what a rule would do if it were saved: how much it covers, whom it reaches, and what it meets. Nothing
// is saved.
type GrantPreview struct {
	Resources      int                `json:"resources" doc:"How many resources the rule covers now (the resources of the synced types it reaches, through the hierarchy)"`
	Types          []GrantPreviewType `json:"types" nullable:"false" doc:"The covered resources by type"`
	Sample         []string           `json:"sample" nullable:"false" doc:"The names of the first covered resources"`
	Capability     bool               `json:"capability" doc:"The rule is on a type without a source: it grants a permission without resources, so there is nothing to count"`
	Dynamic        bool               `json:"dynamic" doc:"The rule finds its resource from a claim of whoever asks, so what it covers depends on the person: Resources does not count it"`
	Who            AccessWho          `json:"who" doc:"Whom the rule applies to"`
	People         GrantPeople        `json:"people" doc:"The known subjects the rule would reach"`
	Conflicts      []GrantConflict    `json:"conflicts" nullable:"false" doc:"Denials and exceptions in force that meet the rule: a denial above it or inside it narrows or cancels what it grants"`
	AlreadyGranted []RuleRef          `json:"already_granted" nullable:"false" doc:"Rules in force that already give the same people the same levels on the same target"`
}

// GrantPreviewType is the covered resources of one type.
type GrantPreviewType struct {
	Type    string `json:"type" doc:"Name of the resource type"`
	Service string `json:"service" doc:"Name of its service"`
	Count   int    `json:"count" doc:"Covered resources of the type"`
}

// GrantPeople is the people of the subject directories a rule reaches.
type GrantPeople struct {
	Total    int           `json:"total" doc:"How many people of the directory"`
	Sample   []KnownPerson `json:"sample" nullable:"false" doc:"The first of them (up to 20)"`
	Registry int           `json:"registry" doc:"People of the directories of the rule's services that the rule was tried on; 0 — those directories are empty, nothing is said about people"`
	Limited  bool          `json:"limited" doc:"The directories are larger than what was tried"`
}

// GrantConflict is a denial or an exception in force that meets the previewed rule.
type GrantConflict struct {
	Position string    `json:"position" enum:"above,inside" doc:"above: it covers what the rule covers; inside: it covers a part of it"`
	Kind     string    `json:"kind" enum:"deny,exception" doc:"A denial, or an exception that lifts denials"`
	Rule     RuleRef   `json:"rule" doc:"The rule in force"`
	Name     *string   `json:"name" doc:"Its name; null when it has none"`
	Who      AccessWho `json:"who" doc:"Whom it applies to"`
}
