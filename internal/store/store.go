// Package store keeps signed records. The local and git stores are built
// in; any other store is an external prove-store-<name> adapter.
package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// Match reports whether m is selected by q.
func (q Query) Match(m Meta) bool {
	return (q.Check == "" || q.Check == m.Check) &&
		(q.Commit == "" || q.Commit == m.Commit) &&
		(q.Tree == "" || q.Tree == m.Tree) &&
		(q.Signer == "" || q.Signer == m.Signer) &&
		(q.Fingerprint == "" || q.Fingerprint == m.Fingerprint)
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

// Warnings receives notes about index entries a store skipped.
var Warnings io.Writer = os.Stderr

// skip notes an unreadable index entry. The index is only a lookup hint,
// so a bad entry hides that record instead of failing every check.
func skip(store, entry string, err error) {
	fmt.Fprintf(Warnings, "prove: %s: skipping unreadable index entry %s: %v\n", store, entry, err)
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func checkID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("bad record id %q: want 64 lowercase hex digits", id)
	}
	return nil
}

// ForPolicy returns the store a policy reads and writes: its store, read
// together with its share store when it names one. A nil policy uses the
// git store.
func ForPolicy(ctx context.Context, policy *config.Config, repo *gitx.Repo) (Store, error) {
	if policy == nil {
		return NewGit(repo), nil
	}
	own, err := Open(ctx, policy.Store, repo)
	if err != nil || policy.Share.Type == "" {
		return own, err
	}
	share, err := Open(ctx, policy.Share, repo)
	if err != nil {
		return nil, err
	}
	return NewShared(own, share), nil
}

// Open returns the store a policy names. The default is the git store.
func Open(ctx context.Context, cfg config.StoreConfig, repo *gitx.Repo) (Store, error) {
	switch cfg.Type {
	case "", "git":
		return NewGit(repo), nil
	case "local":
		dir := cfg.Path
		if dir == "" {
			common, err := repo.CommonDir(ctx)
			if err != nil {
				return nil, err
			}
			dir = filepath.Join(common, "prove", "records")
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(repo.Dir, dir)
		}
		return NewLocal(dir), nil
	default:
		return NewExternal(cfg.Type, cfg.Options), nil
	}
}
