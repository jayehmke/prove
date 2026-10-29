package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

// countPolicy has two checks that each append a line to $PROVE_COUNT/<name>
// when they run, outside the repository so the tree stays clean.
const countPolicy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: 'echo ran >> "$PROVE_COUNT/unit"'
    inputs: ["src/**"]
  - name: lint
    run: 'echo ran >> "$PROVE_COUNT/lint"'
    inputs: ["src/**"]
`

// runs reports how many times each check ran.
func runs(t *testing.T, dir string) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, name := range []string{"unit", "lint"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		got[name] = strings.Count(string(data), "ran\n")
	}
	return got
}

// countSetup makes a repository with countPolicy and one commit of src/,
// and returns it with the folder the checks count their runs in.
func countSetup(t *testing.T) (*gitx.Repo, string) {
	t.Helper()
	repo, _ := setup(t, countPolicy)
	count := t.TempDir()
	t.Setenv("PROVE_COUNT", count)
	testutil.WriteFile(t, repo, "src/a.go", "package a\n")
	testutil.Commit(t, repo, "src")
	return repo, count
}

func TestRunStaleSkipsCurrentRecords(t *testing.T) {
	t.Run("AC-905-1 prove run --stale twice on the same commit runs each check once", func(t *testing.T) {
		_, count := countSetup(t)
		for i := range 2 {
			if code, out, errOut := prove(t, "run", "--stale"); code != 0 {
				t.Fatalf("run %d: prove run --stale exited %d\n%s%s", i+1, code, out, errOut)
			}
		}
		if got := runs(t, count); got["unit"] != 1 || got["lint"] != 1 {
			t.Fatalf("want each check run once, got %v", got)
		}
		// With nothing to run, no signing key is needed (a CI job without one).
		t.Setenv("PROVE_SSH_KEY", filepath.Join(t.TempDir(), "missing"))
		if code, _, errOut := prove(t, "run", "--stale"); code != 0 {
			t.Fatalf("prove run --stale with nothing to run needed a key: exit %d\n%s", code, errOut)
		}
	})

	t.Run("runs only the checks with no current record; a commit that changes no input runs none", func(t *testing.T) {
		repo, count := countSetup(t)
		if code, _, errOut := prove(t, "run", "unit"); code != 0 {
			t.Fatalf("prove run unit: %s", errOut)
		}
		code, _, errOut := prove(t, "run", "--stale")
		if code != 0 {
			t.Fatalf("prove run --stale exited %d: %s", code, errOut)
		}
		if got := runs(t, count); got["unit"] != 1 || got["lint"] != 1 {
			t.Fatalf("want unit once (its record is current) and lint once (no record), got %v", got)
		}
		if !strings.Contains(errOut, "lint is stale (no record)") {
			t.Fatalf("stderr should say why lint ran:\n%s", errOut)
		}

		// A commit that changes no declared input reuses both records.
		testutil.WriteFile(t, repo, "docs/notes.md", "undeclared\n")
		testutil.Commit(t, repo, "docs")
		if code, _, errOut := prove(t, "run", "--stale"); code != 0 {
			t.Fatalf("prove run --stale exited %d: %s", code, errOut)
		}
		if got := runs(t, count); got["unit"] != 1 || got["lint"] != 1 {
			t.Fatalf("a commit that changed no input reran checks: %v", got)
		}
	})
}

func TestRunCommitIgnoresDirtyTree(t *testing.T) {
	t.Run("AC-905-2 prove run --commit records the check for that commit and leaves the working tree untouched", func(t *testing.T) {
		repo, _ := setup(t, runPolicy)
		testutil.WriteFile(t, repo, "src/a.go", "package a\n")
		commit := testutil.Commit(t, repo, "src")
		// Uncommitted work: a modified tracked file and an untracked one.
		testutil.WriteFile(t, repo, "src/a.go", "package a // wip\n")
		testutil.WriteFile(t, repo, "wip.txt", "untracked\n")
		statusBefore := testutil.Git(t, repo.Dir, "status", "--porcelain")
		worktreesBefore := testutil.Git(t, repo.Dir, "worktree", "list")

		code, out, errOut := prove(t, "run", "unit", "--commit", commit[:10])
		if code != 0 {
			t.Fatalf("prove run --commit exited %d\n%s%s", code, out, errOut)
		}
		recs := records(t, repo, commit)
		if len(recs) != 1 || recs[0].Commit() != commit || recs[0].Tree() != testutil.Tree(t, repo, commit) {
			t.Fatalf("want one record of %s, got %d", commit, len(recs))
		}
		if got := testutil.Git(t, repo.Dir, "status", "--porcelain"); got != statusBefore {
			t.Fatalf("working tree changed:\nbefore:\n%s\nafter:\n%s", statusBefore, got)
		}
		data, err := os.ReadFile(filepath.Join(repo.Dir, "src/a.go"))
		if err != nil || string(data) != "package a // wip\n" {
			t.Fatalf("uncommitted edit lost: %q %v", data, err)
		}
		if got := testutil.Git(t, repo.Dir, "worktree", "list"); got != worktreesBefore {
			t.Fatalf("the temporary worktree was left behind:\n%s", got)
		}
		if code, out, _ := prove(t, "verify", commit); code != 0 {
			t.Fatalf("prove verify %s:\n%s", commit, out)
		}
	})

	t.Run("the check runs on the commit's files, not the working tree's", func(t *testing.T) {
		repo, _ := setup(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "grep -q committed src/a.go"
`)
		testutil.WriteFile(t, repo, "src/a.go", "committed\n")
		commit := testutil.Commit(t, repo, "src")
		testutil.WriteFile(t, repo, "src/a.go", "wip\n")
		if code, out, errOut := prove(t, "run", "--commit", commit); code != 0 {
			t.Fatalf("the check saw the working tree, not the commit: exit %d\n%s%s", code, out, errOut)
		}
	})
}

func TestPushAsBranch(t *testing.T) {
	t.Run("AC-905-3 prove push --as-branch x pushes each record as refs/heads/x-prove-<id>", func(t *testing.T) {
		repo, _ := countSetup(t)
		wd := repo.Dir
		remote := filepath.Join(t.TempDir(), "remote.git")
		testutil.Git(t, wd, "init", "-q", "--bare", remote)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		ids := strings.Fields(testutil.Git(t, wd, "for-each-ref", "--format=%(refname:lstrip=3)", store.RefPrefix))
		if len(ids) != 2 {
			t.Fatalf("want 2 local records, got %v", ids)
		}

		code, out, errOut := prove(t, "push", remote, "--as-branch", "x")
		if code != 0 {
			t.Fatalf("prove push exited %d\n%s%s", code, out, errOut)
		}
		var want []string
		for _, id := range ids {
			want = append(want, "refs/heads/x-prove-"+id)
		}
		got := strings.Fields(testutil.Git(t, remote, "for-each-ref", "--format=%(refname)"))
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("remote refs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		// Each branch holds the record itself.
		for _, id := range ids {
			local := testutil.Git(t, wd, "rev-parse", store.RefPrefix+id)
			if pushed := testutil.Git(t, remote, "rev-parse", "refs/heads/x-prove-"+id); pushed != local {
				t.Fatalf("x-prove-%s is %s, want the record commit %s", id, pushed, local)
			}
		}

		// Pushing again is a no-op, not an error.
		if code, _, errOut := prove(t, "push", remote, "--as-branch", "x"); code != 0 {
			t.Fatalf("a second push exited %d: %s", code, errOut)
		}
	})

	t.Run("without --as-branch records go to refs/prove/records and a clone verifies", func(t *testing.T) {
		repo, _ := countSetup(t)
		remote := filepath.Join(t.TempDir(), "remote.git")
		testutil.Git(t, repo.Dir, "init", "-q", "--bare", remote)
		testutil.Git(t, repo.Dir, "push", "-q", remote, "HEAD:refs/heads/main")
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", remote); code != 0 {
			t.Fatalf("prove push: %s", errOut)
		}
		clone := filepath.Join(t.TempDir(), "clone")
		testutil.Git(t, repo.Dir, "clone", "-q", remote, clone)
		testutil.Git(t, clone, "fetch", "-q", "origin", "refs/prove/*:refs/prove/*")
		t.Chdir(clone)
		if code, out, _ := prove(t, "verify"); code != 0 {
			t.Fatalf("a clone with the pushed records fails verify:\n%s", out)
		}
	})

	t.Run("a hook can tell prove's own pushes apart", func(t *testing.T) {
		repo, _ := countSetup(t)
		remote := filepath.Join(t.TempDir(), "remote.git")
		testutil.Git(t, repo.Dir, "init", "-q", "--bare", remote)
		seen := filepath.Join(t.TempDir(), "seen")
		hook := filepath.Join(repo.Dir, ".git", "hooks", "pre-push")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho \"$PROVE_PUSHING\" > '"+seen+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", remote); code != 0 {
			t.Fatalf("prove push: %s", errOut)
		}
		if data, _ := os.ReadFile(seen); strings.TrimSpace(string(data)) != "1" {
			t.Fatalf("the pre-push hook saw PROVE_PUSHING=%q, want 1", data)
		}
	})
}

func TestPlanRunsMissingRecords(t *testing.T) {
	t.Run("AC-905-4 prove plan says run (no record) for a check with no record and accept (signed record) for a valid one", func(t *testing.T) {
		countSetup(t)
		if code, _, errOut := prove(t, "run", "unit"); code != 0 {
			t.Fatalf("prove run unit: %s", errOut)
		}
		code, out, errOut := prove(t, "plan")
		if code != 0 {
			t.Fatalf("prove plan exited %d\n%s%s", code, out, errOut)
		}
		lines := map[string]string{}
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) > 1 {
				lines[f[0]] = strings.Join(f[1:], " ")
			}
		}
		if !strings.HasPrefix(lines["unit"], "accept (signed record)") {
			t.Fatalf("unit: %q, want accept (signed record)\n%s", lines["unit"], out)
		}
		if lines["lint"] != "run (no record)" {
			t.Fatalf("lint: %q, want run (no record)\n%s", lines["lint"], out)
		}
		if !strings.Contains(out, "1 to run, 1 accepted") {
			t.Fatalf("plan lacks its summary:\n%s", out)
		}
	})

	t.Run("a record whose inputs changed is run with the reason, in JSON too", func(t *testing.T) {
		repo, _ := countSetup(t)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		testutil.WriteFile(t, repo, "src/a.go", "package a // changed\n")
		testutil.Commit(t, repo, "change src")
		code, out, errOut := prove(t, "plan", "--format", "json")
		if code != 0 {
			t.Fatalf("prove plan exited %d: %s", code, errOut)
		}
		if strings.Count(out, `"action": "run"`) != 2 || !strings.Contains(out, `"reason": "inputs changed since the record made on`) {
			t.Fatalf("want both checks to run because their inputs changed:\n%s", out)
		}
	})
}
