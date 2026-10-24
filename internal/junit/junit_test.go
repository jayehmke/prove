package junit_test

import (
	"context"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/junit"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/run"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/verify"
)

// Shaped like gotestsum's output: nested suites, a failure, a skip, an error.
const report = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="5" failures="1" errors="1">
  <testsuite name="example.com/pkg" tests="5">
    <testcase classname="example.com/pkg" name="TestLogin/AC-1-1_signs_in" time="0.120"></testcase>
    <testcase classname="example.com/pkg" name="TestLogin/AC-1-2_rejects_bad_password" time="0.050">
      <failure message="Failed">expected 401</failure>
    </testcase>
    <testcase classname="example.com/pkg" name="TestLogout AC-1-2" time="0.010"></testcase>
    <testcase classname="example.com/pkg" name="TestSlow/AC-1-4" time="0"><skipped message="slow"></skipped></testcase>
    <testcase classname="example.com/pkg" name="TestPanics" time="0"><error message="panic"></error></testcase>
  </testsuite>
</testsuites>`

func TestRequirementsFromJUnit(t *testing.T) {
	tests, err := junit.Parse(strings.NewReader(report))
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, tc := range tests {
		results[tc.Name] = tc.Result
	}
	want := map[string]string{
		"TestLogin/AC-1-1_signs_in":             "pass",
		"TestLogin/AC-1-2_rejects_bad_password": "fail",
		"TestLogout AC-1-2":                     "pass",
		"TestSlow/AC-1-4":                       "skip",
		"TestPanics":                            "fail",
	}
	for name, r := range want {
		if results[name] != r {
			t.Errorf("%s = %q, want %q", name, results[name], r)
		}
	}
	reqs := map[string]record.Requirement{}
	for _, r := range junit.Requirements(tests, regexp.MustCompile(config.DefaultRequirementPattern)) {
		reqs[r.ID] = r
	}
	if reqs["AC-1-1"].Result != "pass" || reqs["AC-1-2"].Result != "fail" || reqs["AC-1-4"].Result != "skip" {
		t.Errorf("requirements = %+v", reqs)
	}
	if got := reqs["AC-1-2"].Tests; len(got) != 2 {
		t.Errorf("AC-1-2 tests = %v, want both", got)
	}

	// End to end: the record carries the tests, and verify enforces the
	// required IDs.
	alice := testutil.NewKey(t)
	repo := testutil.Repo(t)
	testutil.Write(t, repo, "fixtures/report.xml", report)
	testutil.Commit(t, repo, "first")
	policy, err := config.Parse([]byte(`
checks:
  - name: unit
    run: cp fixtures/report.xml "$PROVE_OUT/junit.xml"
    junit: $PROVE_OUT/junit.xml
signers:
  - identity: alice
    key: ` + alice.Public + `
requirements:
  required: [AC-1-1, AC-1-2, AC-1-3]
`))
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := policy.Check("unit")
	re, _ := policy.RequirementPattern()
	s := store.NewGit(repo)
	res, err := run.Run(context.Background(), run.Options{
		Repo: repo, Check: ch, Pattern: re, Signer: alice.Signer, Store: s, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Statement.Predicate
	if len(p.Tests) != 5 {
		t.Errorf("record has %d tests, want 5", len(p.Tests))
	}
	var ids []string
	for _, r := range p.Requirements {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"AC-1-1", "AC-1-2", "AC-1-4"}) {
		t.Errorf("record requirements = %v", ids)
	}

	trust, err := sign.NewTrust(policy.Signers, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := verify.Verify(context.Background(), verify.Options{
		Repo: repo, Commit: res.Statement.Commit(), Policy: policy, Store: s, Trust: trust,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verify passed with required IDs unmet")
	}
	failures := strings.Join(rep.Failures(), "\n")
	for _, id := range []string{"AC-1-2", "AC-1-3"} {
		if !strings.Contains(failures, id) {
			t.Errorf("failures don't name %s:\n%s", id, failures)
		}
	}
	if strings.Contains(failures, "AC-1-1") {
		t.Errorf("AC-1-1 passed but a failure names it:\n%s", failures)
	}
}
