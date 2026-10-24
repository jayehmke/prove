package junit_test

import (
	"context"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/junit"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/verify"
)

// report is shaped like gotestsum and jest output: nested suites, failures,
// errors and skips.
const report = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="pkg/a" tests="3">
    <testcase classname="pkg/a" name="TestLogin/AC-1-1_signs_in" time="0.01"></testcase>
    <testcase classname="pkg/a" name="TestLogin/AC-1-2_rejects_bad_password" time="0.01">
      <failure message="expected 401">got 200</failure>
    </testcase>
    <testsuite name="nested">
      <testcase classname="nested" name="covers AC-1-1 and AC-1-4"></testcase>
      <testcase classname="nested" name="AC-1-5 crashes"><error message="panic"/></testcase>
    </testsuite>
  </testsuite>
  <testsuite name="other">
    <testcase classname="other" name="AC-1-3 later"><skipped/></testcase>
  </testsuite>
</testsuites>
`

func TestRequirementsFromJUnit(t *testing.T) {
	t.Run("AC-807-9 JUnit tests and requirement IDs land in the record", func(t *testing.T) {
		tests, err := junit.Parse([]byte(report), regexp.MustCompile(`AC-\d+-\d+`))
		if err != nil {
			t.Fatal(err)
		}
		want := []record.Test{
			{Name: "TestLogin/AC-1-1_signs_in", Classname: "pkg/a", Result: "pass", Requirements: []string{"AC-1-1"}},
			{Name: "TestLogin/AC-1-2_rejects_bad_password", Classname: "pkg/a", Result: "fail", Requirements: []string{"AC-1-2"}},
			{Name: "covers AC-1-1 and AC-1-4", Classname: "nested", Result: "pass", Requirements: []string{"AC-1-1", "AC-1-4"}},
			{Name: "AC-1-5 crashes", Classname: "nested", Result: "fail", Requirements: []string{"AC-1-5"}},
			{Name: "AC-1-3 later", Classname: "other", Result: "skip", Requirements: []string{"AC-1-3"}},
		}
		if !reflect.DeepEqual(tests, want) {
			t.Fatalf("Parse =\n%+v\nwant\n%+v", tests, want)
		}
		if _, err := junit.Parse([]byte("not xml"), nil); err == nil {
			t.Fatal("garbage should not parse")
		}
	})

	t.Run("AC-807-9 a required ID with no passing test fails verify and is named", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		key := testutil.NewKey(t, "tester@example.com")
		testutil.WriteFile(t, repo, ".gitignore", "out/\n")
		testutil.WriteFile(t, repo, "report.xml", report)
		head := testutil.Commit(t, repo, "add report")
		policy, err := config.Parse([]byte(`
signers:
  tester: ` + key.Public + `
requirements:
  pattern: 'AC-\d+-\d+'
  required: [AC-1-1, AC-1-2, AC-1-3, AC-1-4, AC-1-9]
checks:
  - name: unit
    run: mkdir -p out && cp report.xml out/junit.xml
    junit: out/junit.xml
`))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		st := store.NewLocal(t.TempDir())
		check, _ := policy.Check("unit")
		out, err := runner.Run(ctx, runner.Options{
			Repo: repo, Store: st, Signer: key.Signer, Check: check,
			Requirements: regexp.MustCompile(policy.Requirements.Pattern), Stdout: io.Discard, Stderr: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(out.Statement.Predicate.Tests); got != 5 {
			t.Fatalf("record lists %d tests, want 5", got)
		}
		if reqs := out.Statement.Predicate.Tests[2].Requirements; !reflect.DeepEqual(reqs, []string{"AC-1-1", "AC-1-4"}) {
			t.Fatalf("record test requirements = %v", reqs)
		}

		trust, err := signer.NewTrustSet(policy.Signers)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := verify.Verify(ctx, verify.Options{Repo: repo, Store: st, Trust: trust, Policy: policy, Commit: head, Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK {
			t.Fatal("verify passed with required IDs uncovered")
		}
		failures := strings.Join(rep.Failures, "\n")
		for _, id := range []string{"AC-1-2", "AC-1-3", "AC-1-9"} {
			if !strings.Contains(failures, id) {
				t.Fatalf("failures should name %s:\n%s", id, failures)
			}
		}
		for _, id := range []string{"AC-1-1", "AC-1-4"} {
			if strings.Contains(failures, id+" ") || strings.HasSuffix(failures, id) {
				t.Fatalf("%s has a passing test and should not fail:\n%s", id, failures)
			}
		}
		cov := map[string]bool{}
		for _, r := range rep.Requirements {
			cov[r.ID] = r.Covered
		}
		if !cov["AC-1-1"] || !cov["AC-1-4"] || cov["AC-1-2"] || cov["AC-1-3"] || cov["AC-1-9"] {
			t.Fatalf("coverage = %v", cov)
		}
	})
}
