// Package verify checks a commit against the policy using stored records,
// recomputed fingerprints and, optionally, sampled reruns.
//
// A signature proves who signed a record and that it has not changed since.
// It does not prove the record is true. Verify catches the rest by
// completeness (every policy check needs a record), command match,
// fingerprint recompute, sampled reruns and per-check signer rules.
package verify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/fingerprint"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
)

// Options configure a verification.
type Options struct {
	Repo  *gitx.Repo
	Store store.Store
	Trust *signer.TrustSet
	// Policy is the parsed .prove.yaml. Nil means no policy file: every
	// check recorded for the commit's tree is verified against its own
	// command and inputs.
	Policy *config.Config
	// Commit is the revision to verify. Empty means HEAD.
	Commit string
	// Sample is the percent of passing checks to rerun locally.
	Sample float64
	// Stdout and Stderr receive the output of sampled reruns.
	Stdout io.Writer
	Stderr io.Writer
}

// Status of a check.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusMissing = "missing"
)

// CheckResult is the verdict for one policy check.
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	// Reason says why the check failed.
	Reason string `json:"reason,omitempty"`
	// Signer is the trusted identity that signed the record used.
	Signer string `json:"signer,omitempty"`
	// Reused is true when the record was made on a different tree.
	Reused       bool   `json:"reused"`
	RecordID     string `json:"recordId,omitempty"`
	RecordCommit string `json:"recordCommit,omitempty"`
	Sampled      bool   `json:"sampled,omitempty"`
}

// Requirement is the coverage of one requirement ID.
type Requirement struct {
	ID       string   `json:"id"`
	Required bool     `json:"required"`
	Covered  bool     `json:"covered"`
	Passing  []string `json:"passing,omitempty"`
	Failing  []string `json:"failing,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
}

// Report is the outcome of a verification.
type Report struct {
	Commit       string        `json:"commit"`
	Tree         string        `json:"tree"`
	OK           bool          `json:"ok"`
	Checks       []CheckResult `json:"checks"`
	Requirements []Requirement `json:"requirements,omitempty"`
	// Failures holds one readable reason per failure.
	Failures []string `json:"failures,omitempty"`
}

type verifier struct {
	Options
	commit, tree string
	implicit     bool
	fps          map[string]string // fingerprint cache by spec
}

// Verify checks every policy check of the commit. Its error is set only
// when verification could not run at all; failed checks are in the report.
func Verify(ctx context.Context, o Options) (*Report, error) {
	if o.Commit == "" {
		o.Commit = "HEAD"
	}
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	commit, err := o.Repo.ResolveCommit(ctx, o.Commit)
	if err != nil {
		return nil, err
	}
	tree, err := o.Repo.TreeOf(ctx, commit)
	if err != nil {
		return nil, err
	}
	v := &verifier{Options: o, commit: commit, tree: tree, implicit: o.Policy == nil, fps: map[string]string{}}
	rep := &Report{Commit: commit, Tree: tree, Checks: []CheckResult{}}

	checks, err := v.checks(ctx)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		if v.implicit {
			rep.Failures = append(rep.Failures, fmt.Sprintf("no %s and no records for this tree: run `prove run` first", config.FileName))
		} else {
			rep.Failures = append(rep.Failures, fmt.Sprintf("%s defines no checks", config.FileName))
		}
		return rep, nil
	}

	used := map[string]*record.Statement{}
	for _, c := range checks {
		res, st, err := v.check(ctx, c)
		if err != nil {
			return nil, err
		}
		if res.OK {
			used[c.Name] = st
		}
		rep.Checks = append(rep.Checks, res)
	}

	if err := v.sample(ctx, rep, checks, used); err != nil {
		return nil, err
	}
	for _, c := range rep.Checks {
		if !c.OK {
			rep.Failures = append(rep.Failures, c.Name+": "+c.Reason)
			delete(used, c.Name)
		}
	}
	rep.Requirements = v.requirements(used, rep)
	rep.OK = len(rep.Failures) == 0
	return rep, nil
}

// checks returns the policy checks, or with no policy, one check per name
// recorded for the commit's tree.
func (v *verifier) checks(ctx context.Context) ([]config.Check, error) {
	if !v.implicit {
		return v.Policy.Checks, nil
	}
	metas, err := v.Store.List(ctx, store.Query{Tree: v.tree})
	if err != nil {
		return nil, fmt.Errorf("listing records in %s: %w", v.Store.Name(), err)
	}
	seen := map[string]bool{}
	var checks []config.Check
	for _, m := range metas {
		if !seen[m.Check] {
			seen[m.Check] = true
			checks = append(checks, config.Check{Name: m.Check})
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}

type candidate struct {
	meta      store.Meta
	st        *record.Statement
	identity  string
	reason    string
	authentic bool
}

func (v *verifier) check(ctx context.Context, c config.Check) (CheckResult, *record.Statement, error) {
	res := CheckResult{Name: c.Name}
	metas, err := v.Store.List(ctx, store.Query{Check: c.Name})
	if err != nil {
		return res, nil, fmt.Errorf("listing records in %s: %w", v.Store.Name(), err)
	}
	if len(metas) == 0 {
		res.Status = StatusMissing
		res.Reason = fmt.Sprintf("no record of check %q in the %s", c.Name, v.Store.Name())
		return res, nil, nil
	}
	metas, err = v.relevant(ctx, c, metas)
	if err != nil {
		return res, nil, err
	}
	cands := make([]candidate, 0, len(metas))
	for _, m := range metas {
		cand, err := v.evaluate(ctx, c, m)
		if err != nil {
			return res, nil, err
		}
		cands = append(cands, cand)
	}
	// The newest authentic record decides, preferring this exact tree.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.authentic != b.authentic {
			return a.authentic
		}
		at, bt := a.meta.Tree, b.meta.Tree
		aTime, bTime := a.meta.FinishedAt, b.meta.FinishedAt
		if a.authentic {
			at, bt = a.st.Tree(), b.st.Tree()
			aTime, bTime = a.st.Predicate.FinishedAt, b.st.Predicate.FinishedAt
		}
		if (at == v.tree) != (bt == v.tree) {
			return at == v.tree
		}
		return aTime.After(bTime)
	})
	best := cands[0]
	res.RecordID = best.meta.ID
	if !best.authentic {
		res.Status = StatusFail
		res.Reason = best.reason
		if len(cands) > 1 {
			res.Reason += fmt.Sprintf(" (and %d other unusable records)", len(cands)-1)
		}
		return res, nil, nil
	}
	st := best.st
	res.Signer = best.identity
	res.RecordCommit = st.Commit()
	res.Reused = st.Tree() != v.tree
	if st.Predicate.Result != record.Pass || st.Predicate.ExitCode != 0 {
		res.Status = StatusFail
		res.Reason = fmt.Sprintf("the check failed: exit %d on %s", st.Predicate.ExitCode, short(st.Commit()))
		return res, st, nil
	}
	res.Status = StatusPass
	res.OK = true
	return res, st, nil
}

// fallbackRecords is how many of the newest records verify reads for a
// failure reason when no record's index matches the commit.
const fallbackRecords = 3

// relevant narrows a check's records to the ones that can apply to this
// commit, by the store's index: the same fingerprint or the same tree. The
// index is unsigned, so a store that lies in it can only hide records, never
// make a check pass. With no match it keeps the newest few, so the failure
// can say why they don't apply.
func (v *verifier) relevant(ctx context.Context, c config.Check, metas []store.Meta) ([]store.Meta, error) {
	if v.implicit || len(metas) <= fallbackRecords {
		return metas, nil
	}
	inputs := c.Inputs
	if len(inputs) == 0 {
		inputs = config.DefaultInputs
	}
	fp, err := v.fingerprint(ctx, fingerprint.Spec{Command: c.Run, Inputs: inputs, Lockfiles: c.Lockfiles})
	if err != nil {
		return nil, err
	}
	var out []store.Meta
	for _, m := range metas {
		if m.Fingerprint == fp || m.Tree == v.tree {
			out = append(out, m)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	sort.SliceStable(metas, func(i, j int) bool { return metas[i].FinishedAt.After(metas[j].FinishedAt) })
	return metas[:min(len(metas), fallbackRecords)], nil
}

// evaluate checks one record against the check. A candidate that is not
// authentic carries the reason it can't be used.
func (v *verifier) evaluate(ctx context.Context, c config.Check, m store.Meta) (candidate, error) {
	cand := candidate{meta: m}
	fail := func(format string, args ...any) (candidate, error) {
		cand.reason = fmt.Sprintf(format, args...)
		return cand, nil
	}
	data, err := v.Store.Get(ctx, m.ID)
	if err != nil {
		return fail("record %s could not be read: %v", short(m.ID), err)
	}
	if record.ID(data) != m.ID {
		return fail("signature: record %s does not hash to its id (edited after signing)", short(m.ID))
	}
	env, st, err := record.Open(data)
	if err != nil {
		return fail("signature: record %s: %v", short(m.ID), err)
	}
	identity, err := v.signedBy(ctx, env, st)
	if err != nil {
		return fail("%v", err)
	}
	cand.st, cand.identity = st, identity
	if st.Predicate.Check != c.Name {
		return fail("record %s is of check %q, not %q", short(m.ID), st.Predicate.Check, c.Name)
	}
	if len(c.Signers) > 0 && !contains(c.Signers, identity) {
		return fail("signer %q is not allowed to sign check %q (allowed: %s)", identity, c.Name, strings.Join(c.Signers, ", "))
	}
	spec := fingerprint.Spec{Command: c.Run, Inputs: c.Inputs, Lockfiles: c.Lockfiles}
	if v.implicit {
		spec = fingerprint.Spec{Command: st.Predicate.Command, Inputs: st.Predicate.Inputs, Lockfiles: st.Predicate.Lockfiles}
	} else if st.Predicate.Command != c.Run {
		return fail("command differs: the record ran %q but the policy runs %q", st.Predicate.Command, c.Run)
	}
	if len(spec.Inputs) == 0 {
		spec.Inputs = config.DefaultInputs
	}
	fp, err := v.fingerprint(ctx, spec)
	if err != nil {
		return cand, err
	}
	if fp != st.Predicate.Fingerprint {
		return fail("inputs changed since the record made on %s", short(st.Commit()))
	}
	cand.authentic = true
	return cand, nil
}

// signedBy returns the trusted identity whose signature on the envelope
// verifies.
func (v *verifier) signedBy(ctx context.Context, env *record.Envelope, st *record.Statement) (string, error) {
	payload, err := env.PayloadBytes()
	if err != nil {
		return "", fmt.Errorf("signature: %v", err)
	}
	msg := record.PAE(env.PayloadType, payload)
	var untrusted []string
	var bad error
	for _, s := range env.Signatures {
		sig, err := base64.StdEncoding.Strict().DecodeString(s.Sig)
		if err != nil {
			bad = fmt.Errorf("signature: not base64: %v", err)
			continue
		}
		identity, err := v.Trust.Verify(ctx, s.KeyID, msg, sig)
		if err == nil {
			return identity, nil
		}
		if errors.Is(err, signer.ErrUntrusted) {
			untrusted = append(untrusted, s.KeyID)
		} else if bad == nil {
			bad = fmt.Errorf("signature: %v", err)
		}
	}
	if bad != nil {
		return "", bad
	}
	return "", fmt.Errorf("signed by untrusted signer %q (key %s)", st.Predicate.Signer, strings.Join(untrusted, ", "))
}

func (v *verifier) fingerprint(ctx context.Context, spec fingerprint.Spec) (string, error) {
	key := spec.Command + "\x00" + strings.Join(spec.Inputs, "\x00") + "\x01" + strings.Join(spec.Lockfiles, "\x00")
	if fp, ok := v.fps[key]; ok {
		return fp, nil
	}
	fp, err := fingerprint.Compute(ctx, v.Repo, v.tree, spec)
	if err != nil {
		return "", err
	}
	v.fps[key] = fp
	return fp, nil
}

// sample reruns a share of the passing checks and fails any whose rerun
// disagrees with its record.
func (v *verifier) sample(ctx context.Context, rep *Report, checks []config.Check, used map[string]*record.Statement) error {
	if v.Sample <= 0 {
		return nil
	}
	var passing []int
	for i, r := range rep.Checks {
		if r.OK {
			passing = append(passing, i)
		}
	}
	n := int(math.Ceil(float64(len(passing)) * math.Min(v.Sample, 100) / 100))
	if n == 0 {
		return nil
	}
	rand.Shuffle(len(passing), func(i, j int) { passing[i], passing[j] = passing[j], passing[i] })
	picked := passing[:n]
	sort.Ints(picked)

	dir, cleanup, err := v.checkout(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	for _, i := range picked {
		r := &rep.Checks[i]
		command := checks[i].Run
		if v.implicit {
			command = used[r.Name].Predicate.Command
		}
		fmt.Fprintf(v.Stderr, "prove: sampling %s: %s\n", r.Name, command)
		code, err := runner.Exec(ctx, dir, command, v.Stdout, v.Stderr, []string{"PROVE_CHECK=" + r.Name, "PROVE_SAMPLE=1"})
		if err != nil {
			return err
		}
		r.Sampled = true
		if code != 0 {
			r.OK = false
			r.Status = StatusFail
			r.Reason = fmt.Sprintf("sample rerun disagrees: the record says it passed, but the rerun exited %d", code)
		}
	}
	return nil
}

// checkout returns a directory holding the commit: the working tree when
// it is the commit and clean, otherwise a temporary detached worktree.
func (v *verifier) checkout(ctx context.Context) (string, func(), error) {
	head, err := v.Repo.ResolveCommit(ctx, "HEAD")
	if err == nil && head == v.commit {
		if dirty, err := v.Repo.DirtyPaths(ctx); err == nil && len(dirty) == 0 {
			return v.Repo.Dir, func() {}, nil
		}
	}
	tmp, err := os.MkdirTemp("", "prove-sample-")
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(tmp, "tree")
	if _, err := v.Repo.Git(ctx, "worktree", "add", "--detach", dir, v.commit); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	return dir, func() {
		_, _ = v.Repo.Git(context.Background(), "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(tmp)
	}, nil
}

// requirements reads requirement coverage from the records verify used.
func (v *verifier) requirements(used map[string]*record.Statement, rep *Report) []Requirement {
	var required []string
	var pattern *regexp.Regexp
	if v.Policy != nil && v.Policy.Requirements.Pattern != "" {
		required = v.Policy.Requirements.Required
		pattern = regexp.MustCompile(v.Policy.Requirements.Pattern)
	}
	byID := map[string]*Requirement{}
	var order []string
	get := func(id string) *Requirement {
		if r, ok := byID[id]; ok {
			return r
		}
		byID[id] = &Requirement{ID: id}
		order = append(order, id)
		return byID[id]
	}
	for _, id := range required {
		get(id).Required = true
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, t := range used[name].Predicate.Tests {
			for _, id := range t.Requirements {
				if pattern != nil && !pattern.MatchString(id) {
					continue
				}
				r := get(id)
				label := name + ": " + t.Name
				switch t.Result {
				case "pass":
					r.Covered = true
					r.Passing = append(r.Passing, label)
				case "fail":
					r.Failing = append(r.Failing, label)
				default:
					r.Skipped = append(r.Skipped, label)
				}
			}
		}
	}
	out := make([]Requirement, 0, len(order))
	for _, id := range required {
		out = append(out, *byID[id])
	}
	var extra []string
	for _, id := range order {
		if !byID[id].Required {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		out = append(out, *byID[id])
	}
	for _, r := range out {
		if r.Required && !r.Covered {
			reason := "no test names it"
			if len(r.Failing) > 0 {
				reason = "its tests failed: " + strings.Join(r.Failing, "; ")
			} else if len(r.Skipped) > 0 {
				reason = "its tests were skipped: " + strings.Join(r.Skipped, "; ")
			}
			rep.Failures = append(rep.Failures, fmt.Sprintf("requirement %s has no passing test (%s)", r.ID, reason))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
