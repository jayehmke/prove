// Package runner runs a check on a clean tree and stores its signed record.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/fingerprint"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/junit"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
)

// Version is the prove version written into records.
var Version = "dev"

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

// CheckClean returns ErrDirty, naming a few paths, when the working tree
// has uncommitted or untracked changes.
func CheckClean(ctx context.Context, repo *gitx.Repo) error {
	dirty, err := repo.DirtyPaths(ctx)
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	shown := dirty
	if len(shown) > 5 {
		shown = shown[:5]
	}
	more := ""
	if len(dirty) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(dirty)-len(shown))
	}
	return fmt.Errorf("%w (%s%s): commit them first, because a record describes committed code", ErrDirty, strings.Join(shown, ", "), more)
}

// Run runs the check on HEAD and stores its signed record. It refuses a
// dirty working tree. A check whose command fails is still recorded, with
// result "fail".
func Run(ctx context.Context, o Options) (*Outcome, error) {
	if err := CheckClean(ctx, o.Repo); err != nil {
		return nil, err
	}
	commit, err := o.Repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return nil, gitx.ErrNoCommits
	}
	tree, err := o.Repo.TreeOf(ctx, commit)
	if err != nil {
		return nil, err
	}
	check := o.Check
	if len(check.Inputs) == 0 {
		check.Inputs = config.DefaultInputs
	}
	fp, err := fingerprint.Compute(ctx, o.Repo, tree, fingerprint.Spec{Command: check.Run, Inputs: check.Inputs, Lockfiles: check.Lockfiles})
	if err != nil {
		return nil, err
	}

	var junitPath string
	if check.JUnit != "" {
		junitPath = filepath.Join(o.Repo.Dir, filepath.FromSlash(check.JUnit))
		if _, err := o.Repo.Git(ctx, "ls-files", "--error-unmatch", "--", check.JUnit); err == nil {
			return nil, fmt.Errorf("check %s: junit path %s is a committed file; point it at an ignored path", check.Name, check.JUnit)
		}
		// A report left by an earlier run must not be read as this run's.
		if err := os.Remove(junitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("removing old JUnit report: %w", err)
		}
	}

	started := time.Now().UTC()
	code, err := Exec(ctx, o.Repo.Dir, check.Run, o.Stdout, o.Stderr, []string{"PROVE_CHECK=" + check.Name})
	if err != nil {
		return nil, err
	}
	finished := time.Now().UTC()

	pred := record.Predicate{
		Check:       check.Name,
		Command:     check.Run,
		Inputs:      check.Inputs,
		Lockfiles:   check.Lockfiles,
		Fingerprint: fp,
		ExitCode:    code,
		Result:      record.Pass,
		StartedAt:   started,
		FinishedAt:  finished,
		Signer:      o.Signer.Identity(),
		Runner:      record.Runner{Prove: Version, OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	if code != 0 {
		pred.Result = record.Fail
	}
	if junitPath != "" {
		data, err := os.ReadFile(junitPath)
		if err == nil {
			pred.Tests, err = junit.Parse(data, o.Requirements)
		}
		if err != nil {
			pred.JUnitError = fmt.Sprintf("reading %s: %v", check.JUnit, err)
			fmt.Fprintf(o.Stderr, "prove: %s: %s\n", check.Name, pred.JUnitError)
		}
	}

	st := record.New(commit, tree, pred)
	data, err := record.Seal(st, o.Signer)
	if err != nil {
		return nil, err
	}
	id := record.ID(data)
	meta := store.Meta{
		ID: id, Check: check.Name, Commit: commit, Tree: tree, Signer: pred.Signer,
		Fingerprint: fp, Result: pred.Result, FinishedAt: finished,
	}
	if err := o.Store.Put(ctx, meta, data); err != nil {
		return nil, fmt.Errorf("storing record in %s: %w", o.Store.Name(), err)
	}
	return &Outcome{ID: id, Statement: st}, nil
}

// Exec runs a shell command in dir and returns its exit code. A command
// killed by a signal returns -1. The error is set only when the command
// could not be started.
func Exec(ctx context.Context, dir, command string, stdout, stderr io.Writer, env []string) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), env...)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, fmt.Errorf("running %q: %w", command, err)
	}
	return 0, nil
}
