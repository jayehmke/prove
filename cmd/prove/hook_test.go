package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

// TestExamplePrePushHook runs docs/examples/pre-push with a prove built
// from this checkout.
func TestExamplePrePushHook(t *testing.T) {
	hookSrc, err := filepath.Abs(filepath.Join("..", "..", "docs", "examples", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "prove")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// install makes a repository with policy, the hook and a bare remote.
	install := func(t *testing.T, policy string) (repo *gitx.Repo, remote string, push func(args ...string) (string, error)) {
		t.Helper()
		r, _ := setup(t, policy)
		hook, err := os.ReadFile(hookSrc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.Dir, ".git", "hooks", "pre-push"), hook, 0o755); err != nil {
			t.Fatal(err)
		}
		remote = filepath.Join(t.TempDir(), "remote.git")
		testutil.Git(t, r.Dir, "init", "-q", "--bare", remote)
		return r, remote, func(args ...string) (string, error) {
			cmd := exec.Command("git", append([]string{"push", remote}, args...)...)
			cmd.Dir = r.Dir
			cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
	}

	t.Run("a push lands the pushed commit's records with no other command, and a clone verifies", func(t *testing.T) {
		repo, remote, push := install(t, runPolicy)
		// Uncommitted work does not block the push or get recorded.
		testutil.WriteFile(t, repo, "wip.txt", "untracked\n")
		if out, err := push("HEAD:refs/heads/feature"); err != nil {
			t.Fatalf("git push: %v\n%s", err, out)
		}
		if refs := testutil.Git(t, remote, "for-each-ref", store.RefPrefix); len(strings.Fields(refs)) != 3 {
			t.Fatalf("want one record on the remote, got:\n%s", refs)
		}
		clone := filepath.Join(t.TempDir(), "clone")
		testutil.Git(t, repo.Dir, "clone", "-q", "-b", "feature", remote, clone)
		testutil.Git(t, clone, "fetch", "-q", "origin", "refs/prove/*:refs/prove/*")
		t.Chdir(clone)
		if code, out, _ := prove(t, "verify"); code != 0 {
			t.Fatalf("the pushed commit does not verify from the remote:\n%s", out)
		}
	})

	t.Run("a failing check fails the push", func(t *testing.T) {
		_, remote, push := install(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "false"
`)
		out, err := push("HEAD:refs/heads/feature")
		if err == nil {
			t.Fatalf("a push with a failing check succeeded:\n%s", out)
		}
		if refs := testutil.Git(t, remote, "for-each-ref", "refs/heads/"); refs != "" {
			t.Fatalf("the branch was pushed anyway:\n%s", refs)
		}
	})
}
