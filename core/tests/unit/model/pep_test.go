package model_test

import (
	"slices"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func ptr[T any](v T) *T { return &v }

// A client is checked against its kind: a jwt client names an issuer and a client of it, an api_key client names neither.
func TestPEPClientInputFitsItsKind(t *testing.T) {
	ok := []model.PEPClientInput{
		{Name: "a", Kind: "api_key"},
		{Name: " b ", Kind: "jwt", Issuer: ptr("https://idp"), ClientID: ptr(" c ")},
	}
	for i := range ok {
		if err := ok[i].Validate(); err != nil {
			t.Errorf("%+v: %v", ok[i], err)
		}
	}
	if ok[1].Name != "b" || *ok[1].ClientID != "c" {
		t.Errorf("the strings are not trimmed: %+v", ok[1])
	}

	bad := map[string]model.PEPClientInput{
		"no name":                  {Kind: "api_key"},
		"a blank name":             {Name: "  ", Kind: "api_key"},
		"a name too long":          {Name: string(make([]byte, 101)), Kind: "api_key"},
		"an unknown kind":          {Name: "a", Kind: "cookie"},
		"jwt without an issuer":    {Name: "a", Kind: "jwt", ClientID: ptr("c")},
		"jwt without a client":     {Name: "a", Kind: "jwt", Issuer: ptr("i")},
		"jwt with a blank issuer":  {Name: "a", Kind: "jwt", Issuer: ptr(" "), ClientID: ptr("c")},
		"api_key with an issuer":   {Name: "a", Kind: "api_key", Issuer: ptr("i")},
		"api_key with a client id": {Name: "a", Kind: "api_key", ClientID: ptr("c")},
		"an unknown operation":     {Name: "a", Kind: "api_key", Operations: []string{"delete"}},
	}
	for name, in := range bad {
		if err := in.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The operations keep their order without repeats; an absent list stays absent (the default), an empty one stays empty.
func TestPEPOperationsAreDeduplicatedAndTheDefaultStaysAbsent(t *testing.T) {
	in := model.PEPClientInput{Name: "a", Kind: "api_key", Operations: []string{"search_resource", "evaluate", "search_resource"}}
	if err := in.Validate(); err != nil || !slices.Equal(in.Operations, []string{"search_resource", "evaluate"}) {
		t.Fatalf("operations = %v, %v", in.Operations, err)
	}
	absent := model.PEPClientInput{Name: "a", Kind: "api_key"}
	if err := absent.Validate(); err != nil || absent.Operations != nil {
		t.Fatalf("an absent list became %v, %v", absent.Operations, err)
	}
	none := model.PEPClientInput{Name: "a", Kind: "api_key", Operations: []string{}}
	if err := none.Validate(); err != nil || none.Operations == nil || len(none.Operations) != 0 {
		t.Fatalf("an empty list became %#v, %v", none.Operations, err)
	}
}

// A patch checks only what it carries.
func TestPEPClientPatchChecksWhatIsPresent(t *testing.T) {
	empty := model.PEPClientPatch{}
	if err := empty.Validate(); err != nil {
		t.Fatalf("an empty patch: %v", err)
	}
	renamed := model.PEPClientPatch{Name: ptr("  x ")}
	if err := renamed.Validate(); err != nil || *renamed.Name != "x" {
		t.Fatalf("rename: %v %v", renamed.Name, err)
	}
	for name, p := range map[string]model.PEPClientPatch{
		"a blank name":      {Name: ptr(" ")},
		"a blank issuer":    {Issuer: ptr("")},
		"unknown operation": {Operations: &[]string{"nope"}},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// An issuer: the name is kept exactly (it is compared with iss), the URL is checked, only asymmetric algorithms pass.
func TestPEPIssuerIsCheckedAndTheNameKeptExactly(t *testing.T) {
	in := model.PEPIssuer{Issuer: "https://idp.example.com/realms/a/", JWKSURL: "https://idp.example.com/keys",
		Audiences: []string{"b", " a ", "b"}, Algs: []string{"RS256", "ES256", "RS256"}}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	if in.Issuer != "https://idp.example.com/realms/a/" {
		t.Errorf("the issuer was changed: %q", in.Issuer)
	}
	if !slices.Equal(in.Audiences, []string{"a", "b"}) || !slices.Equal(in.Algs, []string{"ES256", "RS256"}) {
		t.Errorf("audiences %v, algs %v", in.Audiences, in.Algs)
	}
	bad := map[string]model.PEPIssuer{
		"no issuer":                 {JWKSURL: "https://k"},
		"an issuer with a space":    {Issuer: " https://idp", JWKSURL: "https://k"},
		"a jwks_url that is no URL": {Issuer: "i", JWKSURL: "keys"},
		"HMAC":                      {Issuer: "i", JWKSURL: "https://k", Algs: []string{"HS256"}},
		"none":                      {Issuer: "i", JWKSURL: "https://k", Algs: []string{"none"}},
		"an empty list of algs":     {Issuer: "i", JWKSURL: "https://k", Algs: []string{}},
		"a blank audience":          {Issuer: "i", JWKSURL: "https://k", Audiences: []string{""}},
		"too many audiences":        {Issuer: "i", JWKSURL: "https://k", Audiences: make17()},
	}
	for name, i := range bad {
		if err := i.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	defaults := model.PEPIssuer{Issuer: "i", JWKSURL: "https://k"}
	if err := defaults.Validate(); err != nil || defaults.Algs != nil {
		t.Errorf("absent algs became %v, %v", defaults.Algs, err)
	}
}

func make17() []string {
	out := make([]string, 17)
	for i := range out {
		out[i] = string(rune('a' + i))
	}
	return out
}
