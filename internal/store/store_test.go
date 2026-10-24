package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

func put(t *testing.T, s store.Store, check, commit, tree, signer string) store.Meta {
	t.Helper()
	data := []byte(`{"record":"` + check + commit + signer + `"}`)
	m := store.Meta{ID: record.ID(data), Check: check, Commit: commit, Tree: tree, Signer: signer,
		Fingerprint: "sha256:" + tree, Result: "pass", FinishedAt: time.Unix(1700000000, 0).UTC()}
	if err := s.Put(context.Background(), m, data); err != nil {
		t.Fatalf("%s put: %v", s.Name(), err)
	}
	return m
}

func ids(ms []store.Meta) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}

func sorted(ss ...string) []string { sort.Strings(ss); return ss }

func refsOutsideProve(t *testing.T, dir string) string {
	t.Helper()
	var keep []string
	for _, line := range strings.Split(testutil.Git(t, dir, "for-each-ref", "--format=%(refname) %(objectname)"), "\n") {
		if line != "" && !strings.HasPrefix(line, "refs/prove/") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n") + "\nHEAD " + testutil.Git(t, dir, "rev-parse", "HEAD") + testutil.Git(t, dir, "symbolic-ref", "HEAD")
}

func TestStoresRoundTrip(t *testing.T) {
	t.Run("AC-807-10 local and git stores put, get and list by commit, tree and signer", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		testutil.Git(t, repo.Dir, "branch", "feature")
		before := refsOutsideProve(t, repo.Dir)

		stores := []store.Store{store.NewLocal(t.TempDir()), store.NewGit(repo)}
		for _, s := range stores {
			a := put(t, s, "unit", "c1", "t1", "alice")
			b := put(t, s, "lint", "c1", "t1", "bob")
			c := put(t, s, "unit", "c2", "t2", "bob")
			// Putting the same record twice is harmless.
			put(t, s, "unit", "c2", "t2", "bob")

			ctx := context.Background()
			for _, m := range []store.Meta{a, b, c} {
				got, err := s.Get(ctx, m.ID)
				if err != nil {
					t.Fatalf("%s get: %v", s.Name(), err)
				}
				if record.ID(got) != m.ID {
					t.Fatalf("%s get returned different bytes for %s", s.Name(), m.ID)
				}
			}
			if _, err := s.Get(ctx, record.ID([]byte("nope"))); err == nil {
				t.Fatalf("%s get of a missing record should fail", s.Name())
			}
			if _, err := s.Get(ctx, "../../etc/passwd"); err == nil {
				t.Fatalf("%s get should reject a malformed id", s.Name())
			}

			for _, tc := range []struct {
				q    store.Query
				want []string
			}{
				{store.Query{}, sorted(a.ID, b.ID, c.ID)},
				{store.Query{Commit: "c1"}, sorted(a.ID, b.ID)},
				{store.Query{Tree: "t2"}, sorted(c.ID)},
				{store.Query{Signer: "bob"}, sorted(b.ID, c.ID)},
				{store.Query{Check: "unit", Signer: "bob"}, sorted(c.ID)},
				{store.Query{Fingerprint: "sha256:t1"}, sorted(a.ID, b.ID)},
				{store.Query{Commit: "c3"}, []string{}},
			} {
				got, err := s.List(ctx, tc.q)
				if err != nil {
					t.Fatalf("%s list: %v", s.Name(), err)
				}
				if !reflect.DeepEqual(ids(got), tc.want) {
					t.Fatalf("%s list %+v = %v, want %v", s.Name(), tc.q, ids(got), tc.want)
				}
			}
			got, _ := s.List(ctx, store.Query{Tree: "t2"})
			if len(got) != 1 || got[0] != c {
				t.Fatalf("%s list returned meta %+v, want %+v", s.Name(), got, c)
			}
		}

		// The git store wrote only under refs/prove/ and left branches,
		// HEAD, the index and the working tree alone.
		if after := refsOutsideProve(t, repo.Dir); after != before {
			t.Fatalf("git store changed refs outside refs/prove/:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		proveRefs := testutil.Git(t, repo.Dir, "for-each-ref", "--format=%(refname)", "refs/prove/")
		if strings.Count(proveRefs, "\n") != 3 {
			t.Fatalf("want 3 refs under refs/prove/, got:\n%s", proveRefs)
		}
		if st := testutil.Git(t, repo.Dir, "status", "--porcelain", "--untracked-files=all"); st != "" {
			t.Fatalf("git store dirtied the working tree:\n%s", st)
		}
	})
}

// writeAdapter puts an executable prove-store-<name> script on PATH.
func writeAdapter(t *testing.T, bin, name, script string) {
	t.Helper()
	path := filepath.Join(bin, "prove-store-"+name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestExternalStoreProtocol(t *testing.T) {
	t.Run("AC-807-11 prove-store-<name> gets put/get/list as JSON on stdin and its answers are used", func(t *testing.T) {
		testutil.Isolate(t)
		bin := t.TempDir()
		log := filepath.Join(t.TempDir(), "requests.log")
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("FAKE_LOG", log)

		stored := []byte(`{"payloadType":"x"}`)
		meta := store.Meta{ID: record.ID(stored), Check: "unit", Commit: "c1", Tree: "t1", Signer: "alice", Result: "pass", FinishedAt: time.Unix(1700000000, 0).UTC()}
		metaJSON, _ := json.Marshal(meta)
		writeAdapter(t, bin, "fake", `req=$(cat)
printf '%s\n' "$req" >> "$FAKE_LOG"
case "$req" in
  *'"op":"put"'*) echo '{"ok":true}' ;;
  *'"op":"get"'*) echo '{"record":"`+base64.StdEncoding.EncodeToString(stored)+`"}' ;;
  *'"op":"list"'*) echo '{"records":[`+string(metaJSON)+`]}' ;;
  *) echo '{"error":"unknown op"}'; exit 1 ;;
esac
`)
		ctx := context.Background()
		s := store.NewExternal("fake", map[string]any{"bucket": "b1"})
		if err := s.Put(ctx, meta, stored); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.Get(ctx, meta.ID)
		if err != nil || string(got) != string(stored) {
			t.Fatalf("get = %q, %v", got, err)
		}
		list, err := s.List(ctx, store.Query{Commit: "c1"})
		if err != nil || len(list) != 1 || list[0] != meta {
			t.Fatalf("list = %+v, %v", list, err)
		}

		raw, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 3 {
			t.Fatalf("want 3 requests, got %d:\n%s", len(lines), raw)
		}
		var reqs []map[string]any
		for _, l := range lines {
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("request is not JSON: %q", l)
			}
			reqs = append(reqs, m)
		}
		if reqs[0]["op"] != "put" || reqs[0]["record"] != base64.StdEncoding.EncodeToString(stored) {
			t.Fatalf("put request = %v", reqs[0])
		}
		if m, _ := reqs[0]["meta"].(map[string]any); m["id"] != meta.ID || m["commit"] != "c1" {
			t.Fatalf("put meta = %v", reqs[0]["meta"])
		}
		if c, _ := reqs[0]["config"].(map[string]any); c["bucket"] != "b1" {
			t.Fatalf("put config = %v", reqs[0]["config"])
		}
		if reqs[1]["op"] != "get" || reqs[1]["id"] != meta.ID {
			t.Fatalf("get request = %v", reqs[1])
		}
		if q, _ := reqs[2]["query"].(map[string]any); reqs[2]["op"] != "list" || q["commit"] != "c1" {
			t.Fatalf("list request = %v", reqs[2])
		}

		for _, tc := range []struct {
			name, script, want string
		}{
			{"garbage", "cat >/dev/null; echo 'this is not json'", "invalid JSON"},
			{"crash", "cat >/dev/null; echo 'disk on fire' >&2; exit 3", "disk on fire"},
			{"refuse", `cat >/dev/null; echo '{"error":"access denied"}'`, "access denied"},
		} {
			writeAdapter(t, bin, tc.name, tc.script)
			err := store.NewExternal(tc.name, nil).Put(ctx, meta, stored)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "prove-store-"+tc.name) {
				t.Fatalf("%s: error %v should name the adapter and say %q", tc.name, err, tc.want)
			}
		}
		if _, err := store.NewExternal("absent", nil).List(ctx, store.Query{}); err == nil || !strings.Contains(err.Error(), "prove-store-absent") {
			t.Fatalf("missing adapter: %v", err)
		}
	})
}
