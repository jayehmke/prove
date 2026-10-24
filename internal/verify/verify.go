// Package verify checks a commit's records against the policy.
//
// A policy check passes when a record for it is signed by a trusted signer
// allowed for that check, ran exactly the policy's command, and has the
// fingerprint the policy's inputs give on this commit (so a record from
// another commit counts when nothing it depends on changed), and that
// record says the command passed. Signatures prove who signed and that
// nothing changed since; sampling (rerunning checks) is what catches a
// trusted signer whose record isn't true.
package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/fingerprint"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/junit"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/run"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
)

// Options configures a verification.
type Options struct {
	Repo   *gitx.Repo
	Commit string
	Policy *config.Config
	Store  store.Store
	Trust  *sign.Trust
	// Sample is the percent of checks to rerun locally.
	Sample float64
	Rand   *rand.Rand
	// Stdout and Stderr get the output of sampled reruns.
	Stdout io.Writer
	Stderr io.Writer
}

// Results of a check.
const (
	ResultPass    = "pass"
	ResultFail    = "fail"
	ResultMissing = "missing"
	ResultInvalid = "invalid"
)

// CheckResult is the verdict on one policy check.
type CheckResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Result   string `json:"result"`
	Reason   string `json:"reason,omitempty"`
	Signer   string `json:"signer,omitempty"`
	Reused   bool   `json:"reused"`
	RecordID string `json:"record,omitempty"`
	// RecordCommit is the commit the record was made on.
	RecordCommit string `json:"recordCommit,omitempty"`
	Sampled      bool   `json:"sampled,omitempty"`

	statement record.Statement
}

// RequirementResult is the verdict on one requirement ID.
type RequirementResult struct {
	ID       string   `json:"id"`
	Met      bool     `json:"met"`
	Required bool     `json:"required"`
	Tests    []string `json:"tests,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// Report is the outcome of a verification.
type Report struct {
	Commit       string              `json:"commit"`
	Tree         string              `json:"tree"`
	OK           bool                `json:"ok"`
	Checks       []CheckResult       `json:"checks"`
	Requirements []RequirementResult `json:"requirements,omitempty"`
	Notes        []string            `json:"notes,omitempty"`
}

// candidate is a stored record evaluated for one check.
type candidate struct {
	entry     store.Entry
	statement record.Statement
	identity  string
	problem   string // empty when the record is usable
	rank      int    // 0 this commit, 1 this tree, 2 another commit
}

// Verify checks every policy check of the commit. Without a policy file
// the checks are the ones recorded on this commit.
func Verify(ctx context.Context, o Options) (*Report, error) {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	tree, err := o.Repo.TreeOf(o.Commit)
	if err != nil {
		return nil, err
	}
	rep := &Report{Commit: o.Commit, Tree: tree}
	checks := o.Policy.Checks
	if !o.Policy.Found {
		if checks, err = recordedChecks(o); err != nil {
			return nil, err
		}
		if len(checks) == 0 {
			rep.Checks = []CheckResult{}
			rep.Notes = append(rep.Notes, fmt.Sprintf("no %s and no records for %s: nothing was proven", config.FileName, short(o.Commit)))
			return rep, nil
		}
	}
	if o.Trust.Empty() {
		rep.Notes = append(rep.Notes, "the policy trusts no signers, so no record can pass")
	}

	hasher := fingerprint.New(o.Repo)
	for _, ch := range checks {
		res, err := verifyCheck(o, hasher, tree, ch)
		if err != nil {
			return nil, err
		}
		rep.Checks = append(rep.Checks, res)
	}

	if o.Sample > 0 {
		if err := sample(ctx, o, rep, checks); err != nil {
			return nil, err
		}
	}

	pattern, err := o.Policy.RequirementPattern()
	if err != nil {
		return nil, err
	}
	rep.Requirements = requirements(rep.Checks, pattern, o.Policy.Requirements.Required)

	rep.OK = len(rep.Checks) > 0
	for _, c := range rep.Checks {
		rep.OK = rep.OK && c.OK
	}
	for _, r := range rep.Requirements {
		rep.OK = rep.OK && (r.Met || !r.Required)
	}
	return rep, nil
}

// recordedChecks builds a policy from the records on this commit, for
// repositories with no policy file. Each check's latest record decides its
// command and inputs.
func recordedChecks(o Options) ([]config.Check, error) {
	entries, err := o.Store.List(store.Filter{Commit: o.Commit})
	if err != nil {
		return nil, err
	}
	type latest struct {
		check config.Check
		at    int64
	}
	byName := map[string]*latest{}
	for _, e := range entries {
		st, _, err := record.Decode(e.Envelope)
		if err != nil {
			// Verified (and reported) as a candidate of its indexed check.
			if _, ok := byName[e.Check]; !ok && e.Check != "" {
				byName[e.Check] = &latest{check: config.Check{Name: e.Check}, at: math.MinInt64}
			}
			continue
		}
		p := st.Predicate
		at := p.FinishedAt.UnixNano()
		if l, ok := byName[p.Check]; !ok || at > l.at {
			byName[p.Check] = &latest{check: config.Check{Name: p.Check, Run: p.Command, Inputs: p.Inputs, Lockfiles: p.Lockfiles}, at: at}
		}
	}
	var checks []config.Check
	for _, l := range byName {
		checks = append(checks, l.check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}

func verifyCheck(o Options, hasher *fingerprint.Hasher, tree string, ch config.Check) (CheckResult, error) {
	res := CheckResult{Name: ch.Name, Result: ResultMissing}
	entries, err := o.Store.List(store.Filter{Check: ch.Name})
	if err != nil {
		return res, err
	}
	if len(entries) == 0 {
		res.Reason = "no record"
		return res, nil
	}
	fp, err := hasher.Compute(o.Commit, fingerprint.Spec{Command: ch.Run, Inputs: ch.InputsOrDefault(), Lockfiles: ch.Lockfiles})
	if err != nil {
		return res, err
	}

	cands := make([]candidate, 0, len(entries))
	for _, e := range entries {
		cands = append(cands, evaluate(o, e, ch, tree, fp))
	}
	// This commit first, then this tree, then other commits; newest first.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		return cands[i].statement.Predicate.FinishedAt.After(cands[j].statement.Predicate.FinishedAt)
	})

	best := -1
	for i, c := range cands {
		if c.problem == "" {
			best = i
			break
		}
	}
	if best < 0 {
		// No usable record: report what's wrong with the most relevant one.
		c := cands[0]
		res.Result = ResultInvalid
		res.Reason = c.problem
		res.Signer = c.identity
		res.RecordID = c.entry.ID
		return res, nil
	}
	c := cands[best]
	p := c.statement.Predicate
	res.Signer = c.identity
	res.RecordID = c.entry.ID
	res.RecordCommit = c.statement.Commit()
	res.Reused = c.rank == 2
	res.statement = c.statement
	if p.Result == record.ResultPass && p.ExitCode == 0 {
		res.OK, res.Result = true, ResultPass
	} else {
		res.Result = ResultFail
		res.Reason = fmt.Sprintf("failed (exit %d)", p.ExitCode)
	}
	if res.Reused {
		where := fmt.Sprintf("reused from %s", short(res.RecordCommit))
		if res.Reason != "" {
			res.Reason += ", " + where
		}
	}
	return res, nil
}

// evaluate decides whether a stored record is usable evidence for ch.
func evaluate(o Options, e store.Entry, ch config.Check, tree, fp string) candidate {
	c := candidate{entry: e, rank: 2}
	env := e.Envelope
	payload, err := env.PayloadBytes()
	if err != nil {
		c.problem = (&sign.SignatureError{Err: err}).Error()
		return c
	}
	if len(env.Signatures) == 0 {
		c.problem = "unsigned record"
		return c
	}
	// Every signature must verify, and at least one must be trusted.
	pae := record.PAE(env.PayloadType, payload)
	var untrusted error
	for _, s := range env.Signatures {
		id, err := o.Trust.Identify(pae, s)
		var sigErr *sign.SignatureError
		switch {
		case errors.As(err, &sigErr):
			c.problem = err.Error()
			return c
		case err != nil:
			untrusted = err
		case c.identity == "":
			c.identity = id
		}
	}
	st, _, err := record.Decode(env)
	if err != nil {
		c.problem = "malformed record: " + err.Error()
		return c
	}
	c.statement = st
	p := st.Predicate
	switch {
	case st.Commit() == o.Commit:
		c.rank = 0
	case st.Tree() == tree:
		c.rank = 1
	}
	switch {
	case c.identity == "":
		c.problem = untrusted.Error()
	case p.Check != ch.Name:
		c.problem = fmt.Sprintf("record is for check %q", p.Check)
	case len(ch.Signers) > 0 && !slices.Contains(ch.Signers, c.identity):
		c.problem = fmt.Sprintf("signer %s is trusted but not allowed to sign this check (allowed: %s)", c.identity, strings.Join(ch.Signers, ", "))
	case p.Command != ch.Run:
		c.problem = fmt.Sprintf("command differs: the record ran %q, the policy runs %q", p.Command, ch.Run)
	case p.Fingerprint != fp && c.rank == 2:
		c.problem = fmt.Sprintf("inputs changed since %s, where the record was made", short(st.Commit()))
	case p.Fingerprint != fp:
		c.problem = "fingerprint differs: the record's inputs or lockfiles aren't the policy's"
	}
	return c
}

// sample reruns a share of the checks that have records and fails any whose
// rerun disagrees with its record.
func sample(ctx context.Context, o Options, rep *Report, checks []config.Check) error {
	var idx []int
	for i, c := range rep.Checks {
		if c.Result == ResultPass || c.Result == ResultFail {
			idx = append(idx, i)
		}
	}
	n := int(math.Ceil(float64(len(idx)) * o.Sample / 100))
	if n == 0 {
		return nil
	}
	r := o.Rand
	if r == nil {
		r = rand.New(rand.NewSource(rand.Int63()))
	}
	r.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	idx = idx[:n]
	sort.Ints(idx)

	dir, err := os.MkdirTemp("", "prove-sample-")
	if err != nil {
		return err
	}
	_ = os.Remove(dir) // git worktree add wants to create it
	if _, err := o.Repo.Run("worktree", "add", "--detach", "--quiet", dir, o.Commit); err != nil {
		return err
	}
	defer func() {
		_, _ = o.Repo.Run("worktree", "remove", "--force", dir)
		_ = os.RemoveAll(dir)
	}()

	byName := map[string]config.Check{}
	for _, ch := range checks {
		byName[ch.Name] = ch
	}
	for _, i := range idx {
		c := &rep.Checks[i]
		ch := byName[c.Name]
		out, err := os.MkdirTemp("", "prove-out-")
		if err != nil {
			return err
		}
		env := append(os.Environ(), "PROVE_OUT="+out, "PROVE_CHECK="+ch.Name, "PROVE_COMMIT="+o.Commit)
		code, err := run.Shell(ctx, dir, ch.Run, env, o.Stdout, o.Stderr)
		_ = os.RemoveAll(out)
		if err != nil {
			return err
		}
		c.Sampled = true
		claimed := c.statement.Predicate.ExitCode
		if (code == 0) != (claimed == 0) {
			c.OK = false
			c.Result = ResultInvalid
			c.Reason = fmt.Sprintf("sample rerun disagrees with the record: the record says exit %d, the rerun exited %d", claimed, code)
		}
	}
	return nil
}

// requirements collects requirement IDs from the tests of the records used,
// with the policy's pattern, and marks the required ones.
func requirements(checks []CheckResult, pattern *regexp.Regexp, required []string) []RequirementResult {
	var tests []record.Test
	for _, c := range checks {
		if c.Result == ResultPass || c.Result == ResultFail {
			tests = append(tests, c.statement.Predicate.Tests...)
		}
	}
	found := map[string]record.Requirement{}
	for _, r := range junit.Requirements(tests, pattern) {
		found[r.ID] = r
	}
	var out []RequirementResult
	seen := map[string]bool{}
	for _, id := range required {
		seen[id] = true
		r := found[id]
		out = append(out, RequirementResult{ID: id, Met: r.Result == record.ResultPass, Required: true, Tests: r.Tests, Reason: unmetReason(r)})
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := found[id]
		out = append(out, RequirementResult{ID: id, Met: r.Result == record.ResultPass, Tests: r.Tests, Reason: unmetReason(r)})
	}
	return out
}

func unmetReason(r record.Requirement) string {
	switch {
	case r.Result == record.ResultPass:
		return ""
	case len(r.Tests) == 0:
		return "no test names it"
	case r.Result == record.ResultFail:
		return "a test fails (tests: " + strings.Join(r.Tests, ", ") + ")"
	default:
		return "no passing test, only skipped ones (tests: " + strings.Join(r.Tests, ", ") + ")"
	}
}

// Failures gives one readable reason for each failure.
func (r *Report) Failures() []string {
	var out []string
	if len(r.Checks) == 0 {
		out = append(out, r.Notes...)
	}
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, fmt.Sprintf("check %s: %s", c.Name, c.Reason))
		}
	}
	for _, q := range r.Requirements {
		if q.Required && !q.Met {
			out = append(out, fmt.Sprintf("requirement %s: %s", q.ID, q.Reason))
		}
	}
	return out
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
