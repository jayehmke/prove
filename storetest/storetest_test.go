package storetest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
)

// The worked example in docs/adapters.md passes the suite.
func TestExampleDirAdapterConforms(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the example adapter needs jq")
	}
	testutil.Isolate(t)
	bin := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "docs", "examples", "prove-store-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "prove-store-dir"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	Run(t, "dir", func(t *testing.T) map[string]any {
		return map[string]any{"dir": t.TempDir()}
	})
}

// memStore is a store with a seeded defect.
type memStore struct {
	// byCheck keys records by check, commit and signer instead of by ID,
	// so a rerun replaces the earlier record.
	byCheck bool
	// dupes lists a record once per put.
	dupes bool
	// corrupt returns different bytes from get.
	corrupt bool
	// lossy forgets the finishedAt of every record.
	lossy bool

	records map[string][]byte
	metas   []store.Meta
}

func (m *memStore) Name() string { return "test store" }

func (m *memStore) key(meta store.Meta) string {
	if m.byCheck {
		return meta.Check + "/" + meta.Commit + "/" + meta.Signer
	}
	return meta.ID
}

func (m *memStore) Put(_ context.Context, meta store.Meta, data []byte) error {
	if m.records == nil {
		m.records = map[string][]byte{}
	}
	k := m.key(meta)
	_, seen := m.records[meta.ID]
	if m.byCheck {
		for id := range m.records {
			if id != meta.ID {
				for i, old := range m.metas {
					if old.ID == id && m.key(old) == k {
						delete(m.records, id)
						m.metas = append(m.metas[:i], m.metas[i+1:]...)
						break
					}
				}
			}
		}
	}
	m.records[meta.ID] = data
	if m.lossy {
		meta.FinishedAt = meta.FinishedAt.Add(1)
	}
	if !seen || m.dupes {
		m.metas = append(m.metas, meta)
	}
	return nil
}

func (m *memStore) Get(_ context.Context, id string) ([]byte, error) {
	data, ok := m.records[id]
	if !ok {
		return nil, fmt.Errorf("no record %s", id)
	}
	if m.corrupt {
		return append([]byte("x"), data...), nil
	}
	return data, nil
}

func (m *memStore) List(_ context.Context, q store.Query) ([]store.Meta, error) {
	var out []store.Meta
	for _, meta := range m.metas {
		if q.Match(meta) {
			out = append(out, meta)
		}
	}
	return out, nil
}

// Each seeded defect fails at least one case, and a sound store fails none.
func TestSuiteCatchesBrokenStores(t *testing.T) {
	failures := func(newStore func() *memStore) []string {
		var failed []string
		for _, c := range cases {
			if err := c.run(context.Background(), newStore()); err != nil {
				failed = append(failed, c.name+": "+err.Error())
			}
		}
		return failed
	}
	if f := failures(func() *memStore { return &memStore{} }); len(f) > 0 {
		t.Fatalf("a sound store failed: %v", f)
	}
	for name, newStore := range map[string]func() *memStore{
		"overwrites a rerun": func() *memStore { return &memStore{byCheck: true} },
		"lists duplicates":   func() *memStore { return &memStore{dupes: true} },
		"corrupts records":   func() *memStore { return &memStore{corrupt: true} },
		"changes meta":       func() *memStore { return &memStore{lossy: true} },
	} {
		if f := failures(newStore); len(f) == 0 {
			t.Errorf("a store that %s passed the suite", name)
		} else {
			t.Logf("a store that %s: %v", name, f)
		}
	}
}
