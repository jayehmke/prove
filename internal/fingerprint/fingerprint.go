// Package fingerprint computes the fingerprint of a check: a sha256 over
// its command, its sorted input paths and their contents, and its lockfiles.
// If the fingerprint is unchanged, the check's result can be reused.
package fingerprint

import (
	"context"
	"errors"

	"github.com/jayehmke/prove/internal/gitx"
)

// Spec is what a fingerprint covers.
type Spec struct {
	Command   string
	Inputs    []string
	Lockfiles []string
}

// Compute fingerprints spec against the files of a git tree.
func Compute(ctx context.Context, repo *gitx.Repo, tree string, spec Spec) (string, error) {
	return "", errors.New("not implemented")
}
