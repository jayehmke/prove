// Package fingerprint computes a check's fingerprint: a sha256 over its
// command, its sorted input paths and their contents, and its lockfiles.
package fingerprint

import (
	"errors"

	"github.com/jayehmke/prove/internal/gitx"
)

// Spec is what a fingerprint covers.
type Spec struct {
	Command   string
	Inputs    []string
	Lockfiles []string
}

// Hasher computes fingerprints from git trees, caching file hashes.
type Hasher struct {
	repo *gitx.Repo
}

// New returns a Hasher for repo.
func New(repo *gitx.Repo) *Hasher { return &Hasher{repo: repo} }

// Compute returns the fingerprint of spec on the tree of rev.
func (h *Hasher) Compute(rev string, s Spec) (string, error) {
	return "", errors.New("not implemented")
}
