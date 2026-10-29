package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/verify"
)

// Plan actions.
const (
	actionAccept = "accept"
	actionRun    = "run"
)

// PlanItem is what a CI job should do about one check: accept its record,
// or run it.
type PlanItem struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	// Reason says why: "signed record" for accept, otherwise why no record
	// can be used ("no record", "inputs changed since ...").
	Reason   string `json:"reason"`
	Signer   string `json:"signer,omitempty"`
	RecordID string `json:"recordId,omitempty"`
	Reused   bool   `json:"reused,omitempty"`
}

// Plan is the plan for one commit.
type Plan struct {
	Commit string     `json:"commit"`
	Tree   string     `json:"tree"`
	Checks []PlanItem `json:"checks"`
}

// Run lists the checks the plan runs.
func (p *Plan) Run() []string {
	var names []string
	for _, c := range p.Checks {
		if c.Action == actionRun {
			names = append(names, c.Name)
		}
	}
	return names
}

// makePlan decides, per policy check, whether commit has a valid, current,
// trusted record (accept) or not (run). It is prove verify without
// sampling, read check by check: a missing record means "run it", not
// "fail".
func makePlan(ctx context.Context, e *env, commit string, stderr io.Writer) (*Plan, error) {
	if e.policy == nil {
		return nil, usageError{fmt.Sprintf("a plan needs a %s that lists the checks", config.FileName)}
	}
	trust, err := e.trust(ctx, stderr)
	if err != nil {
		return nil, err
	}
	rep, err := verify.Verify(ctx, verify.Options{
		Repo: e.repo, Store: e.store, Trust: trust, Policy: e.policy, Commit: commit,
		Stdout: io.Discard, Stderr: stderr,
	})
	if err != nil {
		return nil, err
	}
	if len(rep.Checks) == 0 {
		return nil, fmt.Errorf("%s defines no checks", config.FileName)
	}
	p := &Plan{Commit: rep.Commit, Tree: rep.Tree, Checks: []PlanItem{}}
	for _, c := range rep.Checks {
		item := PlanItem{Name: c.Name, Action: actionRun, Reason: c.Reason}
		switch {
		case c.OK:
			item.Action, item.Reason = actionAccept, "signed record"
			item.Signer, item.RecordID, item.Reused = c.Signer, c.RecordID, c.Reused
		case c.Status == verify.StatusMissing:
			item.Reason = "no record"
		}
		p.Checks = append(p.Checks, item)
	}
	return p, nil
}

func cmdPlan(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	format := fs.String("format", "text", "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil {
		return 0, usageError{"plan takes no command"}
	}
	if len(pos) > 1 {
		return 0, usageError{"give at most one commit"}
	}
	if *format != "text" && *format != "json" {
		return 0, usageError{fmt.Sprintf("unknown format %q: use text or json", *format)}
	}
	commit := "HEAD"
	if len(pos) == 1 {
		commit = pos[0]
	}
	e, err := load(ctx)
	if err != nil {
		return 0, err
	}
	p, err := makePlan(ctx, e, commit, stderr)
	if err != nil {
		return 0, err
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return exitOK, enc.Encode(p)
	}
	fmt.Fprintf(stdout, "prove plan %s (tree %s)\n", short(p.Commit), short(p.Tree))
	width := 0
	for _, c := range p.Checks {
		width = max(width, len(c.Name))
	}
	for _, c := range p.Checks {
		line := fmt.Sprintf("%s (%s)", c.Action, c.Reason)
		if c.Action == actionAccept {
			from := "new"
			if c.Reused {
				from = "reused"
			}
			line += fmt.Sprintf(" by %s, %s, record %s", c.Signer, from, short(c.RecordID))
		}
		fmt.Fprintf(stdout, "  %-*s  %s\n", width, c.Name, line)
	}
	fmt.Fprintf(stdout, "%d to run, %d accepted\n", len(p.Run()), len(p.Checks)-len(p.Run()))
	return exitOK, nil
}

// pushingEnv marks git pushes that prove push makes, so a pre-push hook
// that calls prove push can skip them instead of recursing.
const pushingEnv = "PROVE_PUSHING=1"

func cmdPush(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	rev := fs.String("commit", "HEAD", "")
	prefix := fs.String("as-branch", "", "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil || len(pos) > 1 {
		return 0, usageError{"push is: prove push [remote] [--commit <rev>] [--as-branch <prefix>]"}
	}
	remote := "origin"
	if len(pos) == 1 {
		remote = pos[0]
	}
	e, err := load(ctx)
	if err != nil {
		return 0, err
	}
	if _, ok := e.store.(*store.Git); !ok {
		return 0, fmt.Errorf("prove push shares records kept in the git store; this repository uses the %s", e.store.Name())
	}
	commit, err := e.repo.ResolveCommit(ctx, *rev)
	if err != nil {
		return 0, err
	}
	// Plan with the commit's own policy, as prove run --commit does, so the
	// records the run reused are the ones pushed even when another branch
	// is checked out.
	policy, err := policyAt(ctx, e.repo, commit)
	if err != nil {
		return 0, err
	}
	ids, err := pushIDs(ctx, &env{repo: e.repo, policy: policy, store: e.store}, commit, stderr)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		fmt.Fprintf(stderr, "prove: no records for %s to push\n", short(commit))
		return exitOK, nil
	}
	var refspecs []string
	for _, id := range ids {
		dst := store.RefPrefix + id
		if *prefix != "" {
			dst = "refs/heads/" + *prefix + "-prove-" + id
			if _, err := e.repo.Git(ctx, "check-ref-format", dst); err != nil {
				return 0, usageError{fmt.Sprintf("--as-branch %q does not make a valid branch name", *prefix)}
			}
		}
		refspecs = append(refspecs, store.RefPrefix+id+":"+dst)
	}
	fmt.Fprintf(stderr, "prove: pushing %d record(s) for %s to %s\n", len(ids), short(commit), remote)
	if err := gitPush(ctx, e.repo, remote, refspecs, stdout, stderr); err != nil {
		return 0, err
	}
	return exitOK, nil
}

// pushIDs returns the records a verifier of commit needs: every record
// made for commit, plus the reused records the plan accepts (made on an
// earlier commit with the same inputs, and maybe never pushed).
func pushIDs(ctx context.Context, e *env, commit string, stderr io.Writer) ([]string, error) {
	metas, err := e.store.List(ctx, store.Query{Commit: commit})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, m := range metas {
		add(m.ID)
	}
	if e.policy != nil && len(e.policy.Checks) > 0 {
		p, err := makePlan(ctx, e, commit, stderr)
		if err != nil {
			return nil, err
		}
		for _, c := range p.Checks {
			if c.Action == actionAccept {
				add(c.RecordID)
			}
		}
	}
	return ids, nil
}

// policyAt reads the policy committed in commit, or nil when it has none.
func policyAt(ctx context.Context, repo *gitx.Repo, commit string) (*config.Config, error) {
	path := commit + ":" + config.FileName
	if _, err := repo.Git(ctx, "cat-file", "-e", path); err != nil {
		return nil, nil
	}
	data, err := repo.GitInput(ctx, nil, "cat-file", "blob", path)
	if err != nil {
		return nil, err
	}
	p, err := config.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", config.FileName, short(commit), err)
	}
	return p, nil
}

func gitPush(ctx context.Context, repo *gitx.Repo, remote string, refspecs []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"push", "--quiet", remote}, refspecs...)...)
	cmd.Dir = repo.Dir
	cmd.Env = append(os.Environ(), pushingEnv)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push to %s: %w", remote, err)
	}
	return nil
}
