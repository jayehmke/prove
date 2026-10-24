// Package store keeps signed records. The local and git stores are built
// in; any other store is an external prove-store-<name> adapter.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
)

// Meta indexes a record. It is a lookup hint: verify trusts only the
// signed record itself.
type Meta struct {
	ID          string    `json:"id"`
	Check       string    `json:"check"`
	Commit      string    `json:"commit"`
	Tree        string    `json:"tree"`
	Signer      string    `json:"signer"`
	Fingerprint string    `json:"fingerprint"`
	Result      string    `json:"result"`
	FinishedAt  time.Time `json:"finishedAt"`
}

// Query selects records. Empty fields match everything.
type Query struct {
	Check       string `json:"check,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Tree        string `json:"tree,omitempty"`
	Signer      string `json:"signer,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Store keeps records.
type Store interface {
	// Name describes the store for messages.
	Name() string
	// Put stores record bytes under meta.ID.
	Put(ctx context.Context, meta Meta, record []byte) error
	// Get returns the record bytes stored under id.
	Get(ctx context.Context, id string) ([]byte, error)
	// List returns the metadata of the records matching q.
	List(ctx context.Context, q Query) ([]Meta, error)
}

var errNotImplemented = errors.New("not implemented")

// NewLocal returns the store that keeps records in a folder.
func NewLocal(dir string) Store { return stub{} }

// NewGit returns the store that keeps records under refs/prove/.
func NewGit(repo *gitx.Repo) Store { return stub{} }

// NewExternal returns the prove-store-<name> adapter store.
func NewExternal(name string, options map[string]any) Store { return stub{} }

// Open returns the store a policy names. A nil cfg means the default.
func Open(ctx context.Context, cfg config.StoreConfig, repo *gitx.Repo) (Store, error) {
	return nil, errNotImplemented
}

type stub struct{}

func (stub) Name() string                                { return "stub" }
func (stub) Put(context.Context, Meta, []byte) error     { return errNotImplemented }
func (stub) Get(context.Context, string) ([]byte, error) { return nil, errNotImplemented }
func (stub) List(context.Context, Query) ([]Meta, error) { return nil, errNotImplemented }
