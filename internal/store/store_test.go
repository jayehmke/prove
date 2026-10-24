package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

func entry(t *testing.T, commit, tree, check string, key testutil.Key) store.Entry {
	t.Helper()
	now := time.Now().UTC()
	env, err := record.Seal(record.New(commit, tree, record.Predicate{
		Check: check, Command: "true", Inputs: []string{"**"}, Fingerprint: "sha256:" + check,
		Result: record.ResultPass, StartedAt: now, FinishedAt: now,
	}), key.Signer)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.NewEntry(env)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func ids(entries []store.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.ID)
	}
	slices.Sort(out)
	return out
}

func sorted(s ...string) []string { slices.Sort(s); return s }

func TestStoresRoundTrip(t *testing.T) {
	alice, bob := testutil.NewKey(t), testutil.NewKey(t)
	c1, c2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	t1, t2 := strings.Repeat("a", 40), strings.Repeat("b", 40)
	a := entry(t, c1, t1, "unit", alice)
	b := entry(t, c1, t1, "lint", bob)
	c := entry(t, c2, t2, "unit", alice)

	repo := testutil.Repo(t)
	testutil.Write(t, repo, "a.txt", "x")
	testutil.Commit(t, repo, "first")
	refsBefore := testutil.Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	headBefore := testutil.Git(t, repo, "symbolic-ref", "HEAD")

	stores := map[string]store.Store{
		"local": store.NewLocal(filepath.Join(t.TempDir(), "records")),
		"git":   store.NewGit(repo),
	}
	for name, s := range stores {
		t.Run(name, func(t *testing.T) {
			for _, e := range []store.Entry{a, b, c} {
				if err := s.Put(e); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Get(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != b.ID || got.Commit != c1 || got.Check != "lint" || got.Envelope.Payload != b.Envelope.Payload {
				t.Errorf("Get = %+v", got)
			}
			if _, err := s.Get(strings.Repeat("f", 64)); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("Get(unknown) = %v, want ErrNotFound", err)
			}
			for _, tc := range []struct {
				name   string
				filter store.Filter
				want   []string
			}{
				{"all", store.Filter{}, sorted(a.ID, b.ID, c.ID)},
				{"commit", store.Filter{Commit: c1}, sorted(a.ID, b.ID)},
				{"tree", store.Filter{Tree: t2}, sorted(c.ID)},
				{"signer", store.Filter{KeyID: sign.KeyID(alice.Signer.PublicKey())}, sorted(a.ID, c.ID)},
				{"check", store.Filter{Check: "unit", Commit: c1}, sorted(a.ID)},
			} {
				list, err := s.List(tc.filter)
				if err != nil {
					t.Fatal(err)
				}
				if got := ids(list); !slices.Equal(got, tc.want) {
					t.Errorf("List(%s) = %v, want %v", tc.name, got, tc.want)
				}
			}
		})
	}

	// The git store only adds refs under refs/prove/.
	refsAfter := testutil.Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	var others []string
	for _, l := range strings.Split(refsAfter, "\n") {
		if !strings.HasPrefix(l, store.RefPrefix) {
			others = append(others, l)
		}
	}
	if strings.Join(others, "\n") != refsBefore {
		t.Errorf("non-prove refs changed:\nbefore:\n%s\nafter:\n%s", refsBefore, strings.Join(others, "\n"))
	}
	if n := len(strings.Split(refsAfter, "\n")) - len(others); n != 3 {
		t.Errorf("got %d refs under %s, want 3", n, store.RefPrefix)
	}
	if head := testutil.Git(t, repo, "symbolic-ref", "HEAD"); head != headBefore {
		t.Errorf("HEAD moved to %s", head)
	}
	if status := testutil.Git(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("working tree changed: %s", status)
	}
}

// fakeAdapter writes prove-store-<name> into a directory on PATH. It logs
// each request to $FAKE_LOG and answers with $FAKE_RESPONSE's contents.
func fakeAdapter(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prove-store-"+name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestExternalStoreProtocol(t *testing.T) {
	alice := testutil.NewKey(t)
	c1 := strings.Repeat("1", 40)
	a := entry(t, c1, strings.Repeat("a", 40), "unit", alice)

	dir := fakeAdapter(t, "fake", `cat >> "$FAKE_LOG"; echo >> "$FAKE_LOG"; cat "$FAKE_RESPONSE"`)
	log, resp := filepath.Join(dir, "log"), filepath.Join(dir, "resp")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_RESPONSE", resp)
	respond := func(v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(resp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := store.NewExternal("fake", map[string]any{"bucket": "evidence"}, t.TempDir())
	respond(map[string]any{})
	if err := s.Put(a); err != nil {
		t.Fatal(err)
	}
	respond(map[string]any{"entry": a})
	got, err := s.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID || got.Envelope.Payload != a.Envelope.Payload {
		t.Errorf("Get = %+v", got)
	}
	respond(map[string]any{"entries": []store.Entry{a}})
	list, err := s.List(store.Filter{Commit: c1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("List = %+v", list)
	}
	respond(map[string]any{"error": "no such record"})
	if _, err := s.Get("nope"); err == nil || !strings.Contains(err.Error(), "no such record") {
		t.Errorf("adapter error not passed on: %v", err)
	}

	// What the adapter received: one JSON request per call on stdin.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var reqs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("request is not one line of JSON: %v: %q", err, l)
		}
		reqs = append(reqs, m)
	}
	if len(reqs) != 4 {
		t.Fatalf("got %d requests, want 4: %s", len(reqs), raw)
	}
	ops := []string{reqs[0]["op"].(string), reqs[1]["op"].(string), reqs[2]["op"].(string)}
	if !slices.Equal(ops, []string{"put", "get", "list"}) {
		t.Errorf("ops = %v", ops)
	}
	if e, _ := reqs[0]["entry"].(map[string]any); e["id"] != a.ID {
		t.Errorf("put request = %v", reqs[0])
	}
	if reqs[1]["id"] != a.ID {
		t.Errorf("get request = %v", reqs[1])
	}
	if f, _ := reqs[2]["filter"].(map[string]any); f["commit"] != c1 {
		t.Errorf("list request = %v", reqs[2])
	}
	if o, _ := reqs[0]["options"].(map[string]any); o["bucket"] != "evidence" {
		t.Errorf("options not passed: %v", reqs[0])
	}

	// Broken adapters fail with a message that names them.
	fakeAdapter(t, "crash", `echo "bucket unreachable" >&2; exit 3`)
	if _, err := store.NewExternal("crash", nil, t.TempDir()).List(store.Filter{}); err == nil ||
		!strings.Contains(err.Error(), "prove-store-crash") || !strings.Contains(err.Error(), "bucket unreachable") {
		t.Errorf("crashing adapter: %v", err)
	}
	fakeAdapter(t, "garbage", `cat > /dev/null; echo "this is not json"`)
	if _, err := store.NewExternal("garbage", nil, t.TempDir()).List(store.Filter{}); err == nil ||
		!strings.Contains(err.Error(), "prove-store-garbage") || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("garbage adapter: %v", err)
	}
	if _, err := store.NewExternal("absent", nil, t.TempDir()).List(store.Filter{}); err == nil ||
		!strings.Contains(err.Error(), "prove-store-absent") {
		t.Errorf("missing adapter: %v", err)
	}
}
