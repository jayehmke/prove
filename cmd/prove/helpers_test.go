package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

// setup makes a repository whose .prove.yaml is yml (with $TESTER replaced
// by the test key), signs with that key and changes into the repository.
// An empty yml writes no policy file.
func setup(t *testing.T, yml string) (*gitx.Repo, testutil.Key) {
	t.Helper()
	repo := testutil.NewRepo(t)
	key := testutil.NewKey(t, "tester@example.com")
	t.Setenv("PROVE_SSH_KEY", key.Path)
	if yml != "" {
		testutil.WriteFile(t, repo, config.FileName, strings.ReplaceAll(yml, "$TESTER", key.Public))
		testutil.Commit(t, repo, "add policy")
	}
	t.Chdir(repo.Dir)
	return repo, key
}

func prove(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// records returns the statements stored for a commit in the repository's
// configured store.
func records(t *testing.T, repo *gitx.Repo, commit string) []*record.Statement {
	t.Helper()
	ctx := context.Background()
	cfg, err := config.Load(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var sc config.StoreConfig
	if cfg != nil {
		sc = cfg.Store
	}
	st, err := store.Open(ctx, sc, repo)
	if err != nil {
		t.Fatal(err)
	}
	metas, err := st.List(ctx, store.Query{Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	var out []*record.Statement
	for _, m := range metas {
		data, err := st.Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, s, err := record.Open(data)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func storeConfig(t *testing.T, repo *gitx.Repo) config.StoreConfig {
	t.Helper()
	cfg, err := config.Load(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		return config.StoreConfig{}
	}
	return cfg.Store
}
