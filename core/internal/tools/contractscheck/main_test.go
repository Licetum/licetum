package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tc is the prefix of the test names, kept apart so that this file names no contract to the real check.
const tc = "TestContract"

const index = `contracts:
  - {id: DEC01, statement: a, oracle: b, level: unit, phase: P1, status: active}
  - {id: DEC02, statement: a, oracle: b, level: unit, phase: P1, status: planned}
`

func TestCheckAcceptsAnActiveContractWithItsTest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "contracts.yaml", index)
	write(t, root, "core/decide/x_test.go", "func "+tc+"_DEC01_DenialWins(t *testing.T) {}\n")
	problems, err := check(root)
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems %v, err %v", problems, err)
	}
}

func TestCheckReportsEachKindOfDrift(t *testing.T) {
	root := t.TempDir()
	write(t, root, "contracts.yaml", index+"  - {id: x1, statement: a, oracle: b, level: nowhere, phase: P1, status: done}\n")
	write(t, root, "core/a_test.go", "func "+tc+"_DEC02_Planned(t *testing.T) {}\nfunc "+tc+"_ZZZ01_Unknown(t *testing.T) {}\n")
	write(t, root, "web/tests/b.test.ts", "test('"+tc+"_ZZZ02_Web', () => {})\n")
	write(t, root, "web/node_modules/c.test.ts", "test('"+tc+"_ZZZ03_Ignored', () => {})\n")
	problems, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{
		`id "x1" is not`, `level "nowhere"`, `status "done"`, // malformed entry
		"DEC01 is active but no test", // active without a test
		"DEC02 is planned but tested", // planned with a test
		"names contract ZZZ01",        // unknown, Go
		"names contract ZZZ02",        // unknown, web
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "ZZZ03") {
		t.Errorf("node_modules was read:\n%s", all)
	}
}
