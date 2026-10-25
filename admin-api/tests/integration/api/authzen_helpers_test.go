//go:build integration

package api_test

// The questions the tests of this package put to the check API are the ones of its AuthZEN API, built and read by
// core/testsupport/authzenreq: which resources a person may read (the resource search), what they may do on one (the
// action search), whether they may do it (the evaluations). The helpers below send them and turn the answers into what the
// tests assert on, so that the wire format is written in that package and nowhere here.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// ref is how the check API is told which service a question is about: the slug. The two services of the world have the
// slug of their name (a service the test made itself is named by its own slug, or by id, which the API takes as well).
func (w *world) ref(serviceID string) string {
	switch serviceID {
	case w.org:
		return "org"
	case w.appSvc:
		return "app"
	}
	return serviceID
}

// ask sends a request that authzenreq built to the check API as the holder of the token (no token: no Authorization).
func (e *env) ask(r authzenreq.Request, token string, headers ...string) (int, []byte) {
	e.t.Helper()
	status, _, raw := e.askWithHeader(r, token, headers...)
	return status, raw
}

// askWithHeader is ask with the headers of the answer, for the tests that assert an AuthZEN error (requireAuthzenError).
func (e *env) askWithHeader(r authzenreq.Request, token string, headers ...string) (int, http.Header, []byte) {
	e.t.Helper()
	// The headers of the request (the lever of authzenreq.Fresh) go first, so that the caller's own headers win.
	var own []string
	for k, v := range r.Header {
		own = append(own, k, v[0])
	}
	headers = append(own, headers...)
	if token != "" {
		headers = append([]string{"Authorization", "Bearer " + token}, headers...)
	}
	return e.doWithHeader(r.Method, e.check.URL+r.Path, r.Body, headers...)
}

// found is a resource that a search lists: its id and its name.
type found struct{ ID, Name string }

// searchAll is the resource search of the type and the action, as the holder of the token, with every page of it. It
// returns the resources by id, or the status of the first answer that is not a 200.
func (e *env) searchAll(ref, token, resourceType, action string, opts ...authzenreq.Option) (int, []found) {
	e.t.Helper()
	var all []found
	next := ""
	for pages := 0; ; pages++ {
		if pages > 1000 {
			e.t.Fatalf("the pages of the search of %s never end", resourceType)
		}
		req := authzenreq.SearchResource(ref, authzenreq.SubjectOf(token), resourceType, action,
			append(slices.Clone(opts), authzenreq.Page(0, next))...)
		status, body := e.ask(req, token)
		if status != http.StatusOK {
			return status, nil
		}
		ids, more, err := authzenreq.ResourceIDs(status, body)
		if err != nil {
			e.t.Fatalf("search of %s: %v", resourceType, err)
		}
		// The names are not in what authzenreq decodes: properties.name of each result.
		var decoded struct {
			Results []struct {
				Properties map[string]any `json:"properties"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Results) != len(ids) {
			e.t.Fatalf("search of %s: the results are not as the ids say: %v (%s)", resourceType, err, body)
		}
		for i, id := range ids {
			name, _ := decoded.Results[i].Properties["name"].(string)
			all = append(all, found{ID: id, Name: name})
		}
		if next = more; next == "" {
			return http.StatusOK, all
		}
	}
}

// search lists the ids of the resources of the type on which the holder of the token may do the action, by id; or the
// status of the answer when it is not a 200.
func (e *env) search(ref, token, resourceType, action string, opts ...authzenreq.Option) (int, []string) {
	e.t.Helper()
	status, resources := e.searchAll(ref, token, resourceType, action, opts...)
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	return status, ids
}

// permission asks the check API, as the holder of the token, which branches of the service they may read: the resource
// search of branch and read. It returns the status and the answer (an error as authzenAnswer reads it), for the tests that
// only need to know that the API answers for the service (by its slug or id) and the token, or that it has seen the person.
func (e *env) permission(serviceRef, token string) (int, map[string]any) {
	e.t.Helper()
	status, header, raw := e.permissionRaw(serviceRef, token)
	return status, authzenAnswer(e.t, status, header, raw)
}

// permissionRaw is permission with the headers and the body of the answer as they came.
func (e *env) permissionRaw(serviceRef, token string) (int, http.Header, []byte) {
	e.t.Helper()
	// A token that names nobody (a garbage one) gets a placeholder subject: a request must name a subject to be well formed,
	// and the check of the token then refuses it.
	subject := authzenreq.SubjectOf(token)
	if subject == "" {
		subject = "nobody"
	}
	return e.askWithHeader(authzenreq.SearchResource(serviceRef, subject, "branch", "read"), token)
}

// authzenAnswer is the answer of an AuthZEN request as a map. A success is its JSON object. An error is a JSON string, the
// message, and its code is in the Licetum-Error-Code header: the map reads it as {"detail", "extra": {"code"}}, as the
// errors of the admin API read, so that the callers that look at the code keep working.
func authzenAnswer(t *testing.T, status int, header http.Header, raw []byte) map[string]any {
	t.Helper()
	if status >= http.StatusBadRequest {
		var message string
		if json.Unmarshal(raw, &message) == nil {
			return map[string]any{"detail": message, "extra": map[string]any{"code": header.Get("Licetum-Error-Code")}}
		}
	}
	if len(raw) == 0 {
		return map[string]any{}
	}
	return decodeMap(t, raw)
}

// requireAuthzenError fails the test unless the answer is the error wantStatus with the code wantCode: the body is the
// message as a JSON string, and the code is in the Licetum-Error-Code header.
func requireAuthzenError(t *testing.T, status int, header http.Header, raw []byte, wantStatus int, wantCode string) {
	t.Helper()
	var message string
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("an AuthZEN error is its message as a JSON string: %v (%s)", err, raw)
	}
	if code := header.Get("Licetum-Error-Code"); status != wantStatus || code != wantCode {
		t.Fatalf("error = %d/%q, want %d/%q (%s)", status, code, wantStatus, wantCode, raw)
	}
}

// visible lists the ids of the resources of the type that the person with the claims may read, asked with
// consistency=higher (neither the result cache nor the source throttle is used) and with the request attributes of the
// context, when there are any.
func (w *world) visible(serviceID string, claims jwt.MapClaims, context map[string]any, resourceType string) []string {
	w.t.Helper()
	opts := []authzenreq.Option{authzenreq.Fresh()}
	if context != nil {
		opts = append(opts, authzenreq.WithContext(context))
	}
	status, ids := w.search(w.ref(serviceID), w.token(claims), resourceType, "read", opts...)
	if status != http.StatusOK {
		w.t.Fatalf("search of %s: %d", resourceType, status)
	}
	return ids
}

// expectVisible fails the test unless the person with the claims may read exactly the resources of the type that are wanted.
func (w *world) expectVisible(serviceID string, claims jwt.MapClaims, context map[string]any, resourceType string, want ...string) {
	w.t.Helper()
	if got := w.visible(serviceID, claims, context, resourceType); !slices.Equal(got, want) {
		w.t.Fatalf("%s: got %v, want %v", resourceType, got, want)
	}
}

// listedResource is a resource a person may do something on, with the levels they have on it: what the list of the
// permissions of a service held for each of its resources.
type listedResource struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Access []string `json:"access"`
}

// listedLevels lists the resources of the type on which the holder of the token has at least one of the actions, by id,
// each with the levels it has among them (sorted). The resource search answers for one action, so it is asked once per
// action and the lists are joined. A level that no action names would go unseen that way, so every listed resource is
// also put to the action search, which names all of its levels: the two must agree, or the test names an action too few.
func (e *env) listedLevels(ref, token, resourceType string, actions ...string) []listedResource {
	e.t.Helper()
	byID := map[string]*listedResource{}
	for _, action := range actions {
		status, resources := e.searchAll(ref, token, resourceType, action)
		if status != http.StatusOK {
			e.t.Fatalf("search of %s for %s: %d", resourceType, action, status)
		}
		for _, r := range resources {
			if byID[r.ID] == nil {
				byID[r.ID] = &listedResource{ID: r.ID, Name: r.Name}
			}
			byID[r.ID].Access = append(byID[r.ID].Access, action)
		}
	}
	out := make([]listedResource, 0, len(byID))
	for _, r := range byID {
		slices.Sort(r.Access)
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b listedResource) int { return strings.Compare(a.ID, b.ID) })

	for _, r := range out {
		status, body := e.ask(authzenreq.SearchAction(ref, authzenreq.SubjectOf(token), authzenreq.On(resourceType, r.ID)), token)
		levels, _, err := authzenreq.Actions(status, body)
		if err != nil {
			e.t.Fatalf("actions on %s %s: %v", resourceType, r.ID, err)
		}
		if !slices.Equal(levels, r.Access) {
			e.t.Fatalf("%s %s: the action search names %v, the searches for %v found %v", resourceType, r.ID, levels, actions, r.Access)
		}
	}
	return out
}

// pdpRequest is the HTTP request that sends the question to the PDP at base (the address of a replica) as the holder of the token
// asks it (see askWithHeader), for a test that sends it with a client of its own.
func (e *env) pdpRequest(base string, r authzenreq.Request, token string) *http.Request {
	e.t.Helper()
	var headers []string
	for k, v := range r.Header {
		headers = append(headers, k, v[0])
	}
	if token != "" {
		headers = append(headers, "Authorization", "Bearer "+token)
	}
	headers, body := e.asPEP(base+r.Path, headers, r.Body)
	payload, err := json.Marshal(body)
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(r.Method, base+r.Path, bytes.NewReader(payload))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return req
}
