package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

func TestRunRecordsTreeAndFingerprint(t *testing.T) {
	isolate(t)
	key := testutil.NewKey(t)
	t.Setenv("PROVE_SIGNING_KEY", key.Path)
	repo := testutil.Repo(t)
	testutil.Write(t, repo, ".prove.yaml", policy(
		"checks:",
		"  - name: unit",
		"    run: test -f src/a.txt",
		"    inputs: [\"src/**\"]",
		"signers:",
		"  - identity: alice",
		"    key: "+key.Public,
	))
	testutil.Write(t, repo, "src/a.txt", "one")
	testutil.Write(t, repo, "README", "hello")

	runAt := func(commit string) record.Statement {
		t.Helper()
		code, _, stderr := prove(t, repo, "run", "unit")
		if code != 0 {
			t.Fatalf("prove run exited %d: %s", code, stderr)
		}
		entries, err := store.NewGit(repo).List(store.Filter{Commit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("got %d records for %s, want 1", len(entries), commit)
		}
		env := entries[0].Envelope
		st, payload, err := record.Decode(env)
		if err != nil {
			t.Fatal(err)
		}
		// Signed with the configured key.
		sig, err := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := sign.VerifySSH(record.PAE(env.PayloadType, payload), sig)
		if err != nil {
			t.Fatalf("record signature: %v", err)
		}
		if sign.KeyID(pub) != sign.KeyID(key.Signer.PublicKey()) {
			t.Errorf("signed by %s, want %s", sign.KeyID(pub), sign.KeyID(key.Signer.PublicKey()))
		}
		if tree := testutil.Git(t, repo, "rev-parse", commit+"^{tree}"); st.Tree() != tree {
			t.Errorf("subject tree = %s, want HEAD's tree %s", st.Tree(), tree)
		}
		if st.Commit() != commit || st.Predicate.Result != record.ResultPass || st.Predicate.Check != "unit" {
			t.Errorf("predicate = %+v", st.Predicate)
		}
		if !strings.HasPrefix(st.Predicate.Fingerprint, "sha256:") {
			t.Errorf("fingerprint = %q", st.Predicate.Fingerprint)
		}
		return st
	}

	first := runAt(testutil.Commit(t, repo, "first"))

	testutil.Write(t, repo, "README", "an undeclared file changed")
	undeclared := runAt(testutil.Commit(t, repo, "readme"))
	if undeclared.Tree() == first.Tree() {
		t.Fatal("trees should differ")
	}
	if undeclared.Predicate.Fingerprint != first.Predicate.Fingerprint {
		t.Errorf("fingerprint changed when an undeclared file changed: %s -> %s", first.Predicate.Fingerprint, undeclared.Predicate.Fingerprint)
	}

	testutil.Write(t, repo, "src/a.txt", "two")
	declared := runAt(testutil.Commit(t, repo, "input"))
	if declared.Predicate.Fingerprint == first.Predicate.Fingerprint {
		t.Error("fingerprint did not change when a declared input changed")
	}
}

func TestRunRefusesDirtyTree(t *testing.T) {
	isolate(t)
	key := testutil.NewKey(t)
	t.Setenv("PROVE_SIGNING_KEY", key.Path)
	repo := testutil.Repo(t)
	testutil.Write(t, repo, ".prove.yaml", policy(
		"checks:",
		"  - name: unit",
		"    run: \"true\"",
	))
	testutil.Write(t, repo, "a.txt", "one")
	commit := testutil.Commit(t, repo, "first")

	for name, dirty := range map[string]func(){
		"modified":  func() { testutil.Write(t, repo, "a.txt", "edited") },
		"untracked": func() { testutil.Write(t, repo, "new.txt", "new") },
	} {
		t.Run(name, func(t *testing.T) {
			testutil.Git(t, repo, "checkout", "-q", "--", ".")
			testutil.Git(t, repo, "clean", "-qfd")
			dirty()
			code, _, stderr := prove(t, repo, "run", "unit")
			if code == 0 {
				t.Fatal("prove run succeeded on a dirty tree")
			}
			if !strings.Contains(stderr, "uncommitted") {
				t.Errorf("stderr doesn't say why: %q", stderr)
			}
			if got := records(t, repo, commit); len(got) != 0 {
				t.Errorf("stored %d records on a dirty tree", len(got))
			}
			if refs := testutil.Git(t, repo, "for-each-ref", store.RefPrefix); refs != "" {
				t.Errorf("refs written: %s", refs)
			}
		})
	}
}
