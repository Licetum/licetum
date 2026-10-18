package loadtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// warmUpServer is an AuthZEN API whose first searches list less than the policy grants, as the check server does while a
// type is still synchronizing. The lists are those of the successive searches of the warm-up (the last one is the final
// one); a search is paged by three, whatever the page limit says, and the evaluations answer as the evaluate field says.
type warmUpServer struct {
	lists [][]string
	// evaluate is the body of the answer to an evaluation and to an evaluations request.
	evaluate string
	// status is the status of a search, 200 when zero.
	status int

	mu    sync.Mutex
	polls int
	seen  []string // the paths asked, in order
}

func (s *warmUpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, r.URL.Path)
	if !strings.HasSuffix(r.URL.Path, "/search/resource") {
		_, _ = w.Write([]byte(s.evaluate))
		return
	}
	if s.status != 0 {
		http.Error(w, `{"detail":"nope"}`, s.status)
		return
	}
	var req struct {
		Page struct {
			Token string `json:"token"`
		} `json:"page"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	poll, offset := s.polls, 0
	if req.Page.Token != "" {
		_, rest, _ := strings.Cut(req.Page.Token, ":")
		poll, _ = strconv.Atoi(strings.SplitN(req.Page.Token, ":", 2)[0])
		offset, _ = strconv.Atoi(rest)
	} else {
		s.polls++
	}
	list := s.lists[min(poll, len(s.lists)-1)]
	end := min(offset+3, len(list))
	results := make([]map[string]any, 0, 3)
	for _, id := range list[offset:end] {
		results = append(results, map[string]any{"type": "document", "id": id})
	}
	page := map[string]any{"count": len(results)}
	if end < len(list) {
		page["next_token"] = fmt.Sprintf("%d:%d", poll, end)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "page": page})
}

func warmerFor(srv *httptest.Server, p Profile) warmer {
	tokens := make([]string, p.Users)
	return warmer{base: srv.URL, client: srv.Client(), tokens: tokens, backend: "backend", profile: p, retry: time.Millisecond}
}

// readable is the ids of the documents subject 0 may read under the policy of the profile.
func readable(p Profile) []string {
	var ids []string
	for d := range p.documents() {
		if p.expectedAllowed(warmSubject, d) {
			ids = append(ids, fmt.Sprintf("d-%d", d))
		}
	}
	return ids
}

// The first searches of a type that is synchronizing list less than the policy grants, or nothing, and say nothing else
// about it: the warm-up must go on asking until the list is whole, and only then probe the evaluations. The pages of a
// list are followed to the end.
func TestWarmUpWaitsForTheWholeList(t *testing.T) {
	p := Profile{Name: "tiny", Users: 20, Groups: 4, Rules: 200, Resources: 200}
	all := readable(p)
	if len(all) < 7 {
		t.Fatalf("the profile grants subject 0 only %d documents, the test needs a list of several pages", len(all))
	}
	api := &warmUpServer{
		lists:    [][]string{nil, all[:1], all[:len(all)-1], all},
		evaluate: `{"decision":true}`,
	}
	srv := httptest.NewServer(api)
	defer srv.Close()

	phases, err := warmerFor(srv, p).warmUp(t.Context())
	if err != nil {
		t.Fatalf("warmUp: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.polls != 4 {
		t.Errorf("the list was asked for %d times, want 4 (empty, one document, all but one, all)", api.polls)
	}
	var names []string
	for _, ph := range phases {
		names = append(names, ph.Name)
	}
	want := "warm-up search_resource (first synchronization),warm-up evaluation,warm-up evaluations10,warm-up evaluation_properties"
	if strings.Join(names, ",") != want {
		t.Errorf("phases = %q, want %q", names, want)
	}
	// The evaluations are probed after the list is whole, not before.
	firstProbe := -1
	for i, path := range api.seen {
		if !strings.HasSuffix(path, "/search/resource") {
			firstProbe = i
			break
		}
	}
	if firstProbe < 0 || strings.HasSuffix(api.seen[len(api.seen)-1], "/search/resource") {
		t.Errorf("the requests were %v: the evaluations must come after the last search", api.seen)
	}
}

func TestWarmUpRefusesWhatIsNotAnAnswerOfThePolicy(t *testing.T) {
	p := Profile{Name: "tiny", Users: 20, Groups: 4, Rules: 200, Resources: 200}
	all := readable(p)
	cases := []struct {
		name string
		api  *warmUpServer
		want string
	}{
		{"a list longer than the policy grants", &warmUpServer{lists: [][]string{append(append([]string{}, all...), "d-extra")}, evaluate: `{"decision":true}`},
			"may read"},
		{"a refused search", &warmUpServer{lists: [][]string{all}, status: http.StatusForbidden, evaluate: `{"decision":true}`},
			"status 403"},
		{"a denial of the owner", &warmUpServer{lists: [][]string{all}, evaluate: `{"decision":false}`},
			"may not read"},
		// A batch answers an evaluation it could not decide with decision false and context.error: not a grant.
		{"an evaluation that could not be decided", &warmUpServer{lists: [][]string{all}, evaluate: `{"decision":false,"context":{"error":{"status":400,"message":"x"}}}`},
			"may not read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.api)
			defer srv.Close()
			_, err := warmerFor(srv, p).warmUp(t.Context())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("warmUp = %v, want an error with %q", err, c.want)
			}
		})
	}
}
