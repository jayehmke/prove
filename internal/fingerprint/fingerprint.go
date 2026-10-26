// Package fingerprint computes the fingerprint of a check: a sha256 over
// its command, its sorted input paths and their contents, and its lockfiles.
// If the fingerprint is unchanged, the check's result can be reused.
package fingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/glob"
)

// Spec is what a fingerprint covers.
type Spec struct {
	Command   string
	Inputs    []string
	Lockfiles []string
}

// version changes whenever the hashed layout changes.
const version = "prove-fingerprint-v1"

// Compute fingerprints spec against the files of a git tree. It reads the
// committed contents, never the working tree, so it gives the same answer
// for a commit on every machine.
func Compute(ctx context.Context, repo *gitx.Repo, tree string, spec Spec) (string, error) {
	entries, err := repo.ListTree(ctx, tree)
	if err != nil {
		return "", err
	}
	inputs := pick(entries, spec.Inputs)
	locks := pick(entries, spec.Lockfiles)

	h := sha256.New()
	field(h, version)
	field(h, "command", spec.Command)
	if err := files(ctx, repo, h, "input", inputs); err != nil {
		return "", err
	}
	if err := files(ctx, repo, h, "lockfile", locks); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func pick(entries []gitx.Entry, patterns []string) []gitx.Entry {
	var out []gitx.Entry
	for _, e := range entries {
		if glob.MatchAny(patterns, e.Path) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// files hashes each entry's path, mode and the sha256 of its contents. A
// submodule is hashed by the commit it points at.
func files(ctx context.Context, repo *gitx.Repo, h hash.Hash, kind string, entries []gitx.Entry) error {
	field(h, kind+"s", fmt.Sprint(len(entries)))
	var blobs []string
	var idx []int
	sums := make([]string, len(entries))
	for i, e := range entries {
		if e.Type == "blob" {
			blobs = append(blobs, e.OID)
			idx = append(idx, i)
		} else {
			sums[i] = e.Type + ":" + e.OID
		}
	}
	err := repo.ReadBlobs(ctx, blobs, func(i int, data []byte) error {
		if data == nil {
			return fmt.Errorf("%s: git object %s is missing (a shallow or partial clone?)", entries[idx[i]].Path, blobs[i])
		}
		sum := sha256.Sum256(data)
		sums[idx[i]] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return err
	}
	for i, e := range entries {
		field(h, kind, e.Path, e.Mode, sums[i])
	}
	return nil
}

// field writes length-prefixed values so no two inputs hash alike.
func field(h hash.Hash, values ...string) {
	for _, v := range values {
		fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	h.Write([]byte{'\n'})
}
