package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
)

const junitAC1 = `<testsuites><testsuite name="pkg">
<testcase name="TestA/AC-1-1_works" classname="pkg" time="0.01"/>
</testsuite></testsuites>`

func TestShowMarkdown(t *testing.T) {
	isolate(t)
	key := testutil.NewKey(t)
	t.Setenv("PROVE_SIGNING_KEY", key.Path)
	repo := testutil.Repo(t)
	testutil.Write(t, repo, ".prove.yaml", policy(
		"checks:",
		"  - name: unit",
		"    run: cp fixtures/report.xml \"$PROVE_OUT/report.xml\"",
		"    inputs: [\"src/**\", \"fixtures/**\"]",
		"    junit: $PROVE_OUT/report.xml",
		"  - name: lint",
		"    run: \"false\"",
		"  - name: docs",
		"    run: \"true\"",
		"signers:",
		"  - identity: alice",
		"    key: "+key.Public,
		"requirements:",
		"  required: [AC-1-1, AC-1-2]",
	))
	testutil.Write(t, repo, "src/a.txt", "a")
	testutil.Write(t, repo, "fixtures/report.xml", junitAC1)
	testutil.Commit(t, repo, "first")
	if code, _, stderr := prove(t, repo, "run", "unit"); code != 0 {
		t.Fatalf("run unit: %s", stderr)
	}
	testutil.Write(t, repo, "README", "undeclared for unit")
	testutil.Commit(t, repo, "second")
	if code, _, _ := prove(t, repo, "run", "lint"); code == 0 {
		t.Fatal("run lint should exit non-zero")
	}

	code, md, stderr := prove(t, repo, "show", "--format", "md")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, stderr)
	}
	line := func(check string) string {
		for _, l := range strings.Split(md, "\n") {
			if strings.Contains(l, "`"+check+"`") {
				return l
			}
		}
		t.Fatalf("no line for %s in:\n%s", check, md)
		return ""
	}
	wantIn := func(l string, parts ...string) {
		t.Helper()
		for _, p := range parts {
			if !strings.Contains(l, p) {
				t.Errorf("line %q lacks %q", l, p)
			}
		}
	}
	wantIn(line("unit"), "pass", "alice", "reused")
	wantIn(line("lint"), "fail", "alice", "new")
	wantIn(line("docs"), "missing")
	wantIn(md, "1/2", "AC-1-2")

	code, js, stderr := prove(t, repo, "show", "--format", "json")
	if code != 0 {
		t.Fatalf("show json exited %d: %s", code, stderr)
	}
	var rep struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			Result string `json:"result"`
			Signer string `json:"signer"`
			Reused bool   `json:"reused"`
		} `json:"checks"`
		Requirements []struct {
			ID  string `json:"id"`
			Met bool   `json:"met"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal([]byte(js), &rep); err != nil {
		t.Fatalf("show json isn't JSON: %v\n%s", err, js)
	}
	got := map[string]string{}
	for _, c := range rep.Checks {
		got[c.Name] = c.Result + "/" + c.Signer + "/" + map[bool]string{true: "reused", false: "new"}[c.Reused]
	}
	want := map[string]string{"unit": "pass/alice/reused", "lint": "fail/alice/new", "docs": "missing//new"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("json check %s = %q, want %q", k, got[k], v)
		}
	}
	met := map[string]bool{}
	for _, r := range rep.Requirements {
		met[r.ID] = r.Met
	}
	if rep.OK || !met["AC-1-1"] || met["AC-1-2"] {
		t.Errorf("json ok=%v requirements=%v", rep.OK, met)
	}
}
