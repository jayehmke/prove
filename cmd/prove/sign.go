package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/signer"
)

// runUnsigned runs checks with no signing key loaded and writes each
// result to dir/<check>.json for prove sign.
func runUnsigned(ctx context.Context, e *env, checks []config.Check, req *regexp.Regexp, dir string, stdout, stderr io.Writer) (int, error) {
	code := exitOK
	var results []*runner.Result
	for _, c := range checks {
		fmt.Fprintf(stderr, "prove: running %s: %s\n", c.Name, c.Run)
		res, err := runner.Execute(ctx, e.repo, c, req, stdout, stderr)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", c.Name, err)
		}
		fmt.Fprintf(stderr, "prove: %s exited %d, not signed\n", c.Name, res.ExitCode)
		if res.ExitCode != 0 {
			code = exitFail
		}
		results = append(results, res)
	}
	// Written once every check has run, so a results folder inside the
	// working tree can't fail a later check's clean-tree test.
	for _, res := range results {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(dir, res.Check+".json"), append(data, '\n'), 0o644); err != nil {
			return 0, err
		}
	}
	fmt.Fprintf(stderr, "prove: wrote %d unsigned result(s) to %s; prove sign signs them\n", len(results), dir)
	return code, nil
}

// cmdSign signs the results in a folder that prove run --unsigned wrote.
// It runs nothing from the repository: a result is untrusted, so
// runner.Sign takes only the run's own facts from it.
func cmdSign(ctx context.Context, args []string, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	rev := fs.String("commit", "", "")
	here := fs.Bool("checkout-policy", false, "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil || len(pos) != 1 {
		return 0, usageError{"sign is: prove sign <dir> [--commit <rev>] [--checkout-policy]"}
	}
	if *here && *rev == "" {
		return 0, usageError{"--checkout-policy goes with --commit: without it, the checkout's policy is used anyway"}
	}
	e, err := load(ctx)
	if err != nil {
		return 0, err
	}
	if *rev != "" {
		at, cleanup, err := e.forCommit(ctx, *rev, "", *here, stderr)
		if err != nil {
			return 0, err
		}
		defer cleanup()
		e = at
	}
	if e.policy == nil {
		return 0, usageError{fmt.Sprintf("no %s here: prove sign takes each check's command from the policy", config.FileName)}
	}
	commit, err := e.repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return 0, gitx.ErrNoCommits
	}
	todo, err := readResults(pos[0], e.policy)
	if err != nil {
		return 0, err
	}
	if len(todo) == 0 {
		fmt.Fprintf(stderr, "prove: no results in %s, nothing to sign\n", pos[0])
		return exitOK, nil
	}
	s, err := signer.FromEnv(ctx, e.repo, stderr)
	if err != nil {
		return 0, err
	}
	code := exitOK
	for _, p := range todo {
		out, err := runner.Sign(ctx, e.repo, e.store, s, p.check, commit, p.res)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", p.path, err)
		}
		if reportSigned(stderr, out, e.store) {
			code = exitFail
		}
	}
	return code, nil
}

// pending is a result to sign and the policy check its file names.
type pending struct {
	path  string
	check config.Check
	res   runner.Result
}

// readResults reads every <check>.json in dir.
func readResults(dir string, policy *config.Config) ([]pending, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var todo []pending
	for _, ent := range entries {
		name, ok := strings.CutSuffix(ent.Name(), ".json")
		if !ok {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		res, err := runner.ParseResult(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		c, ok := policy.Check(name)
		if !ok {
			return nil, fmt.Errorf("%s: no check %q in %s, so nothing says what it ran", path, name, config.FileName)
		}
		todo = append(todo, pending{path: path, check: c, res: res})
	}
	return todo, nil
}
