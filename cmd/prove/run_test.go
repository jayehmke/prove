package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

const runPolicy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
    inputs: ["src/**"]
`

func TestRunRecordsTreeAndFingerprint(t *testing.T) {
	t.Run("AC-807-2 prove run stores a signed record for HEAD's tree; the fingerprint follows declared inputs only", func(t *testing.T) {
		repo, key := setup(t, runPolicy)
		testutil.WriteFile(t, repo, "src/a.go", "package a\n")
		first := testutil.Commit(t, repo, "src")

		fingerprintAt := func(commit string) string {
			t.Helper()
			if code, out, errOut := prove(t, "run", "unit"); code != 0 {
				t.Fatalf("prove run exited %d\n%s%s", code, out, errOut)
			}
			recs := records(t, repo, commit)
			if len(recs) != 1 {
				t.Fatalf("want 1 record for %s, got %d", commit, len(recs))
			}
			r := recs[0]
			if r.Tree() != testutil.Tree(t, repo, commit) || r.Commit() != commit {
				t.Fatalf("record is about tree %s commit %s, want HEAD %s", r.Tree(), r.Commit(), commit)
			}
			if r.Predicate.Check != "unit" || r.Predicate.Result != record.Pass || r.Predicate.Command != "true" {
				t.Fatalf("predicate = %+v", r.Predicate)
			}
			return r.Predicate.Fingerprint
		}

		fp1 := fingerprintAt(first)

		// The stored record is signed by the key.
		st, _ := store.Open(context.Background(), storeConfig(t, repo), repo)
		metas, _ := st.List(context.Background(), store.Query{Commit: first})
		data, _ := st.Get(context.Background(), metas[0].ID)
		var env record.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		payload, _ := base64.StdEncoding.DecodeString(env.Payload)
		sig, _ := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
		trust := testutil.Trust(t, map[string]testutil.Key{"tester": key})
		if _, err := trust.Verify(context.Background(), env.Signatures[0].KeyID, record.PAE(env.PayloadType, payload), sig); err != nil {
			t.Fatalf("stored record signature: %v", err)
		}

		testutil.WriteFile(t, repo, "docs/notes.md", "undeclared\n")
		second := testutil.Commit(t, repo, "docs")
		if fp2 := fingerprintAt(second); fp2 != fp1 {
			t.Fatalf("an undeclared file changed the fingerprint: %s -> %s", fp1, fp2)
		}

		testutil.WriteFile(t, repo, "src/a.go", "package a // changed\n")
		third := testutil.Commit(t, repo, "change src")
		if fp3 := fingerprintAt(third); fp3 == fp1 {
			t.Fatalf("a declared input changed but the fingerprint did not: %s", fp3)
		}
	})
}

func TestRunRefusesDirtyTree(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"modified tracked file", "README.md"},
		{"untracked file", "new.txt"},
	} {
		t.Run("AC-807-3 prove run refuses a dirty tree: "+tc.name, func(t *testing.T) {
			repo, _ := setup(t, runPolicy)
			head := testutil.Head(t, repo)
			testutil.WriteFile(t, repo, tc.path, "uncommitted\n")
			code, _, errOut := prove(t, "run", "unit")
			if code == 0 {
				t.Fatal("prove run on a dirty tree exited 0")
			}
			if !strings.Contains(errOut, "uncommitted") || !strings.Contains(errOut, tc.path) {
				t.Fatalf("stderr should say the tree has uncommitted changes in %s:\n%s", tc.path, errOut)
			}
			if recs := records(t, repo, head); len(recs) != 0 {
				t.Fatalf("a refused run stored %d records", len(recs))
			}
			if code, _, _ := prove(t, "run", "--name", "adhoc", "--", "true"); code == 0 {
				t.Fatal("ad-hoc prove run on a dirty tree exited 0")
			}
			if recs := records(t, repo, head); len(recs) != 0 {
				t.Fatalf("a refused ad-hoc run stored %d records", len(recs))
			}
		})
	}
}
