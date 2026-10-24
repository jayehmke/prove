package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
)

const showPolicy = `
signers:
  tester: $TESTER
requirements:
  pattern: 'AC-\d+-\d+'
  required: [AC-9-1, AC-9-2]
checks:
  - name: unit
    run: mkdir -p out && printf '<testsuite><testcase name="AC-9-1 works"/></testsuite>' > out/junit.xml
    junit: out/junit.xml
    inputs: ["src/**"]
  - name: lint
    run: "true"
  - name: e2e
    run: "true"
`

func TestShowMarkdown(t *testing.T) {
	t.Run("AC-807-14 show prints one line per check and the requirement coverage, as markdown or JSON", func(t *testing.T) {
		repo, _ := setup(t, showPolicy)
		testutil.WriteFile(t, repo, ".gitignore", "out/\n")
		testutil.WriteFile(t, repo, "src/a.go", "package a\n")
		first := testutil.Commit(t, repo, "src")
		if code, out, errOut := prove(t, "run", "unit"); code != 0 {
			t.Fatalf("run unit: %d\n%s%s", code, out, errOut)
		}
		testutil.WriteFile(t, repo, "docs/a.md", "docs\n")
		testutil.Commit(t, repo, "docs")
		if code, out, errOut := prove(t, "run", "lint"); code != 0 {
			t.Fatalf("run lint: %d\n%s%s", code, out, errOut)
		}

		code, md, errOut := prove(t, "show", "--format", "md")
		if code != 0 {
			t.Fatalf("show exited %d\n%s", code, errOut)
		}
		line := func(name string) string {
			t.Helper()
			var found []string
			for _, l := range strings.Split(md, "\n") {
				if strings.Contains(l, "| "+name+" |") {
					found = append(found, l)
				}
			}
			if len(found) != 1 {
				t.Fatalf("want exactly one line for %s, got %d:\n%s", name, len(found), md)
			}
			return found[0]
		}
		unit, lint, e2e := line("unit"), line("lint"), line("e2e")
		for _, want := range []string{"pass", "tester", "reused", first[:7]} {
			if !strings.Contains(unit, want) {
				t.Fatalf("unit line %q should contain %q", unit, want)
			}
		}
		for _, want := range []string{"pass", "tester", "new"} {
			if !strings.Contains(lint, want) {
				t.Fatalf("lint line %q should contain %q", lint, want)
			}
		}
		if !strings.Contains(e2e, "missing") {
			t.Fatalf("e2e line %q should say missing", e2e)
		}
		if !strings.Contains(md, "1/2") || !strings.Contains(md, "AC-9-2") {
			t.Fatalf("markdown should show 1/2 requirements covered and name AC-9-2:\n%s", md)
		}

		code, js, errOut := prove(t, "show", "--format", "json")
		if code != 0 {
			t.Fatalf("show json exited %d\n%s", code, errOut)
		}
		var rep struct {
			OK     bool `json:"ok"`
			Checks []struct {
				Name, Status, Signer, RecordCommit string
				Reused                             bool
			} `json:"checks"`
			Requirements []struct {
				ID      string `json:"id"`
				Covered bool   `json:"covered"`
			} `json:"requirements"`
		}
		if err := json.Unmarshal([]byte(js), &rep); err != nil {
			t.Fatalf("show json is not JSON: %v\n%s", err, js)
		}
		if rep.OK || len(rep.Checks) != 3 || len(rep.Requirements) != 2 {
			t.Fatalf("json report = %+v", rep)
		}
		byName := map[string]int{}
		for i, c := range rep.Checks {
			byName[c.Name] = i
		}
		u, l, e := rep.Checks[byName["unit"]], rep.Checks[byName["lint"]], rep.Checks[byName["e2e"]]
		if u.Status != "pass" || !u.Reused || u.Signer != "tester" || u.RecordCommit != first {
			t.Fatalf("unit = %+v", u)
		}
		if l.Status != "pass" || l.Reused || l.Signer != "tester" {
			t.Fatalf("lint = %+v", l)
		}
		if e.Status != "missing" {
			t.Fatalf("e2e = %+v", e)
		}
		if code, _, _ := prove(t, "show", "--format", "yaml"); code != 2 {
			t.Fatal("an unknown format should be a usage error")
		}
	})
}
