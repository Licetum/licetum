package tests

import (
	"errors"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The ratchet against the routes that AuthZEN replaced. The check API served /service/{ref}/permission, /check and
// /on_behalf/…, and the simulations of the admin API ended the same way (/simulate/check, /simulate/permission); AuthZEN serves
// the same questions under /access/v1/ (contract AZ10 pins the routes the servers have). The tests and the load test ask
// AuthZEN; this file keeps them from going back to the old routes, and from spelling the AuthZEN paths outside the request
// builders of core/testsupport/authzenreq.
//
// The lines that send an old route, or spell an AuthZEN path, are counted per file. A file that is not in allowed may have
// none. A listed file must have exactly the lines it is allowed: a new line fails, and so does a removed one, so the list can
// only shrink.

// self is this file, which names the routes it looks for.
const self = "tests/oldendpoints_test.go"

// conformance is the one file that spells the AuthZEN paths on purpose: the conformance suite of the AuthZEN API.
const conformance = "tests/integration/api/authzen_test.go"

// oldRoute matches a route of the check API by its last segment: /permission, /check and /on_behalf, which the simulations of
// the admin API share. The slash is required, so that an operation named "check" or the subcommand "check" is not a route.
var oldRoute = regexp.MustCompile(`/(permission|check|on_behalf)(/|\?|$)`)

// allowance is what a listed file may still hold: its lines that send an old route and its lines that spell an AuthZEN path.
type allowance struct {
	routes, paths int
	why           string
}

// allowed lists the files that still hold old routes or raw AuthZEN paths, and why. The k6 generator is JavaScript and builds its
// AuthZEN paths itself, and a test that refuses the retired operation names names them; the rest are lines to build with authzenreq.
var allowed = map[string]allowance{
	"loadtest/k6/check.js":                               {0, 3, "k6 is JavaScript: it builds its AuthZEN paths itself, not with authzenreq (a permanent exception)"},
	"loadtest/loadtest_test.go":                          {1, 0, "the names of the retired requests that ParseMix refuses (a test that they are refused)"},
	"tests/integration/api/deleted_synchronizer_test.go": {0, 1, "one AuthZEN path built from a variable, to build with authzenreq"},
	"tests/integration/api/graph_test.go":                {0, 1, "a raw AuthZEN path of the evaluations, to build with authzenreq"},
	"tests/integration/contracts/ask_test.go":            {0, 1, "a raw AuthZEN path expected from Ask, to build with authzenreq"},
	"tests/integration/harness/requests_test.go":         {0, 2, "the expected wire paths of the harness requests, to take from authzenreq"},
}

// finding is one line that the ratchet counts.
type finding struct {
	kind string // "route" or "path"
	line int
	text string
}

// scanRoots are the directories the ratchet reads, with the prefix that their files have in allowed.
var scanRoots = []struct{ prefix, dir string }{
	{"tests", "."},
	{"loadtest", "../loadtest"},
}

func TestOldEndpointsRatchet(t *testing.T) {
	found := map[string][]finding{}
	seen := map[string]bool{}
	for _, root := range scanRoots {
		err := filepath.WalkDir(root.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".js")) {
				return nil
			}
			rel, err := filepath.Rel(root.dir, path)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(filepath.Join(root.prefix, rel))
			if key == self {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out, err := findings(key, src)
			if err != nil {
				return err
			}
			seen[key] = true
			if len(out) > 0 {
				found[key] = out
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// A moved directory would scan nothing and pass: the conformance suite and the k6 script must be among the files read.
	if !seen[conformance] || !seen["loadtest/k6/check.js"] {
		t.Fatalf("the ratchet did not reach %s and loadtest/k6/check.js: the scanned directories moved", conformance)
	}

	keys := map[string]bool{}
	for k := range found {
		keys[k] = true
	}
	for k := range allowed {
		keys[k] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		var routes, paths int
		for _, f := range found[key] {
			if f.kind == "route" {
				routes++
			} else {
				paths++
			}
		}
		a, listed := allowed[key]
		switch {
		case !listed && routes+paths > 0:
			t.Errorf("%s sends %d line(s) of old routes and spells %d AuthZEN path line(s), and is not in allowed:\n%s",
				key, routes, paths, describe(key, found[key]))
		case listed && (routes != a.routes || paths != a.paths):
			t.Errorf("%s is allowed %d route and %d path line(s) (%s), it has %d and %d:\n%s%s",
				key, a.routes, a.paths, a.why, routes, paths, describe(key, found[key]), advice(a, routes, paths, key))
		}
	}
}

// advice says what a listed file must do to match its allowance.
func advice(a allowance, routes, paths int, key string) string {
	switch {
	case routes == 0 && paths == 0:
		return fmt.Sprintf("  %s holds none any more: remove its entry from allowed\n", key)
	case routes <= a.routes && paths <= a.paths:
		return fmt.Sprintf("  the allowance shrank: set %s to %d route and %d path line(s)\n", key, routes, paths)
	default:
		return "  a new line is outside the allowance: send it through core/testsupport/authzenreq or the AuthZEN API\n"
	}
}

// describe lists the lines of one file that the ratchet counts.
func describe(key string, list []finding) string {
	var b strings.Builder
	for _, f := range list {
		fmt.Fprintf(&b, "  %s:%d (%s): %s\n", key, f.line, f.kind, strings.TrimSpace(f.text))
	}
	return b.String()
}

// findings returns the lines of one file that the ratchet counts. A Go file is read as tokens, so comments (which document
// the routes) do not count; the load test's JavaScript is read line by line, as its traffic is written there.
func findings(key string, src []byte) ([]finding, error) {
	lines := strings.Split(string(src), "\n")
	values := map[int][]string{}
	if strings.HasSuffix(key, ".go") {
		var err error
		if values, err = stringsByLine(key, src); err != nil {
			return nil, err
		}
	} else {
		for i, line := range lines {
			values[i+1] = []string{line}
		}
	}
	var out []finding
	for _, n := range slices.Sorted(maps.Keys(values)) {
		vs := values[n]
		// A path built from a variable (service+"/check") has no /service/ on its line, so the line counts too, unless it
		// is about a role: /role/{id}/permission lists the items of a role, an admin route that stays.
		role := slices.ContainsFunc(vs, func(v string) bool { return strings.Contains(v, "/role/") })
		route := slices.ContainsFunc(vs, func(v string) bool {
			return strings.Contains(v, "on_behalf") || (oldRoute.MatchString(v) && !role)
		})
		if route {
			out = append(out, finding{kind: "route", line: n, text: lines[n-1]})
		}
		if key != conformance && slices.ContainsFunc(vs, func(v string) bool { return strings.Contains(v, "access/v1/") }) {
			out = append(out, finding{kind: "path", line: n, text: lines[n-1]})
		}
	}
	return out, nil
}

// stringsByLine returns the string literals of a Go file, by line.
func stringsByLine(key string, src []byte) (map[int][]string, error) {
	fset := token.NewFileSet()
	file := fset.AddFile(key, fset.Base(), len(src))
	var errs error
	var s scanner.Scanner
	s.Init(file, src, func(pos token.Position, msg string) {
		errs = errors.Join(errs, fmt.Errorf("%s: %s", pos, msg))
	}, 0)
	values := map[int][]string{}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING {
			continue
		}
		v, err := strconv.Unquote(lit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fset.Position(pos), err)
		}
		n := fset.Position(pos).Line
		values[n] = append(values[n], v)
	}
	return values, errs
}
