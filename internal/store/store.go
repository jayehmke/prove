// Package store keeps records: the built-in local (a folder) and git
// (refs under refs/prove/) stores, and external `prove-store-<name>`
// adapters.
package store

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jayehmke/prove/internal/adapter"
	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
)

// ErrNotFound is returned by Get for an unknown record.
var ErrNotFound = errors.New("record not found")

// RefPrefix is where the git store keeps records.
const RefPrefix = "refs/prove/"

// Entry is a stored record with an index of what it is about. The index is
// a lookup hint only: verify trusts nothing but the signed envelope.
type Entry struct {
	ID          string          `json:"id"`
	Commit      string          `json:"commit"`
	Tree        string          `json:"tree"`
	Check       string          `json:"check"`
	Fingerprint string          `json:"fingerprint"`
	KeyID       string          `json:"keyid"`
	Envelope    record.Envelope `json:"envelope"`
}

// Filter selects entries; empty fields match anything.
type Filter struct {
	Commit      string `json:"commit,omitempty"`
	Tree        string `json:"tree,omitempty"`
	Check       string `json:"check,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	KeyID       string `json:"keyid,omitempty"`
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Entry) bool {
	return (f.Commit == "" || f.Commit == e.Commit) &&
		(f.Tree == "" || f.Tree == e.Tree) &&
		(f.Check == "" || f.Check == e.Check) &&
		(f.Fingerprint == "" || f.Fingerprint == e.Fingerprint) &&
		(f.KeyID == "" || f.KeyID == e.KeyID)
}

// Store puts, gets and lists records.
type Store interface {
	// Put stores an entry, replacing one with the same ID.
	Put(Entry) error
	Get(id string) (Entry, error)
	List(Filter) ([]Entry, error)
}

// NewEntry indexes a sealed envelope.
func NewEntry(env record.Envelope) (Entry, error) {
	st, _, err := record.Decode(env)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID:          env.ID(),
		Commit:      st.Commit(),
		Tree:        st.Tree(),
		Check:       st.Predicate.Check,
		Fingerprint: st.Predicate.Fingerprint,
		Envelope:    env,
	}
	if len(env.Signatures) > 0 {
		e.KeyID = env.Signatures[0].KeyID
	}
	return e, nil
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func filterEntries(entries []Entry, f Filter) []Entry {
	var out []Entry
	for _, e := range entries {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// Open opens the store a policy names. Relative local paths are relative to
// the repository root; the default local path is .git/prove/records.
func Open(cfg config.Store, repo *gitx.Repo) (Store, error) {
	switch cfg.Type {
	case "", "git":
		return NewGit(repo), nil
	case "local":
		path := cfg.Path
		if path == "" {
			gitDir, err := repo.GitDir()
			if err != nil {
				return nil, err
			}
			path = filepath.Join(gitDir, "prove", "records")
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(repo.Dir, path)
		}
		return NewLocal(path), nil
	default:
		opts := map[string]any{}
		for k, v := range cfg.Options {
			opts[k] = v
		}
		if cfg.Path != "" {
			opts["path"] = cfg.Path
		}
		return NewExternal(cfg.Type, opts, repo.Dir), nil
	}
}

type external struct {
	name    string
	options map[string]any
	dir     string
}

// NewExternal returns a store backed by the program prove-store-<name>.
func NewExternal(name string, options map[string]any, dir string) Store {
	return &external{name: name, options: options, dir: dir}
}

func (s *external) call(op string, req map[string]any, resp any) error {
	req["options"] = s.options
	return adapter.Call("prove-store-"+s.name, s.dir, op, req, resp)
}

func (s *external) Put(e Entry) error {
	return s.call("put", map[string]any{"entry": e}, nil)
}

func (s *external) Get(id string) (Entry, error) {
	var resp struct {
		Entry *Entry `json:"entry"`
	}
	if err := s.call("get", map[string]any{"id": id}, &resp); err != nil {
		return Entry{}, err
	}
	if resp.Entry == nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return *resp.Entry, nil
}

func (s *external) List(f Filter) ([]Entry, error) {
	var resp struct {
		Entries []Entry `json:"entries"`
	}
	if err := s.call("list", map[string]any{"filter": f}, &resp); err != nil {
		return nil, err
	}
	// The adapter may filter loosely; the filter is applied again here.
	return filterEntries(resp.Entries, f), nil
}
