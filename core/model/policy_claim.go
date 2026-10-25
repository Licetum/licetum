package model

// PolicyClaim is a property of the subject, or an attribute of the request, that the policy of a service reads, and how.
type PolicyClaim struct {
	Name        string `json:"name" doc:"The property of the subject (a claim), or the request attribute"`
	Source      string `json:"source" enum:"claim,context" doc:"claim — read from subject.properties of the request; context — read from the context of the request"`
	Rules       int    `json:"rules" doc:"In how many conditions of rules and their guards that apply to the service"`
	Targets     int    `json:"targets" doc:"In how many claim-based targets (resource_from_claim) of those rules and their roles"`
	Memberships int    `json:"memberships" doc:"In how many conditions of group memberships (groups are shared by every service)"`
}
