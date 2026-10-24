// Package run runs a check on a clean tree and stores its signed record.
package run

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
	"github.com/jayehmke/prove/internal/store"
)

// Version is prove's version, recorded in every record.
var Version = "dev"

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

// Run runs the check on HEAD and stores its signed record. A check that
// fails is still recorded; its exit code is in the result. Run refuses a
// dirty working tree, because the record would describe code that isn't
// what ran.
func Run(ctx context.Context, o Options) (*Result, error) {
	if err := CheckClean(o.Repo); err != nil {
		return nil, err
	}
	commit, err := o.Repo.ResolveCommit("HEAD")
	if err != nil {
		return nil, fmt.Errorf("no commit to run on: %v", err)
	}
	tree, err := o.Repo.TreeOf(commit)
	if err != nil {
		return nil, err
	}
	inputs := o.Check.InputsOrDefault()
	fp, err := fingerprint.New(o.Repo).Compute(commit, fingerprint.Spec{
		Command: o.Check.Run, Inputs: inputs, Lockfiles: o.Check.Lockfiles,
	})
	if err != nil {
		return nil, err
	}

	out, err := os.MkdirTemp("", "prove-out-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(out) }()
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	env = append(env[:len(env):len(env)], "PROVE_OUT="+out, "PROVE_CHECK="+o.Check.Name, "PROVE_COMMIT="+commit)

	started := time.Now().UTC()
	exitCode, err := Shell(ctx, o.Repo.Dir, o.Check.Run, env, o.Stdout, o.Stderr)
	if err != nil {
		return nil, err
	}
	finished := time.Now().UTC()

	p := record.Predicate{
		Check:       o.Check.Name,
		Command:     o.Check.Run,
		Inputs:      inputs,
		Lockfiles:   o.Check.Lockfiles,
		Fingerprint: fp,
		ExitCode:    exitCode,
		Result:      record.ResultPass,
		StartedAt:   started,
		FinishedAt:  finished,
		Runner:      record.Runner{ProveVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	if exitCode != 0 {
		p.Result = record.ResultFail
	}
	if o.Check.JUnit != "" {
		tests, err := readJUnit(o.Repo.Dir, o.Check.JUnit, out)
		if err != nil {
			fmt.Fprintf(o.Stderr, "prove: check %s: %v; recording it without per-test results\n", o.Check.Name, err)
		} else {
			p.Tests = tests
			if o.Pattern != nil {
				p.Requirements = junit.Requirements(tests, o.Pattern)
			}
		}
	}

	st := record.New(commit, tree, p)
	envl, err := record.Seal(st, o.Signer)
	if err != nil {
		return nil, err
	}
	entry, err := store.NewEntry(envl)
	if err != nil {
		return nil, err
	}
	if err := o.Store.Put(entry); err != nil {
		return nil, fmt.Errorf("storing the record: %w", err)
	}
	return &Result{Entry: entry, Statement: st, ExitCode: exitCode}, nil
}

// CheckClean returns an ErrDirty error listing the changes when the working
// tree isn't clean.
func CheckClean(repo *gitx.Repo) error {
	dirty, err := repo.Dirty()
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	shown := dirty
	if len(shown) > 10 {
		shown = append(shown[:10:10], fmt.Sprintf("... and %d more", len(dirty)-10))
	}
	return fmt.Errorf("%w; commit or stash them first:\n  %s", ErrDirty, strings.Join(shown, "\n  "))
}

// readJUnit expands $PROVE_OUT in the configured path (a glob) and reads
// every file it matches.
func readJUnit(dir, pattern, out string) ([]record.Test, error) {
	p := os.Expand(pattern, func(k string) string {
		if k == "PROVE_OUT" {
			return out
		}
		return os.Getenv(k)
	})
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	files, err := filepath.Glob(p)
	if err != nil {
		return nil, fmt.Errorf("junit path %q: %v", pattern, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no JUnit file at %s", pattern)
	}
	return junit.ParseFiles(files)
}

// Shell runs command with sh -c in dir and returns its exit code. err is
// set only when the command couldn't be run at all.
func Shell(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code, nil
		}
		return 128, nil // killed by a signal
	}
	if err != nil {
		return 0, fmt.Errorf("running %q: %v", command, err)
	}
	return 0, nil
}
