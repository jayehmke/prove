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
	return fmt.Errorf("%w (%s): commit them first, because a record describes committed code", ErrDirty, summarize(dirty))
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// summarize lists up to five paths.
func summarize(paths []string) string {
	shown := paths
	if len(shown) > 5 {
		shown = shown[:5]
	}
	s := strings.Join(shown, ", ")
	if len(paths) > len(shown) {
		s += fmt.Sprintf(" and %d more", len(paths)-len(shown))
	}
	return s
}

// Run runs the check on HEAD and stores its signed record. It refuses a
// dirty working tree. A check whose command fails is still recorded, with
// result "fail".
func Run(ctx context.Context, o Options) (*Outcome, error) {
	res, err := Execute(ctx, o.Repo, o.Check, o.Requirements, o.Stdout, o.Stderr)
	if err != nil {
		return nil, err
	}
	return Sign(ctx, o.Repo, o.Store, o.Signer, o.Check, res.Commit, *res)
}

// Result is what a run hands to prove sign. Repo code can rewrite it, so
// prove sign takes only the run's own facts from it and computes the rest
// from git and the policy.
type Result struct {
	Check       string        `json:"check"`
	Commit      string        `json:"commit"`      // consistency: must equal the commit sign signs for
	Fingerprint string        `json:"fingerprint"` // consistency: must equal sign's own computation
	ExitCode    int           `json:"exitCode"`
	StartedAt   time.Time     `json:"startedAt"`
	FinishedAt  time.Time     `json:"finishedAt"`
	Tests       []record.Test `json:"tests,omitempty"`
	JUnitError  string        `json:"junitError,omitempty"`
}

// Execute runs the check on HEAD and returns its result. It refuses a dirty
// working tree, and a check that moves HEAD or changes the tree.
func Execute(ctx context.Context, repo *gitx.Repo, check config.Check, requirements *regexp.Regexp, stdout, stderr io.Writer) (*Result, error) {
	if err := CheckClean(ctx, repo); err != nil {
		return nil, err
	}
	commit, err := repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return nil, gitx.ErrNoCommits
	}
	_, fp, err := fingerprintAt(ctx, repo, commit, check)
	if err != nil {
		return nil, err
	}

	var junitPath string
	if check.JUnit != "" {
		junitPath = filepath.Join(repo.Dir, filepath.FromSlash(check.JUnit))
		if _, err := repo.Git(ctx, "ls-files", "--error-unmatch", "--", check.JUnit); err == nil {
			return nil, fmt.Errorf("check %s: junit path %s is a committed file; point it at an ignored path", check.Name, check.JUnit)
		}
		// A report left by an earlier run must not be read as this run's.
		if err := os.Remove(junitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("removing old JUnit report: %w", err)
		}
	}

	started := time.Now().UTC()
	code, err := Exec(ctx, repo.Dir, check.Run, stdout, stderr, []string{"PROVE_CHECK=" + check.Name})
	if err != nil {
		return nil, err
	}
	finished := time.Now().UTC()
	// The record claims the committed tree passed, so the command must have
	// run on exactly that tree: HEAD unmoved and nothing changed.
	if after, err := repo.ResolveCommit(ctx, "HEAD"); err != nil || after != commit {
		moved := "nowhere"
		if err == nil {
			moved = short(after)
		}
		return nil, fmt.Errorf("the check moved HEAD (%s -> %s), so it did not test the committed code: no record stored. A check must not commit, check out or reset", short(commit), moved)
	}
	if dirty, err := repo.DirtyPaths(ctx); err != nil {
		return nil, err
	} else if len(dirty) > 0 {
		return nil, fmt.Errorf("the check changed the working tree (%s), so it did not test the committed code: no record stored. Make it leave tracked files alone and git-ignore what it writes", summarize(dirty))
	}

	res := &Result{Check: check.Name, Commit: commit, Fingerprint: fp, ExitCode: code, StartedAt: started, FinishedAt: finished}
	if junitPath != "" {
		data, err := os.ReadFile(junitPath)
		if err == nil {
			res.Tests, err = junit.Parse(data, requirements)
		}
		if err != nil {
			res.JUnitError = fmt.Sprintf("reading %s: %v", check.JUnit, err)
			fmt.Fprintf(stderr, "prove: %s: %s\n", check.Name, res.JUnitError)
		}
	}
	return res, nil
}

// Sign stores a signed record of res, a run of check on commit. It runs
// nothing. The record's tree, fingerprint, command, inputs and lockfiles
// come from git and check; only the exit code, times and tests come from
// res. It refuses a result of another check or commit, or one whose
// fingerprint is not check's on commit.
func Sign(ctx context.Context, repo *gitx.Repo, st store.Store, s signer.Signer, check config.Check, commit string, res Result) (*Outcome, error) {
	if res.Check != check.Name {
		return nil, fmt.Errorf("the result is of check %q, not %q", res.Check, check.Name)
	}
	if res.Commit != commit {
		return nil, fmt.Errorf("the result is of commit %s, not %s, the commit being signed", short(res.Commit), short(commit))
	}
	tree, fp, err := fingerprintAt(ctx, repo, commit, check)
	if err != nil {
		return nil, err
	}
	if res.Fingerprint != fp {
		return nil, fmt.Errorf("the result's fingerprint %s is not %s, the check's on %s: the run had another command or inputs", res.Fingerprint, fp, short(commit))
	}

	pred := record.Predicate{
		Check:       check.Name,
		Command:     check.Run,
		Inputs:      inputs(check),
		Lockfiles:   check.Lockfiles,
		Fingerprint: fp,
		ExitCode:    res.ExitCode,
		Result:      record.Pass,
		StartedAt:   res.StartedAt,
		FinishedAt:  res.FinishedAt,
		Signer:      s.Identity(),
		Tests:       res.Tests,
		JUnitError:  res.JUnitError,
		Runner:      record.Runner{Prove: Version, OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	if res.ExitCode != 0 {
		pred.Result = record.Fail
	}
	stmt := record.New(commit, tree, pred)
	data, err := record.Seal(stmt, s)
	if err != nil {
		return nil, err
	}
	id := record.ID(data)
	meta := store.Meta{
		ID: id, Check: check.Name, Commit: commit, Tree: tree, Signer: pred.Signer,
		Fingerprint: fp, Result: pred.Result, FinishedAt: pred.FinishedAt,
	}
	if err := st.Put(ctx, meta, data); err != nil {
		return nil, fmt.Errorf("storing record in %s: %w", st.Name(), err)
	}
	return &Outcome{ID: id, Statement: stmt}, nil
}

// fingerprintAt returns commit's tree and check's fingerprint on it, read
// from git objects only.
func fingerprintAt(ctx context.Context, repo *gitx.Repo, commit string, check config.Check) (tree, fp string, err error) {
	tree, err = repo.TreeOf(ctx, commit)
	if err != nil {
		return "", "", err
	}
	fp, err = fingerprint.Compute(ctx, repo, tree, fingerprint.Spec{Command: check.Run, Inputs: inputs(check), Lockfiles: check.Lockfiles})
	return tree, fp, err
}

func inputs(c config.Check) []string {
	if len(c.Inputs) == 0 {
		return config.DefaultInputs
	}
	return c.Inputs
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
