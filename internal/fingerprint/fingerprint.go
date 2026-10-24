// Package fingerprint computes a check's fingerprint: a sha256 over its
// command, its sorted input paths and their contents, and its lockfiles.
// Contents come from the git tree, so the fingerprint of any commit can be
// computed without checking it out.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"slices"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/glob"
)

// Spec is what a fingerprint covers.
type Spec struct {
	Command   string
	Inputs    []string
	Lockfiles []string
}

// Hasher computes fingerprints from git trees, caching tree listings and
// file content hashes.
type Hasher struct {
	repo  *gitx.Repo
	trees map[string][]gitx.Entry
	sums  map[string]string // object hash -> sha256 of its content
}

// New returns a Hasher for repo.
func New(repo *gitx.Repo) *Hasher {
	return &Hasher{repo: repo, trees: map[string][]gitx.Entry{}, sums: map[string]string{}}
}

// Compute returns the fingerprint of spec on the tree of rev, as
// "sha256:<hex>".
func (h *Hasher) Compute(rev string, s Spec) (string, error) {
	entries, ok := h.trees[rev]
	if !ok {
		var err error
		if entries, err = h.repo.ListTree(rev); err != nil {
			return "", err
		}
		h.trees[rev] = entries
	}
	inputs := s.Inputs
	if len(inputs) == 0 {
		inputs = []string{"**"}
	}
	byPath := map[string]gitx.Entry{}
	var selected []gitx.Entry
	for _, e := range entries {
		byPath[e.Path] = e
		if glob.Selected(inputs, e.Path) {
			selected = append(selected, e)
		}
	}
	lockfiles := slices.Clone(s.Lockfiles)
	slices.Sort(lockfiles)
	lockfiles = slices.Compact(lockfiles)

	var need []string
	for _, e := range selected {
		need = append(need, e.Hash)
	}
	for _, l := range lockfiles {
		if e, ok := byPath[l]; ok {
			need = append(need, e.Hash)
		}
	}
	if err := h.hashBlobs(need); err != nil {
		return "", err
	}

	// ls-tree lists paths sorted, which is the order we hash in.
	d := sha256.New()
	field(d, "prove-fingerprint/v1")
	field(d, "command", s.Command)
	for _, e := range selected {
		field(d, "input", e.Path, e.Mode, h.sums[e.Hash])
	}
	for _, l := range lockfiles {
		if e, ok := byPath[l]; ok {
			field(d, "lockfile", l, e.Mode, h.sums[e.Hash])
		} else {
			field(d, "lockfile", l, "missing")
		}
	}
	return "sha256:" + hex.EncodeToString(d.Sum(nil)), nil
}

// field writes length-prefixed values, so no two field lists hash alike.
func field(w io.Writer, values ...string) {
	for _, v := range values {
		fmt.Fprintf(w, "%d:%s,", len(v), v)
	}
	io.WriteString(w, "\n")
}

func (h *Hasher) hashBlobs(hashes []string) error {
	var todo []string
	seen := map[string]bool{}
	for _, x := range hashes {
		if _, ok := h.sums[x]; !ok && !seen[x] {
			seen[x] = true
			todo = append(todo, x)
		}
	}
	return h.repo.ReadObjects(todo, func(rev string, content []byte, err error) error {
		if err != nil {
			// A submodule's commit isn't in this repository; its hash is
			// its identity.
			h.sums[rev] = "object:" + rev
			return nil
		}
		h.sums[rev] = sum(sha256.New(), content)
		return nil
	})
}

func sum(d hash.Hash, b []byte) string {
	d.Write(b)
	return hex.EncodeToString(d.Sum(nil))
}
