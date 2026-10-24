// Package runner runs a check on a clean tree and stores its signed record.
package runner

import (
	"context"
	"errors"
	"io"
	"regexp"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
)

// Options configure one run.
type Options struct {
	Repo   *gitx.Repo
	Store  store.Store
	Signer signer.Signer
	Check  config.Check
	// Requirements finds requirement IDs in test names; nil finds none.
	Requirements *regexp.Regexp
	Stdout       io.Writer
	Stderr       io.Writer
}

// Outcome is a stored record.
type Outcome struct {
	ID        string
	Statement record.Statement
}

// ErrDirty means the working tree has uncommitted changes.
var ErrDirty = errors.New("working tree has uncommitted changes")

// Run runs the check on HEAD and stores its signed record. It refuses a
// dirty working tree.
func Run(ctx context.Context, o Options) (*Outcome, error) {
	return nil, errors.New("not implemented")
}

// Exec runs a shell command in dir and returns its exit code.
func Exec(ctx context.Context, dir, command string, stdout, stderr io.Writer, env []string) (int, error) {
	return -1, errors.New("not implemented")
}
