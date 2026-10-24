package main

import (
	"slices"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
)

func TestZeroConfig(t *testing.T) {
	isolate(t) // no key configured: prove makes one for this machine
	repo := testutil.Repo(t)
	testutil.Write(t, repo, "main.go", "package main\n")
	commit := testutil.Commit(t, repo, "first")

	code, _, stderr := prove(t, repo, "run", "--name", "t", "--", "true")
	if code != 0 {
		t.Fatalf("prove run exited %d: %s", code, stderr)
	}
	sts := records(t, repo, commit)
	if len(sts) != 1 {
		t.Fatalf("got %d records, want 1", len(sts))
	}
	if p := sts[0].Predicate; p.Check != "t" || p.Command != "true" || !slices.Equal(p.Inputs, []string{"**"}) {
		t.Errorf("predicate = %+v, want check t, command true, inputs [**]", p)
	}

	code, stdout, stderr := prove(t, repo, "verify")
	if code != 0 {
		t.Fatalf("prove verify exited %d:\n%s%s", code, stdout, stderr)
	}

	// The whole repository is the input, so any change needs a new run.
	testutil.Write(t, repo, "notes.txt", "x")
	testutil.Commit(t, repo, "second")
	if code, stdout, _ := prove(t, repo, "verify"); code == 0 {
		t.Errorf("verify passed after a change with no new record:\n%s", stdout)
	}
}
