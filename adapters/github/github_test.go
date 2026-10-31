package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/storetest"
)

// TestMain runs this test binary as the adapter when it is started under
// the name prove-store-github, so the protocol tests need no separate build.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == Program {
		os.Exit(Main(context.Background(), os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// fakeGitHub serves the commit-comment endpoints of one repository, two
// comments per page so pagination is exercised.
type fakeGitHub struct {
	mu       sync.Mutex
	comments map[string][]string // commit -> bodies, oldest first
	order    []string            // "commit\x00body", every comment oldest first
	posts    int
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, string) {
	f := &fakeGitHub{comments: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeGitHub) add(commit, body string) {
	f.comments[commit] = append(f.comments[commit], body)
	f.order = append(f.order, commit+"\x00"+body)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	const prefix = "/repos/acme/app/"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	var bodies []string
	switch {
	case path == "comments" && r.Method == http.MethodGet:
		for _, o := range f.order {
			bodies = append(bodies, strings.SplitN(o, "\x00", 2)[1])
		}
	case strings.HasPrefix(path, "commits/") && strings.HasSuffix(path, "/comments"):
		commit := strings.TrimSuffix(strings.TrimPrefix(path, "commits/"), "/comments")
		if r.Method == http.MethodPost {
			var in struct{ Body string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.add(commit, in.Body)
			f.posts++
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":1}`)
			return
		}
		bodies = f.comments[commit]
	default:
		http.NotFound(w, r)
		return
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &page); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	start, end := (page-1)*2, page*2
	if start > len(bodies) {
		start = len(bodies)
	}
	if end >= len(bodies) {
		end = len(bodies)
	} else {
		q := r.URL.Query()
		q.Set("page", fmt.Sprint(page+1))
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?%s>; rel="next"`, r.Host, r.URL.Path, q.Encode()))
	}
	out := []map[string]string{}
	for _, b := range bodies[start:end] {
		out = append(out, map[string]string{"body": b})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func githubEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_REPOSITORY", "")
}

func sample(check, commit, body string) (store.Meta, []byte) {
	data := []byte(`{"payloadType":"application/vnd.in-toto+json","payload":"` + body + `"}`)
	return store.Meta{ID: record.ID(data), Check: check, Commit: commit, Tree: "t-" + commit, Signer: "ci@example.com",
		Fingerprint: "sha256:" + body, Result: "pass", FinishedAt: time.Unix(1700000000, 0).UTC()}, data
}

func TestGitHubSpeaksStoreProtocol(t *testing.T) {
	t.Run("AC-1014-1 prove-store-github passes the store-protocol conformance suite against a fake GitHub API", func(t *testing.T) {
		githubEnv(t)
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		if err := os.Symlink(self, filepath.Join(bin, Program)); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		storetest.Run(t, "github", func(t *testing.T) map[string]any {
			_, api := newFakeGitHub(t)
			return map[string]any{"repo": "acme/app", "api": api}
		})
	})
}

func TestGitHubComments(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, opts map[string]any) (*fakeGitHub, *Store) {
		githubEnv(t)
		f, api := newFakeGitHub(t)
		o := map[string]any{"repo": "acme/app", "api": api}
		for k, v := range opts {
			o[k] = v
		}
		s, err := New(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		return f, s
	}

	t.Run("AC-1014-1 put comments each record on its own commit once, and a second put posts nothing", func(t *testing.T) {
		f, s := open(t, nil)
		a, aData := sample("unit", "c0ffee1", "a")
		for i := 0; i < 2; i++ {
			if err := s.Put(ctx, a, aData); err != nil {
				t.Fatal(err)
			}
		}
		if f.posts != 1 || len(f.comments["c0ffee1"]) != 1 {
			t.Fatalf("posts=%d comments=%v, want one comment on c0ffee1", f.posts, f.comments)
		}
		if !strings.HasPrefix(f.comments["c0ffee1"][0], Marker) {
			t.Fatalf("comment doesn't open with the marker:\n%s", f.comments["c0ffee1"][0])
		}
		got, err := s.Get(ctx, a.ID)
		if err != nil || !bytes.Equal(got, aData) {
			t.Fatalf("get = %q, %v", got, err)
		}
	})

	t.Run("a comment whose bytes don't hash to its id, or that isn't a record, is skipped", func(t *testing.T) {
		f, s := open(t, map[string]any{"commit": "c0ffee1"})
		a, aData := sample("unit", "c0ffee1", "a")
		body, err := Body(a, aData)
		if err != nil {
			t.Fatal(err)
		}
		forged := strings.Replace(body, `"record":"`, `"record":"QQ`, 1)
		f.add("c0ffee1", forged)
		f.add("c0ffee1", "LGTM")
		f.add("c0ffee1", Marker+"\n```prove-record\nnot json\n```")
		metas, err := s.List(ctx, store.Query{Commit: "c0ffee1"})
		if err != nil || len(metas) != 0 {
			t.Fatalf("list = %v, %v; want nothing", metas, err)
		}
		if _, err := s.Get(ctx, a.ID); err == nil {
			t.Fatal("get returned a record whose bytes don't hash to its id")
		}
		if err := s.Put(ctx, a, aData); err != nil || f.posts != 1 {
			t.Fatalf("put after a forged copy: posts=%d, %v; want the real record posted", f.posts, err)
		}
		if got, err := s.Get(ctx, a.ID); err != nil || !bytes.Equal(got, aData) {
			t.Fatalf("get = %q, %v", got, err)
		}
	})

	t.Run("put refuses a meta id that isn't the record's hash", func(t *testing.T) {
		f, s := open(t, nil)
		a, _ := sample("unit", "c0ffee1", "a")
		if err := s.Put(ctx, a, []byte("other bytes")); err == nil || f.posts != 0 {
			t.Fatalf("put of mismatched bytes: posts=%d, %v", f.posts, err)
		}
	})

	t.Run("the repository comes from the origin remote when no option or env names it", func(t *testing.T) {
		githubEnv(t)
		dir := t.TempDir()
		for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme/app.git"}} {
			cmd := execGit(dir, args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		t.Chdir(dir)
		s, err := New(ctx, map[string]any{"api": "http://unused"})
		if err != nil || s.repo != "acme/app" {
			t.Fatalf("repo = %q, %v", s.repo, err)
		}
	})
}

func execGit(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd
}
