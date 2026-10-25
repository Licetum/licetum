package server

import (
	"slices"
	"testing"
)

// TestContract_AZ05_MetadataNamesTheHTTPSIdentifierAndOnlyImplementedEndpoints: the PDP identifier of the metadata is
// PDP__PUBLIC_URL and the slug of the service, and the document lists the five endpoints the PDP serves, nothing else (ADR 0001,
// 7.3). A trailing slash of the public URL does not reach the identifier.
func TestContract_AZ05_MetadataNamesTheHTTPSIdentifierAndOnlyImplementedEndpoints(t *testing.T) {
	m := authzenMetadata("https://licetum.example.com/", "filials")
	const pdp = "https://licetum.example.com/service/filials"
	if m.PolicyDecisionPoint != pdp {
		t.Fatalf("policy_decision_point = %q, want %q", m.PolicyDecisionPoint, pdp)
	}
	for name, got := range map[string]string{
		"access_evaluation_endpoint":  m.AccessEvaluationEndpoint,
		"access_evaluations_endpoint": m.AccessEvaluationsEndpoint,
		"search_subject_endpoint":     m.SearchSubjectEndpoint,
		"search_action_endpoint":      m.SearchActionEndpoint,
		"search_resource_endpoint":    m.SearchResourceEndpoint,
	} {
		want := pdp + map[string]string{
			"access_evaluation_endpoint":  "/access/v1/evaluation",
			"access_evaluations_endpoint": "/access/v1/evaluations",
			"search_subject_endpoint":     "/access/v1/search/subject",
			"search_action_endpoint":      "/access/v1/search/action",
			"search_resource_endpoint":    "/access/v1/search/resource",
		}[name]
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// The capabilities are the three URNs of ADR 0001, 7.3, in that order.
	want := []string{"urn:licetum:whole-type-resource-id", "urn:licetum:context:min-revision", "urn:licetum:context:explain"}
	if !slices.Equal(m.Capabilities, want) {
		t.Errorf("capabilities = %v, want %v", m.Capabilities, want)
	}
	// A caller that changes the document it got does not change the next one.
	m.Capabilities[0] = "x"
	if again := authzenMetadata("https://licetum.example.com/", "filials"); !slices.Equal(again.Capabilities, want) {
		t.Errorf("the capabilities are shared between documents: %v", again.Capabilities)
	}
}
