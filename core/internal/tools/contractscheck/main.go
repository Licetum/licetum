// Command contractscheck keeps contracts.yaml and the tests in step: every active contract has a test named
// TestContract_<ID>_<What>, every such test names a known contract, and a planned contract with a test is reported (it should
// be marked active). It reads Go test files and the web tests (*.test.ts, *.test.tsx) under the root of the repository.
//
//	go run ./internal/tools/contractscheck [-root ..]
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// contract is one entry of contracts.yaml.
type contract struct {
	ID        string `yaml:"id"`
	Statement string `yaml:"statement"`
	Oracle    string `yaml:"oracle"`
	Level     string `yaml:"level"`
	Phase     string `yaml:"phase"`
	Status    string `yaml:"status"`
}

var (
	validID     = regexp.MustCompile(`^[A-Z][A-Z0-9]{2,7}$`)
	testName    = regexp.MustCompile(`TestContract_([A-Z][A-Z0-9]{2,7})_[A-Za-z0-9_]+`)
	levels      = []string{"unit", "pg", "cql", "system", "fault", "multidc", "load", "helm", "web"}
	statuses    = []string{"planned", "active", "retired"}
	skippedDirs = []string{".git", "node_modules", ".next", "vendor", "reports", "dist"}
)

func main() {
	root := flag.String("root", "..", "root of the repository (contracts.yaml lives there)")
	flag.Parse()
	problems, err := check(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "contractscheck:", err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// check returns the problems found under root.
func check(root string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "contracts.yaml")) //nolint:gosec // the index of the repository named by the flag
	if err != nil {
		return nil, err
	}
	var doc struct {
		Contracts []contract `yaml:"contracts"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("contracts.yaml: %w", err)
	}
	if len(doc.Contracts) == 0 {
		return nil, errors.New("contracts.yaml lists no contracts")
	}

	var problems []string
	byID := map[string]contract{}
	for _, c := range doc.Contracts {
		switch {
		case !validID.MatchString(c.ID):
			problems = append(problems, fmt.Sprintf("contracts.yaml: id %q is not 3-8 capital letters and digits", c.ID))
		case byID[c.ID].ID != "":
			problems = append(problems, fmt.Sprintf("contracts.yaml: id %s appears twice", c.ID))
		}
		if strings.TrimSpace(c.Statement) == "" || strings.TrimSpace(c.Oracle) == "" || strings.TrimSpace(c.Phase) == "" {
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s needs a statement, an oracle and a phase", c.ID))
		}
		if !slices.Contains(levels, c.Level) {
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s has level %q, not one of %v", c.ID, c.Level, levels))
		}
		if !slices.Contains(statuses, c.Status) {
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s has status %q, not one of %v", c.ID, c.Status, statuses))
		}
		byID[c.ID] = c
	}

	tests, err := contractTests(root)
	if err != nil {
		return nil, err
	}
	for _, id := range sortedKeys(tests) {
		c, known := byID[id]
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("%s: names contract %s, which contracts.yaml does not list", tests[id][0], id))
		case c.Status == "planned":
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s is planned but tested by %s: mark it active", id, tests[id][0]))
		case c.Status == "retired":
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s is retired but still tested by %s", id, tests[id][0]))
		}
	}
	for _, c := range doc.Contracts {
		if c.Status == "active" && len(tests[c.ID]) == 0 {
			problems = append(problems, fmt.Sprintf("contracts.yaml: %s is active but no test is named TestContract_%s_*", c.ID, c.ID))
		}
	}
	return problems, nil
}

// contractTests maps the contract IDs named by tests under root to the files that name them.
func contractTests(root string) (map[string][]string, error) {
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(skippedDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".test.ts") && !strings.HasSuffix(name, ".test.tsx") {
			return nil
		}
		ids, err := idsIn(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, id := range ids {
			if !slices.Contains(out[id], rel) {
				out[id] = append(out[id], rel)
			}
		}
		return nil
	})
	return out, err
}

// idsIn returns the contract IDs named by the tests of one file.
func idsIn(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // a file of the repository found by the walk
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var ids []string
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		for _, m := range testName.FindAllStringSubmatch(scan.Text(), -1) {
			ids = append(ids, m[1])
		}
	}
	return ids, scan.Err()
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
