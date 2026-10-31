package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/testutil/fakes3"
)

// sharePolicy is countPolicy's two checks with a share store; $SHARE is
// replaced by the share store's lines.
const sharePolicy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
    inputs: ["src/**"]
  - name: lint
    run: "true"
    inputs: ["src/**"]
$SHARE
`

// shareSetup makes a repository whose policy shares records in a local
// folder, with src/ committed. It returns the repository and the folder.
func shareSetup(t *testing.T) (*gitx.Repo, string) {
	t.Helper()
	share := filepath.Join(t.TempDir(), "share")
	return shareSetupWith(t, "share:\n  type: local\n  path: "+share+"\n"), share
}

// shareSetupWith is shareSetup with the policy's share lines given.
func shareSetupWith(t *testing.T, shareYAML string) *gitx.Repo {
	t.Helper()
	r, _ := setup(t, strings.ReplaceAll(sharePolicy, "$SHARE", shareYAML))
	testutil.WriteFile(t, r, "src/a.go", "package a\n")
	testutil.Commit(t, r, "src")
	return r
}

// bareRemote makes a bare repository holding dir's HEAD as main, and sets
// it as dir's origin.
func bareRemote(t *testing.T, dir string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, dir, "init", "-q", "--bare", remote)
	testutil.Git(t, dir, "remote", "add", "origin", remote)
	testutil.Git(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	return remote
}

// localIDs returns the record IDs in the repository's git store.
func localIDs(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Fields(testutil.Git(t, dir, "for-each-ref", "--format=%(refname:lstrip=3)", store.RefPrefix))
}

// sharedIDs returns the record IDs in a local share folder.
func sharedIDs(t *testing.T, share string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(share, "records"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids
}

// cloneWithoutRecords clones remote without fetching refs/prove/*, so a
// verify there can only find records in the share store, and changes into
// it.
func cloneWithoutRecords(t *testing.T, remote string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, filepath.Dir(remote), "clone", "-q", remote, clone)
	if refs := testutil.Git(t, clone, "for-each-ref", store.RefPrefix); refs != "" {
		t.Fatalf("the clone has record refs:\n%s", refs)
	}
	t.Chdir(clone)
	return clone
}

func TestPushShare(t *testing.T) {
	t.Run("AC-1014-2 prove push --share copies the commit's records byte-exact into the share store, and a clone with no record refs verifies from it", func(t *testing.T) {
		repo, share := shareSetup(t)
		dir := repo.Dir
		remote := bareRemote(t, dir)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		ids := localIDs(t, dir)
		if len(ids) != 2 {
			t.Fatalf("want 2 local records, got %v", ids)
		}
		if got := sharedIDs(t, share); len(got) != 0 {
			t.Fatalf("prove run wrote to the share store: %v", got)
		}

		code, out, errOut := prove(t, "push", "--share")
		if code != 0 {
			t.Fatalf("prove push --share exited %d\n%s%s", code, out, errOut)
		}
		if got := sharedIDs(t, share); strings.Join(got, " ") != strings.Join(ids, " ") {
			t.Fatalf("share store holds %v, want %v", got, ids)
		}
		for _, id := range ids {
			local := testutil.Git(t, dir, "cat-file", "blob", store.RefPrefix+id+":record.json")
			shared, err := os.ReadFile(filepath.Join(share, "records", id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(shared, []byte(local)) || record.ID(shared) != id {
				t.Fatalf("record %s in the share store differs from the git store's", id)
			}
		}

		cloneWithoutRecords(t, remote)
		code, out, errOut = prove(t, "verify")
		if code != 0 || !strings.Contains(out, "PASS") {
			t.Fatalf("verify in a clone with only the share store exited %d:\n%s%s", code, out, errOut)
		}
	})

	t.Run("AC-1014-5 prove push --share makes no git push: the remote gets no ref, no refs/heads/*-prove-* branch", func(t *testing.T) {
		repo, _ := shareSetup(t)
		dir := repo.Dir
		remote := bareRemote(t, dir)
		before := testutil.Git(t, remote, "for-each-ref", "--format=%(refname)")
		// A pre-push hook that would record any git push prove makes.
		pushed := filepath.Join(t.TempDir(), "pushed")
		hook := filepath.Join(dir, ".git", "hooks", "pre-push")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho pushed >> '"+pushed+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 {
			t.Fatalf("prove push --share: %s", errOut)
		}
		if after := testutil.Git(t, remote, "for-each-ref", "--format=%(refname)"); after != before {
			t.Fatalf("remote refs changed:\n%s\nwant:\n%s", after, before)
		}
		if _, err := os.Stat(pushed); err == nil {
			t.Fatal("prove push --share ran git push")
		}
		for _, args := range [][]string{{"push", "--share", "origin"}, {"push", "--share", "--as-branch", "x"}} {
			if code, _, errOut := prove(t, args...); code != exitUsage || !strings.Contains(errOut, "no remote and no --as-branch") {
				t.Fatalf("prove %v exited %d: %s", args, code, errOut)
			}
		}
	})

	t.Run("AC-1014-4 a second push --share is a no-op and leaves the shared bytes as they were", func(t *testing.T) {
		_, share := shareSetup(t)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 {
			t.Fatalf("prove push --share: %s", errOut)
		}
		ids := sharedIDs(t, share)
		snapshot := map[string][]byte{}
		for _, id := range ids {
			data, err := os.ReadFile(filepath.Join(share, "records", id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			snapshot[id] = data
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 || strings.Contains(errOut, "could not share") {
			t.Fatalf("a second push --share exited %d: %s", code, errOut)
		}
		if got := sharedIDs(t, share); strings.Join(got, " ") != strings.Join(ids, " ") {
			t.Fatalf("share store holds %v after a second push, want %v", got, ids)
		}
		for id, want := range snapshot {
			if got, _ := os.ReadFile(filepath.Join(share, "records", id+".json")); !bytes.Equal(got, want) {
				t.Fatalf("record %s changed", id)
			}
		}
	})

	t.Run("AC-1014-7 verify from the share store fails for a record altered after signing, exactly as from the git store", func(t *testing.T) {
		repo, share := shareSetup(t)
		dir := repo.Dir
		remote := bareRemote(t, dir)
		if code, _, errOut := prove(t, "run", "unit"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 {
			t.Fatalf("prove push --share: %s", errOut)
		}
		ids := sharedIDs(t, share)
		if len(ids) != 1 {
			t.Fatalf("want one shared record, got %v", ids)
		}
		path := filepath.Join(share, "records", ids[0]+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Replace(data, []byte(`"signatures"`), []byte(`"signatures" `), 1), 0o644); err != nil {
			t.Fatal(err)
		}
		cloneWithoutRecords(t, remote)
		code, out, _ := prove(t, "verify")
		if code != exitFail || !strings.Contains(out, "signature") {
			t.Fatalf("verify of an altered shared record exited %d, want 1 with a signature reason:\n%s", code, out)
		}
	})

	t.Run("a record reused on a later commit is shared, so a squash commit with the same inputs verifies from the share store", func(t *testing.T) {
		repo, share := shareSetup(t)
		dir := repo.Dir
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		// A later commit outside the checks' inputs reuses both records,
		// which were never shared.
		testutil.WriteFile(t, repo, "README.md", "changed\n")
		later := testutil.Commit(t, repo, "docs")
		if code, _, errOut := prove(t, "push", "--share", "--commit", later); code != 0 {
			t.Fatalf("prove push --share: %s", errOut)
		}
		if got := sharedIDs(t, share); len(got) != 2 {
			t.Fatalf("want the 2 reused records shared, got %v", got)
		}
		// The squash merge: the same tree on a new commit with another
		// parent, which no record names.
		remote := bareRemote(t, dir)
		testutil.Git(t, dir, "checkout", "-q", "--orphan", "squashed")
		squash := testutil.Commit(t, repo, "squash")
		testutil.Git(t, dir, "push", "-q", "origin", "squashed")
		clone := cloneWithoutRecords(t, remote)
		testutil.Git(t, clone, "checkout", "-q", "squashed")
		code, out, errOut := prove(t, "verify", squash)
		if code != 0 {
			t.Fatalf("verify of the squash commit exited %d:\n%s%s", code, out, errOut)
		}
	})

	t.Run("AC-1014-6 a share store that can't be read: push --share reports it and succeeds, and verify says so once and runs the check", func(t *testing.T) {
		dir := shareSetupWith(t, "share: no-such-store\n").Dir
		remote := bareRemote(t, dir)
		var warn bytes.Buffer
		old := store.Warnings
		store.Warnings = &warn
		t.Cleanup(func() { store.Warnings = old })

		// Run and plan still work on the git store alone.
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		code, _, errOut := prove(t, "push", "--share")
		if code != 0 || !strings.Contains(errOut, "2 of 2 record(s) not shared") {
			t.Fatalf("push --share to a missing store exited %d, want 0 and a report:\n%s", code, errOut)
		}

		warn.Reset()
		cloneWithoutRecords(t, remote)
		code, out, _ := prove(t, "verify")
		if code != exitFail || !strings.Contains(out, "no record") {
			t.Fatalf("verify without the share store exited %d, want 1 with no record:\n%s", code, out)
		}
		if n := strings.Count(warn.String(), "not reading the"); n != 1 {
			t.Fatalf("want one warning about the share store, got %d:\n%s", n, warn.String())
		}
		code, out, _ = prove(t, "plan")
		if code != 0 || strings.Count(out, "run (no record)") != 2 {
			t.Fatalf("plan without the share store should run both checks:\n%s", out)
		}
	})

	t.Run("prove push to a git remote skips records only the share store holds", func(t *testing.T) {
		repo, _ := shareSetup(t)
		dir := repo.Dir
		remote := bareRemote(t, dir)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 {
			t.Fatalf("prove push --share: %s", errOut)
		}
		cloneWithoutRecords(t, remote)
		code, _, errOut := prove(t, "push", "origin")
		if code != 0 || !strings.Contains(errOut, "no records") {
			t.Fatalf("prove push of share-only records exited %d: %s", code, errOut)
		}
		if refs := testutil.Git(t, remote, "for-each-ref", store.RefPrefix); refs != "" {
			t.Fatalf("pushed refs:\n%s", refs)
		}
	})

	t.Run("prove push --share needs a share store", func(t *testing.T) {
		countSetup(t)
		if code, _, errOut := prove(t, "push", "--share"); code != exitUsage || !strings.Contains(errOut, "share store") {
			t.Fatalf("push --share without a share store exited %d: %s", code, errOut)
		}
	})
}

// TestPushShareS3 runs the whole path through the real prove-store-s3
// adapter against an in-process S3.
func TestPushShareS3(t *testing.T) {
	t.Run("AC-1014-2 through prove-store-s3: push --share writes records keyed by check inputs, never overwrites, and a clone verifies from the bucket", func(t *testing.T) {
		bin := t.TempDir()
		if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "prove-store-s3"), "../prove-store-s3").CombinedOutput(); err != nil {
			t.Fatalf("go build prove-store-s3: %v\n%s", err, out)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		home := t.TempDir()
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIDPROVETEST")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
		t.Setenv("AWS_SESSION_TOKEN", "")
		t.Setenv("AWS_REGION", "us-east-1")
		t.Setenv("AWS_PROFILE", "")
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "none"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "none"))
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		fake, endpoint := fakes3.New(t, "records", "AKIDPROVETEST")

		dir := shareSetupWith(t, "share:\n  type: s3\n  options:\n    bucket: records\n    endpoint: "+endpoint+"\n    path_style: true\n").Dir
		remote := bareRemote(t, dir)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		ids := localIDs(t, dir)
		if code, _, errOut := prove(t, "push", "--share"); code != 0 || strings.Contains(errOut, "could not share") {
			t.Fatalf("prove push --share exited %d: %s", code, errOut)
		}
		if code, _, errOut := prove(t, "push", "--share"); code != 0 || strings.Contains(errOut, "could not share") {
			t.Fatalf("a second prove push --share exited %d: %s", code, errOut)
		}
		head := strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "HEAD"))
		var recordKeys []string
		for _, k := range fake.Keys() {
			if strings.Contains(k, head) {
				t.Fatalf("key %s names the commit; records are keyed by check inputs", k)
			}
			if !strings.HasPrefix(k, "prove/index/") {
				recordKeys = append(recordKeys, k)
			}
		}
		if len(recordKeys) != len(ids) || len(fake.Keys()) != 2*len(ids) {
			t.Fatalf("want %d records and their index entries, got %v", len(ids), fake.Keys())
		}
		for _, id := range ids {
			local := testutil.Git(t, dir, "cat-file", "blob", store.RefPrefix+id+":record.json")
			_, st, err := record.Open([]byte(local))
			if err != nil {
				t.Fatal(err)
			}
			key := "prove/" + st.Predicate.Check + "/" + st.Predicate.Fingerprint + "/" + id + ".json"
			if got, ok := fake.Object(key); !ok || !bytes.Equal(got, []byte(local)) {
				t.Fatalf("%s does not hold record %s byte for byte; keys: %v", key, id, fake.Keys())
			}
		}
		if len(fake.Overwrites) > 0 || len(fake.Unconditional) > 0 {
			t.Fatalf("overwrote %v; puts without If-None-Match: %v", fake.Overwrites, fake.Unconditional)
		}

		cloneWithoutRecords(t, remote)
		code, out, errOut := prove(t, "verify")
		if code != 0 || !strings.Contains(out, "PASS") {
			t.Fatalf("verify from the bucket exited %d:\n%s%s", code, out, errOut)
		}
	})
}
