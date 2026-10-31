package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

// sharedDir is where the built-in local store keeps records by default; the
// tests use it as the "other store" that push --to and fetch --from name.
func sharedDir(t *testing.T, wd string) string {
	t.Helper()
	return filepath.Join(strings.TrimSpace(testutil.Git(t, wd, "rev-parse", "--path-format=absolute", "--git-common-dir")), "prove", "records")
}

func dropGitRecords(t *testing.T, wd string) {
	t.Helper()
	for _, ref := range strings.Fields(testutil.Git(t, wd, "for-each-ref", "--format=%(refname)", store.RefPrefix)) {
		testutil.Git(t, wd, "update-ref", "-d", ref)
	}
}

func TestPushToStore(t *testing.T) {
	t.Run("AC-1014-2 prove push --to copies a commit's records into another store and makes no git ref", func(t *testing.T) {
		repo, _ := countSetup(t)
		wd := repo.Dir
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		before := testutil.Git(t, wd, "for-each-ref", "--format=%(refname)")
		code, out, errOut := prove(t, "push", "--to", "local")
		if code != 0 {
			t.Fatalf("prove push --to local exited %d\n%s%s", code, out, errOut)
		}
		if after := testutil.Git(t, wd, "for-each-ref", "--format=%(refname)"); after != before {
			t.Fatalf("push --to changed git refs:\n%s\nwas:\n%s", after, before)
		}
		ids := strings.Fields(testutil.Git(t, wd, "for-each-ref", "--format=%(refname:lstrip=3)", store.RefPrefix))
		for _, id := range ids {
			if _, err := os.Stat(filepath.Join(sharedDir(t, wd), "records", id+".json")); err != nil {
				t.Fatalf("record %s not copied: %v", id, err)
			}
		}
		if code, _, errOut := prove(t, "push", "--to", "local"); code != 0 {
			t.Fatalf("a second push --to exited %d: %s", code, errOut)
		}
		if code, _, _ := prove(t, "push", "origin", "--to", "local"); code == 0 {
			t.Fatal("push with both a remote and --to succeeded")
		}
	})
}

func TestFetchFromStore(t *testing.T) {
	setupShared := func(t *testing.T) string {
		repo, _ := countSetup(t)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run: %s", errOut)
		}
		if code, _, errOut := prove(t, "push", "--to", "local"); code != 0 {
			t.Fatalf("prove push --to local: %s", errOut)
		}
		dropGitRecords(t, repo.Dir)
		if code, _, _ := prove(t, "verify"); code == 0 {
			t.Fatal("verify passed with no records in the git store")
		}
		return repo.Dir
	}

	t.Run("AC-1014-3 prove fetch --from copies the commit's records back and verify accepts them", func(t *testing.T) {
		setupShared(t)
		code, out, errOut := prove(t, "fetch", "--from", "local")
		if code != 0 || !strings.Contains(out, "fetched 2 record(s), refused 0") {
			t.Fatalf("prove fetch exited %d\n%s%s", code, out, errOut)
		}
		if code, out, _ := prove(t, "verify"); code != 0 {
			t.Fatalf("verify after fetch:\n%s", out)
		}
	})

	t.Run("AC-1014-3 fetch refuses a record whose bytes don't hash to its id", func(t *testing.T) {
		wd := setupShared(t)
		files, _ := filepath.Glob(filepath.Join(sharedDir(t, wd), "records", "*.json"))
		if err := os.WriteFile(files[0], []byte(`{"payload":"swapped"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := prove(t, "fetch", "--from", "local")
		if code != 0 || !strings.Contains(out, "fetched 1 record(s), refused 1") || !strings.Contains(errOut, "refusing record") {
			t.Fatalf("prove fetch exited %d\n%s%s", code, out, errOut)
		}
		if code, _, _ := prove(t, "verify"); code == 0 {
			t.Fatal("verify passed with one record refused")
		}
	})

	t.Run("AC-1014-3 an altered record that hashes to its new id is fetched but verify still refuses it", func(t *testing.T) {
		wd := setupShared(t)
		dir := sharedDir(t, wd)
		files, _ := filepath.Glob(filepath.Join(dir, "records", "*.json"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var env map[string]any
			if err := json.Unmarshal(data, &env); err != nil {
				t.Fatal(err)
			}
			sig := env["signatures"].([]any)[0].(map[string]any)
			sig["sig"] = strings.Repeat("A", len(sig["sig"].(string))-4) + "AA=="
			altered, _ := json.Marshal(env)
			if bytes.Equal(altered, data) {
				t.Fatal("record not altered")
			}
			oldID := strings.TrimSuffix(filepath.Base(f), ".json")
			newID := record.ID(altered)
			meta, err := os.ReadFile(filepath.Join(dir, "index", oldID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range []error{
				os.Remove(f),
				os.Remove(filepath.Join(dir, "index", oldID+".json")),
				os.WriteFile(filepath.Join(dir, "records", newID+".json"), altered, 0o644),
				os.WriteFile(filepath.Join(dir, "index", newID+".json"), bytes.Replace(meta, []byte(oldID), []byte(newID), 1), 0o644),
			} {
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if code, out, errOut := prove(t, "fetch", "--from", "local"); code != 0 || !strings.Contains(out, "fetched 2") {
			t.Fatalf("prove fetch exited %d\n%s%s", code, out, errOut)
		}
		if code, out, _ := prove(t, "verify"); code == 0 {
			t.Fatalf("verify accepted records with broken signatures:\n%s", out)
		}
	})
}
