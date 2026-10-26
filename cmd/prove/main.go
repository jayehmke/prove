// Command prove runs checks, signs their results and verifies them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
)

func main() { os.Exit(Main(os.Args[1:], os.Stdout, os.Stderr)) }

// Exit codes.
const (
	exitOK    = 0
	exitFail  = 1 // a check or verification failed
	exitUsage = 2 // bad usage or setup
)

const usage = `prove: run checks, sign their results, verify them without rerunning.

Usage:
  prove run [check...]                   run checks from .prove.yaml (all if none named)
  prove run --name <n> [flags] -- <cmd>  run one command with no config
      --input <glob>      an input file glob (repeatable; default: the whole repo)
      --lockfile <glob>   a lockfile glob (repeatable)
      --junit <path>      JUnit XML report the command writes
  prove verify [commit] [--sample <pct>] [--format text|json]
                                         check every policy check of a commit (default HEAD)
  prove show [commit] [--format md|json] print what is recorded for a commit
  prove version

Signing: PROVE_SSH_KEY (an SSH key file), PROVE_SIGNER (an adapter name),
PROVE_IDENTITY. See README.md.
`

// Main runs prove with args in the current directory and returns the exit
// code: 0 success, 1 a check or verification failed, 2 a usage or setup
// error.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	ctx := context.Background()
	var err error
	code := exitOK
	switch args[0] {
	case "run":
		code, err = cmdRun(ctx, args[1:], stdout, stderr)
	case "verify":
		code, err = cmdVerify(ctx, args[1:], stdout, stderr)
	case "show":
		code, err = cmdShow(ctx, args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "prove", runner.Version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		err = usageError{fmt.Sprintf("unknown command %q", args[0])}
	}
	if err != nil {
		fmt.Fprintln(stderr, "prove:", err)
		var u usageError
		if errors.As(err, &u) {
			fmt.Fprint(stderr, "\n"+usage)
		}
		return exitUsage
	}
	return code
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

// env is what every command loads: the repository, its policy and store.
type env struct {
	repo   *gitx.Repo
	policy *config.Config
	store  store.Store
}

func load(ctx context.Context) (*env, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repo, err := gitx.Open(ctx, wd)
	if err != nil {
		return nil, err
	}
	policy, err := config.Load(repo.Dir)
	if err != nil {
		return nil, err
	}
	var sc config.StoreConfig
	if policy != nil {
		sc = policy.Store
	}
	st, err := store.Open(ctx, sc, repo)
	if err != nil {
		return nil, err
	}
	return &env{repo: repo, policy: policy, store: st}, nil
}

// trust is the policy's signers. A policy without signers (or no policy)
// trusts only this machine's own key, which suits one person on one
// machine; CI should always list its signers.
func (e *env) trust(ctx context.Context, stderr io.Writer) (*signer.TrustSet, error) {
	if e.policy != nil && len(e.policy.Signers) > 0 {
		return signer.NewTrustSet(e.policy.Signers)
	}
	s, err := signer.FromEnv(ctx, e.repo)
	if err != nil {
		return nil, fmt.Errorf("no trusted signers in %s and no local key to trust: %w", config.FileName, err)
	}
	key, err := s.Key()
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stderr, "prove: no signers in %s, so only this machine's key (%s) is trusted\n", config.FileName, s.Identity())
	return signer.NewTrustSet(map[string]config.SignerKey{s.Identity(): key})
}

// parse parses flags that may come before or after positional arguments.
// Everything after "--" is returned separately.
func parse(fs *flag.FlagSet, args []string) (pos, rest []string, err error) {
	fs.SetOutput(io.Discard)
	for i, a := range args {
		if a == "--" {
			rest = args[i+1:]
			if rest == nil {
				rest = []string{}
			}
			args = args[:i]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, rest, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// shellJoin turns argv into one shell command. A single argument is used
// as written, so `-- "go test ./..."` records exactly that string.
func shellJoin(argv []string) string {
	if len(argv) == 1 {
		return argv[0]
	}
	quoted := make([]string, len(argv))
	safe := regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
	for i, a := range argv {
		if safe.MatchString(a) {
			quoted[i] = a
		} else {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

func cmdRun(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("name", "", "")
	junit := fs.String("junit", "", "")
	var inputs, lockfiles multi
	fs.Var(&inputs, "input", "")
	fs.Var(&lockfiles, "lockfile", "")
	names, command, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	e, err := load(ctx)
	if err != nil {
		return 0, err
	}
	// Refuse a dirty tree before anything else, so nothing is run or stored.
	if err := runner.CheckClean(ctx, e.repo); err != nil {
		return 0, err
	}

	var checks []config.Check
	switch {
	case *name != "" || command != nil:
		if *name == "" || len(command) == 0 || len(names) > 0 {
			return 0, usageError{"an ad-hoc run is: prove run --name <n> -- <cmd...>"}
		}
		if !config.ValidName(*name) {
			return 0, usageError{fmt.Sprintf("check name %q must be letters, digits, '.', '_' or '-'", *name)}
		}
		c, ok := e.policy.Check(*name)
		if !ok {
			c = config.Check{Name: *name}
		}
		c.Run = shellJoin(command)
		if len(inputs) > 0 {
			c.Inputs = inputs
		}
		if len(lockfiles) > 0 {
			c.Lockfiles = lockfiles
		}
		if *junit != "" {
			c.JUnit = *junit
		}
		checks = []config.Check{c}
	case e.policy == nil:
		return 0, usageError{fmt.Sprintf("no %s here: name a command with prove run --name <n> -- <cmd...>", config.FileName)}
	case len(names) == 0:
		checks = e.policy.Checks
		if len(checks) == 0 {
			return 0, fmt.Errorf("%s defines no checks", config.FileName)
		}
	default:
		for _, n := range names {
			c, ok := e.policy.Check(n)
			if !ok {
				return 0, fmt.Errorf("no check %q in %s", n, config.FileName)
			}
			checks = append(checks, c)
		}
	}

	s, err := signer.FromEnv(ctx, e.repo)
	if err != nil {
		return 0, err
	}
	var req *regexp.Regexp
	if e.policy != nil && e.policy.Requirements.Pattern != "" {
		req = regexp.MustCompile(e.policy.Requirements.Pattern)
	}
	code := exitOK
	for _, c := range checks {
		fmt.Fprintf(stderr, "prove: running %s: %s\n", c.Name, c.Run)
		out, err := runner.Run(ctx, runner.Options{
			Repo: e.repo, Store: e.store, Signer: s, Check: c, Requirements: req, Stdout: stdout, Stderr: stderr,
		})
		if err != nil {
			return 0, fmt.Errorf("%s: %w", c.Name, err)
		}
		p := out.Statement.Predicate
		fmt.Fprintf(stderr, "prove: %s %s (exit %d), signed by %s, record %s in the %s\n",
			c.Name, p.Result, p.ExitCode, p.Signer, out.ID[:12], e.store.Name())
		if p.ExitCode != 0 {
			code = exitFail
		}
	}
	return code, nil
}
