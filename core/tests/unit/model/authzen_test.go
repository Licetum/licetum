package model_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// rejectedWith fails the test unless err is an error of the catalog key.
func rejectedWith(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Key() != key {
		t.Fatalf("want the error %s, got %v", key, err)
	}
}

func subject(typ, id string) *model.AuthzenSubject {
	return &model.AuthzenSubject{Type: typ, ID: id}
}

func resource(typ, id string) *model.AuthzenResource {
	return &model.AuthzenResource{Type: typ, ID: id}
}

func action(name string) *model.AuthzenAction {
	return &model.AuthzenAction{Name: name}
}

// The evaluation needs the subject, the resource (its type and its id, where * is the type as a whole) and the action.
func TestContract_AZ01_EvaluationNeedsSubjectResourceAndAction(t *testing.T) {
	complete := func() model.AuthzenEvaluationRequest {
		return model.AuthzenEvaluationRequest{
			Subject:  subject("user", "alice"),
			Resource: resource("branch", "b1"),
			Action:   action("read"),
		}
	}
	cases := map[string]struct {
		edit func(*model.AuthzenEvaluationRequest)
		want i18n.Key // empty: accepted
	}{
		"complete":             {func(*model.AuthzenEvaluationRequest) {}, ""},
		"the type as a whole":  {func(r *model.AuthzenEvaluationRequest) { r.Resource.ID = model.AuthzenWholeType }, ""},
		"no subject":           {func(r *model.AuthzenEvaluationRequest) { r.Subject = nil }, i18n.AuthzenFieldRequired},
		"no subject type":      {func(r *model.AuthzenEvaluationRequest) { r.Subject.Type = "" }, i18n.AuthzenFieldRequired},
		"no subject id":        {func(r *model.AuthzenEvaluationRequest) { r.Subject.ID = "" }, i18n.AuthzenFieldRequired},
		"no resource":          {func(r *model.AuthzenEvaluationRequest) { r.Resource = nil }, i18n.AuthzenFieldRequired},
		"no resource type":     {func(r *model.AuthzenEvaluationRequest) { r.Resource.Type = "" }, i18n.AuthzenFieldRequired},
		"no resource id":       {func(r *model.AuthzenEvaluationRequest) { r.Resource.ID = "" }, i18n.AuthzenFieldRequired},
		"no action":            {func(r *model.AuthzenEvaluationRequest) { r.Action = nil }, i18n.AuthzenFieldRequired},
		"no action name":       {func(r *model.AuthzenEvaluationRequest) { r.Action.Name = "" }, i18n.AuthzenFieldRequired},
		"another subject type": {func(r *model.AuthzenEvaluationRequest) { r.Subject.Type = "service" }, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := complete()
			c.edit(&r)
			err := r.Validate()
			if c.want == "" {
				if err != nil {
					t.Fatalf("must be accepted: %v", err)
				}
				return
			}
			rejectedWith(t, err, c.want)
		})
	}
}

// A batch takes the subject, resource, action and context of the batch for the evaluations that omit them, and names the
// evaluation that lacks a field. Without evaluations the batch is one evaluation, which must be complete on its own.
func TestContract_AZ01_EvaluationsTakeTheDefaultsOfTheBatch(t *testing.T) {
	batch := model.AuthzenEvaluationsRequest{
		Subject: subject("user", "alice"),
		Action:  action("read"),
		Evaluations: []model.AuthzenEvaluationRequest{
			{Resource: resource("branch", "b1")},
			{Resource: resource("branch", "b2"), Action: action("write")},
		},
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("the defaults fill the evaluations: %v", err)
	}

	batch.Evaluations[1].Resource.ID = ""
	err := batch.Validate()
	rejectedWith(t, err, i18n.AuthzenFieldRequired)
	if ae, _ := apperr.As(err); !strings.Contains(ae.Detail, "evaluations[1].resource.id") {
		t.Fatalf("the error must name the evaluation: %q", ae.Detail)
	}

	single := model.AuthzenEvaluationsRequest{Resource: resource("branch", "b1"), Action: action("read")}
	rejectedWith(t, single.Validate(), i18n.AuthzenFieldRequired)
}

// The subject search takes the type of the subject, and not its id: the id is what the search returns.
func TestContract_AZ01_SubjectSearchIgnoresTheSubjectID(t *testing.T) {
	complete := func() model.AuthzenSubjectSearchRequest {
		return model.AuthzenSubjectSearchRequest{Subject: subject("user", ""), Resource: resource("branch", ""), Action: action("read")}
	}
	bare := complete()
	if err := bare.Validate(); err != nil {
		t.Fatalf("a subject search without a subject id must be accepted: %v", err)
	}
	withID := complete()
	withID.Subject.ID = "group:a" // not read by the search, so not refused either
	if err := withID.Validate(); err != nil {
		t.Fatalf("the subject id is ignored: %v", err)
	}
	cases := map[string]func(*model.AuthzenSubjectSearchRequest){
		"no subject":       func(r *model.AuthzenSubjectSearchRequest) { r.Subject = nil },
		"no subject type":  func(r *model.AuthzenSubjectSearchRequest) { r.Subject.Type = "" },
		"no resource":      func(r *model.AuthzenSubjectSearchRequest) { r.Resource = nil },
		"no resource type": func(r *model.AuthzenSubjectSearchRequest) { r.Resource.Type = "" },
		"no action":        func(r *model.AuthzenSubjectSearchRequest) { r.Action = nil },
		"no action name":   func(r *model.AuthzenSubjectSearchRequest) { r.Action.Name = "" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := complete()
			edit(&r)
			rejectedWith(t, r.Validate(), i18n.AuthzenFieldRequired)
		})
	}
}

// The resource search takes the type of the resource and its action, and not the resource id: the search lists the ids.
func TestContract_AZ01_ResourceSearchIgnoresTheResourceID(t *testing.T) {
	complete := func() model.AuthzenResourceSearchRequest {
		return model.AuthzenResourceSearchRequest{Subject: subject("user", "alice"), Resource: resource("branch", ""), Action: action("read")}
	}
	bare := complete()
	if err := bare.Validate(); err != nil {
		t.Fatalf("a resource search without a resource id must be accepted: %v", err)
	}
	cases := map[string]func(*model.AuthzenResourceSearchRequest){
		"no subject":       func(r *model.AuthzenResourceSearchRequest) { r.Subject = nil },
		"no subject id":    func(r *model.AuthzenResourceSearchRequest) { r.Subject.ID = "" },
		"no resource":      func(r *model.AuthzenResourceSearchRequest) { r.Resource = nil },
		"no resource type": func(r *model.AuthzenResourceSearchRequest) { r.Resource.Type = "" },
		"no action":        func(r *model.AuthzenResourceSearchRequest) { r.Action = nil },
		"no action name":   func(r *model.AuthzenResourceSearchRequest) { r.Action.Name = "" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := complete()
			edit(&r)
			rejectedWith(t, r.Validate(), i18n.AuthzenFieldRequired)
		})
	}
	withID := complete()
	withID.Resource.ID = "b1"
	if err := withID.Validate(); err != nil {
		t.Fatalf("the resource id is ignored: %v", err)
	}
}

// The action search has no action: the access levels are its answer. The resource id is optional.
func TestContract_AZ01_ActionSearchNeedsNoAction(t *testing.T) {
	for _, id := range []string{"", "b1", model.AuthzenWholeType} {
		r := model.AuthzenActionSearchRequest{Subject: subject("user", "alice"), Resource: resource("branch", id)}
		if err := r.Validate(); err != nil {
			t.Fatalf("an action search with resource id %q must be accepted: %v", id, err)
		}
	}
	cases := map[string]func(*model.AuthzenActionSearchRequest){
		"no subject":       func(r *model.AuthzenActionSearchRequest) { r.Subject = nil },
		"no subject id":    func(r *model.AuthzenActionSearchRequest) { r.Subject.ID = "" },
		"no resource":      func(r *model.AuthzenActionSearchRequest) { r.Resource = nil },
		"no resource type": func(r *model.AuthzenActionSearchRequest) { r.Resource.Type = "" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := model.AuthzenActionSearchRequest{Subject: subject("user", "alice"), Resource: resource("branch", "")}
			edit(&r)
			rejectedWith(t, r.Validate(), i18n.AuthzenFieldRequired)
		})
	}
}

// The subject of a request is a person, or a group's subject in the form group:<uuid>: the bounds of its type and its id
// are the ones of PrepareSubject, which the AuthZEN requests and the on_behalf routes share.
func TestContract_API03_SubjectTypeBounds(t *testing.T) {
	for typ, ok := range map[string]bool{
		strings.Repeat("t", 64): true,
		strings.Repeat("t", 65): false,
		"group":                 false,
		"groups":                true,
	} {
		_, err := model.PrepareSubject(typ, "alice", nil)
		if (err == nil) != ok {
			t.Errorf("subject type of %d bytes (%q): accepted %v, want %v", len(typ), typ, err == nil, ok)
		}
	}
	// On the AuthZEN path the type is required as well.
	r := model.AuthzenSubjectSearchRequest{Subject: subject(strings.Repeat("t", 65), ""), Resource: resource("branch", ""), Action: action("read")}
	rejectedWith(t, r.Validate(), i18n.AuthzenSubjectTypeInvalid)
}

func TestContract_API03_SubjectIDBounds(t *testing.T) {
	for _, c := range []struct {
		id string
		ok bool
	}{
		{strings.Repeat("a", 256), true},
		{strings.Repeat("a", 257), false},
		{"", false},
		{"a\x00b", false},
		{"a\tb", false},
		{"a\nb", false},
		{"group:" + strings.Repeat("a", 8), false},
		{"group", true},
		{"groups:a", true},
	} {
		if _, err := model.PrepareSubject("", c.id, nil); (err == nil) != c.ok {
			t.Errorf("subject id %q (%d bytes): accepted %v, want %v", c.id, len(c.id), err == nil, c.ok)
		}
	}
	// The AuthZEN request checks the same id, and the on_behalf routes refuse it with the same error.
	r := model.AuthzenEvaluationRequest{Subject: subject("user", strings.Repeat("a", 257)), Resource: resource("branch", "b1"), Action: action("read")}
	rejectedWith(t, r.Validate(), i18n.DelegationSubjectInvalid)
}

// The claims of a subject are at most 100 claims and 16 KiB of JSON, and the boundary is the one that is accepted.
func TestContract_API03_SubjectPropertyBounds(t *testing.T) {
	many := map[string]any{}
	for i := range 100 {
		many["c"+strconv.Itoa(i)] = 1
	}
	if _, err := model.PrepareSubject("", "alice", many); err != nil {
		t.Fatalf("100 claims must be accepted: %v", err)
	}
	many["one-more"] = 1
	if _, err := model.PrepareSubject("", "alice", many); err == nil {
		t.Fatal("101 claims must be refused")
	}

	// A claim with a string of n bytes encodes to base+n bytes; base is the encoding of the empty string.
	padded := func(n int) map[string]any { return map[string]any{"pad": strings.Repeat("x", n)} }
	base := len(mustJSON(t, padded(0)))
	const limit = 16 * 1024
	if _, err := model.PrepareSubject("", "alice", padded(limit-base)); err != nil {
		t.Fatalf("16 KiB of JSON must be accepted: %v", err)
	}
	if _, err := model.PrepareSubject("", "alice", padded(limit-base+1)); err == nil {
		t.Fatal("16 KiB + 1 byte of JSON must be refused")
	}

	// The subject search returns the ids and reads no claims, but the claims of its subject are bounded all the same.
	search := model.AuthzenSubjectSearchRequest{
		Subject:  &model.AuthzenSubject{Type: "user", Properties: many},
		Resource: resource("branch", ""),
		Action:   action("read"),
	}
	rejectedWith(t, search.Validate(), i18n.DelegationClaimsInvalid)
}

// preferred_username is the id unless the claims hold a truthy one; the claims are a copy.
func TestPrepareSubjectDefaultsPreferredUsername(t *testing.T) {
	props := map[string]any{"role": "auditor", "preferred_username": ""}
	prepared, err := model.PrepareSubject("user", "alice", props)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Claims["preferred_username"] != "alice" || prepared.Type != "user" || prepared.ID != "alice" {
		t.Fatalf("prepared: %+v", prepared)
	}
	if props["preferred_username"] != "" {
		t.Fatal("the properties of the caller must not change")
	}
	kept, _ := model.PrepareSubject("", "alice", map[string]any{"preferred_username": "bob"})
	if kept.Claims["preferred_username"] != "bob" {
		t.Fatalf("a truthy preferred_username is kept: %+v", kept.Claims)
	}
}

// The settings of Licetum are read from the top-level key licetum, and only from it.
func TestParseAuthzenContextReadsTheLicetumSettings(t *testing.T) {
	attrs, settings, err := model.ParseAuthzenContext(map[string]any{
		"licetum": map[string]any{"explain": true, "min_revision": "42"},
		"ip":      "10.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Explain || settings.MinRevision != "42" {
		t.Fatalf("settings: %+v", settings)
	}
	if _, ok := attrs["licetum"]; ok || attrs["ip"] != "10.0.0.1" || len(attrs) != 1 {
		t.Fatalf("the settings are not attributes: %+v", attrs)
	}
	// A nested licetum is an attribute like any other: it does not switch anything on.
	_, nested, err := model.ParseAuthzenContext(map[string]any{"ip": map[string]any{"licetum": map[string]any{"explain": true}}})
	if err != nil || nested.Explain {
		t.Fatalf("a nested licetum must not be read as the settings: %+v, %v", nested, err)
	}
	// The header of the PDP, 1.<r>, is a valid min_revision: the epoch is checked and only r is kept.
	for value, want := range map[string]string{"42": "42", "1.42": "42", "7.0": "0"} {
		_, got, err := model.ParseAuthzenContext(map[string]any{"licetum": map[string]any{"min_revision": value}})
		if err != nil || got.MinRevision != want {
			t.Errorf("min_revision %q: got %q, %v; want %q", value, got.MinRevision, err, want)
		}
	}
	for name, raw := range map[string]any{
		"not an object":         "yes",
		"null":                  nil,
		"an unknown field":      map[string]any{"verbose": true},
		"a number revision":     map[string]any{"min_revision": 42},
		"a revision not digits": map[string]any{"min_revision": "4x2"},
		"an epoch not digits":   map[string]any{"min_revision": "x.42"},
		"two dots":              map[string]any{"min_revision": "1.2.3"},
		"explain not a bool":    map[string]any{"explain": "yes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := model.ParseAuthzenContext(map[string]any{"licetum": raw})
			rejectedWith(t, err, i18n.AuthzenLicetumInvalid)
		})
	}
}

// licetum is not the name of an attribute: the check API refuses it as one.
func TestLicetumIsNotAnAttributeName(t *testing.T) {
	if model.ValidContextName("licetum") {
		t.Fatal("licetum is a valid attribute name")
	}
	if err := (model.RequestContext{"licetum": "x"}).Validate(); err == nil {
		t.Fatal("the legacy check refuses licetum as an attribute")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
