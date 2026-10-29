package main

import (
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/testutil"
)

// skewPolicy's web check fails, like a TypeScript TS2305, when app.ts uses a
// name lib.ts doesn't export.
const skewPolicy = `
signers:
  tester: $TESTER
checks:
  - name: web
    run: sh web/check.sh
    inputs: ["web/**"]
`

const skewCheck = `for n in $(sed -n 's/^uses //p' web/app.ts); do
  grep -qx "export $n" web/lib.ts || { echo "TS2305: lib has no exported member $n"; exit 1; }
done
`

// skewRepo makes main and a feature branch from the same base. feature
// starts using toKeepKey; main, meanwhile, stops exporting it. Each branch
// passes alone; merged, web fails. feature has a passing record of its own.
func skewRepo(t *testing.T) (*gitx.Repo, string) {
	t.Helper()
	repo, _ := setup(t, skewPolicy)
	testutil.WriteFile(t, repo, "web/check.sh", skewCheck)
	testutil.WriteFile(t, repo, "web/lib.ts", "export toKeepKey\nexport other\n")
	testutil.WriteFile(t, repo, "web/app.ts", "uses other\n")
	testutil.Commit(t, repo, "base")
	testutil.Git(t, repo.Dir, "checkout", "-q", "-b", "feature")
	testutil.WriteFile(t, repo, "web/app.ts", "uses other\nuses toKeepKey\n")
	feature := testutil.Commit(t, repo, "use toKeepKey")
	if code, _, errOut := prove(t, "run"); code != 0 {
		t.Fatalf("feature alone should pass: %s", errOut)
	}
	testutil.Git(t, repo.Dir, "checkout", "-q", "main")
	testutil.WriteFile(t, repo, "web/lib.ts", "export other\n")
	testutil.Commit(t, repo, "drop toKeepKey")
	testutil.Git(t, repo.Dir, "checkout", "-q", "feature")
	return repo, feature
}

func TestOntoCatchesMergeSkew(t *testing.T) {
	t.Run("merge skew: a branch's own record does not cover it merged onto a base that changed the check's inputs", func(t *testing.T) {
		repo, feature := skewRepo(t)
		refsBefore := testutil.Git(t, repo.Dir, "for-each-ref", "refs/heads/")

		// The gap: the branch alone verifies.
		if code, out, _ := prove(t, "verify", feature); code != 0 {
			t.Fatalf("feature alone should verify:\n%s", out)
		}

		code, out, errOut := prove(t, "plan", feature, "--onto", "main")
		if code != 0 {
			t.Fatalf("prove plan --onto exited %d: %s", code, errOut)
		}
		if !strings.Contains(out, "web  run (inputs changed since the record made on "+short(feature)) {
			t.Fatalf("plan --onto main should rerun web because main changed its inputs:\n%s", out)
		}
		if code, out, _ := prove(t, "verify", feature, "--onto", "main"); code != 1 {
			t.Fatalf("verify --onto main exited %d, want 1:\n%s", code, out)
		}

		code, out, errOut = prove(t, "run", "--stale", "--commit", feature, "--onto", "main")
		if code != 1 || !strings.Contains(out, "TS2305: lib has no exported member toKeepKey") {
			t.Fatalf("run --stale --onto main exited %d, want 1 with the merge's failure:\n%s%s", code, out, errOut)
		}

		// The merge is made off to the side: no branch, HEAD or file moved.
		if got := testutil.Git(t, repo.Dir, "for-each-ref", "refs/heads/"); got != refsBefore {
			t.Fatalf("branches moved:\n%s\nwant:\n%s", got, refsBefore)
		}
		if head := testutil.Head(t, repo); head != feature {
			t.Fatalf("HEAD moved to %s", head)
		}
		if status := testutil.Git(t, repo.Dir, "status", "--porcelain"); status != "" {
			t.Fatalf("the working tree changed:\n%s", status)
		}
	})

	t.Run("a base change outside the check's inputs reuses the branch's record", func(t *testing.T) {
		repo, _ := setup(t, skewPolicy)
		testutil.WriteFile(t, repo, "web/check.sh", skewCheck)
		testutil.WriteFile(t, repo, "web/lib.ts", "export other\n")
		testutil.WriteFile(t, repo, "web/app.ts", "uses other\n")
		testutil.Commit(t, repo, "base")
		testutil.Git(t, repo.Dir, "checkout", "-q", "-b", "feature")
		testutil.WriteFile(t, repo, "web/app.ts", "uses other\nuses other\n")
		testutil.Commit(t, repo, "feature")
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatal(errOut)
		}
		testutil.Git(t, repo.Dir, "checkout", "-q", "main")
		testutil.WriteFile(t, repo, "docs/notes.md", "outside web\n")
		testutil.Commit(t, repo, "docs")
		testutil.Git(t, repo.Dir, "checkout", "-q", "feature")

		code, out, errOut := prove(t, "plan", "--onto", "main")
		if code != 0 || !strings.Contains(out, "web  accept (signed record) by tester, reused") {
			t.Fatalf("plan --onto main exited %d, want web accepted:\n%s%s", code, out, errOut)
		}
		if code, out, _ := prove(t, "verify", "--onto", "main"); code != 0 {
			t.Fatalf("verify --onto main:\n%s", out)
		}
	})

	t.Run("the same branch and base always make the same merge commit; a branch that contains the base is itself", func(t *testing.T) {
		repo, feature := skewRepo(t)
		ctx := t.Context()
		a, err := repo.MergeOnto(ctx, feature, "main")
		if err != nil {
			t.Fatal(err)
		}
		b, err := repo.MergeOnto(ctx, "feature", "main")
		if err != nil {
			t.Fatal(err)
		}
		if a != b || a == feature {
			t.Fatalf("merge commits %s and %s (feature %s)", a, b, feature)
		}
		if self, err := repo.MergeOnto(ctx, a, "main"); err != nil || self != a {
			t.Fatalf("a commit that contains main merged onto main = %s, %v; want itself %s", self, err, a)
		}
	})

	t.Run("a branch that conflicts with the base is a setup error, not a pass", func(t *testing.T) {
		repo, _ := setup(t, skewPolicy)
		testutil.WriteFile(t, repo, "web/check.sh", "true\n")
		testutil.WriteFile(t, repo, "web/lib.ts", "base\n")
		testutil.Commit(t, repo, "base")
		testutil.Git(t, repo.Dir, "checkout", "-q", "-b", "feature")
		testutil.WriteFile(t, repo, "web/lib.ts", "feature\n")
		testutil.Commit(t, repo, "feature")
		testutil.Git(t, repo.Dir, "checkout", "-q", "main")
		testutil.WriteFile(t, repo, "web/lib.ts", "main\n")
		testutil.Commit(t, repo, "main")
		testutil.Git(t, repo.Dir, "checkout", "-q", "feature")
		code, _, errOut := prove(t, "verify", "--onto", "main")
		if code != 2 || !strings.Contains(errOut, "does not merge cleanly onto main") {
			t.Fatalf("verify --onto a conflicting base exited %d:\n%s", code, errOut)
		}
	})
}
