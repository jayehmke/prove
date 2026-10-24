// Package store keeps records: the built-in local (a folder) and git
// (refs under refs/prove/) stores, and external `prove-store-<name>`
// adapters.
package store

import (
	"errors"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
)

var errTODO = errors.New("not implemented")

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

// Store puts, gets and lists records.
type Store interface {
	Put(Entry) error
	Get(id string) (Entry, error)
	List(Filter) ([]Entry, error)
}

// NewEntry indexes a sealed envelope.
func NewEntry(env record.Envelope) (Entry, error) { return Entry{}, errTODO }

// NewLocal returns a store that keeps records as files in dir.
func NewLocal(dir string) Store { return nil }

// NewGit returns a store that keeps records under refs/prove/ in repo.
func NewGit(repo *gitx.Repo) Store { return nil }

// NewExternal returns a store backed by the program prove-store-<name>.
func NewExternal(name string, options map[string]any, dir string) Store { return nil }

// Open opens the store a policy names.
func Open(cfg config.Store, repo *gitx.Repo) (Store, error) { return nil, errTODO }
