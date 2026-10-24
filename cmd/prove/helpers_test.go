package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// isolate keeps prove from reading the developer's keys and config.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("PROVE_SIGNING_KEY", "")
	t.Setenv("PROVE_SIGNER", "")
}

// prove runs the CLI in repo and returns the exit code, stdout and stderr.
func prove(t *testing.T, repo *gitx.Repo, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(args, Env{Dir: repo.Dir, Stdout: &stdout, Stderr: &stderr, Getenv: os.Getenv})
	return code, stdout.String(), stderr.String()
}

// records returns the decoded records the git store holds for commit.
func records(t *testing.T, repo *gitx.Repo, commit string) []record.Statement {
	t.Helper()
	entries, err := store.NewGit(repo).List(store.Filter{Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	var sts []record.Statement
	for _, e := range entries {
		st, _, err := record.Decode(e.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		sts = append(sts, st)
	}
	return sts
}

func policy(lines ...string) string { return strings.Join(lines, "\n") + "\n" }
