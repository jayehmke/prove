// Package run runs a check on a clean tree and stores its signed record.
package run

import (
	"context"
	"errors"
	"io"
	"regexp"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// ErrDirty means the working tree has uncommitted changes.
var ErrDirty = errors.New("working tree has uncommitted changes")

// Options configures a run.
type Options struct {
	Repo    *gitx.Repo
	Check   config.Check
	Pattern *regexp.Regexp
	Signer  record.Signer
	Store   store.Store
	Stdout  io.Writer
	Stderr  io.Writer
	// Env is the command's environment; nil means prove's own.
	Env []string
}

// Result is a stored run.
type Result struct {
	Entry     store.Entry
	Statement record.Statement
	ExitCode  int
}

// Run runs the check and stores its record.
func Run(ctx context.Context, o Options) (*Result, error) { return nil, errors.New("not implemented") }
