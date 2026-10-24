package verify_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/fingerprint"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/verify"
)

type env struct {
	t      *testing.T
	repo   *gitx.Repo
	store  store.Store
	key    testutil.Key
	policy *config.Config
	trust  *signer.TrustSet
}

// newEnv makes a repository, a local store, a trusted key "tester" and the
// policy in yml (which may reference $TESTER for the key).
func newEnv(t *testing.T, yml string) *env {
	t.Helper()
	repo := testutil.NewRepo(t)
	key := testutil.NewKey(t, "tester@example.com")
	return newEnvWith(t, repo, key, yml)
}

func newEnvWith(t *testing.T, repo *gitx.Repo, key testutil.Key, yml string) *env {
	t.Helper()
	policy, err := config.Parse([]byte(strings.ReplaceAll(yml, "$TESTER", key.Public)))
	if err != nil {
		t.Fatal(err)
	}
	trust, err := signer.NewTrustSet(policy.Signers)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, repo: repo, store: store.NewLocal(t.TempDir()), key: key, policy: policy, trust: trust}
}

func (e *env) check(name string) config.Check {
	e.t.Helper()
	c, ok := e.policy.Check(name)
	if !ok {
		e.t.Fatalf("no check %q", name)
	}
	return c
}

func (e *env) run(c config.Check, s signer.Signer) *runner.Outcome {
	e.t.Helper()
	out, err := runner.Run(context.Background(), runner.Options{
		Repo: e.repo, Store: e.store, Signer: s, Check: c, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		e.t.Fatalf("run %s: %v", c.Name, err)
	}
	return out
}

func (e *env) verify(commit string, sample float64) *verify.Report {
	e.t.Helper()
	return e.verifyWith(e.store, commit, sample)
}

func (e *env) verifyWith(st store.Store, commit string, sample float64) *verify.Report {
	e.t.Helper()
	rep, err := verify.Verify(context.Background(), verify.Options{
		Repo: e.repo, Store: st, Trust: e.trust, Policy: e.policy, Commit: commit,
		Sample: sample, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		e.t.Fatalf("verify: %v", err)
	}
	return rep
}

func result(t *testing.T, rep *verify.Report, name string) verify.CheckResult {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("report has no check %q: %+v", name, rep.Checks)
	return verify.CheckResult{}
}

const onePolicy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
`

// memStore holds records in memory so a test can hand verify any bytes.
type memStore struct {
	metas   []store.Meta
	records map[string][]byte
}

func (m *memStore) Name() string { return "mem" }
func (m *memStore) Put(_ context.Context, meta store.Meta, b []byte) error {
	if m.records == nil {
		m.records = map[string][]byte{}
	}
	m.metas = append(m.metas, meta)
	m.records[meta.ID] = b
	return nil
}
func (m *memStore) Get(_ context.Context, id string) ([]byte, error) { return m.records[id], nil }
func (m *memStore) List(_ context.Context, q store.Query) ([]store.Meta, error) {
	var out []store.Meta
	for _, meta := range m.metas {
		if (q.Check == "" || q.Check == meta.Check) && (q.Tree == "" || q.Tree == meta.Tree) &&
			(q.Commit == "" || q.Commit == meta.Commit) && (q.Signer == "" || q.Signer == meta.Signer) &&
			(q.Fingerprint == "" || q.Fingerprint == meta.Fingerprint) {
			out = append(out, meta)
		}
	}
	return out, nil
}

func TestVerifyRejectsTamperedRecord(t *testing.T) {
	t.Run("AC-807-4 changing any byte of a stored record fails with a signature reason", func(t *testing.T) {
		e := newEnv(t, onePolicy)
		head := testutil.Head(t, e.repo)
		out := e.run(e.check("unit"), e.key.Signer)
		metas, err := e.store.List(context.Background(), store.Query{Check: "unit"})
		if err != nil || len(metas) != 1 {
			t.Fatalf("list: %v %v", metas, err)
		}
		orig, err := e.store.Get(context.Background(), out.ID)
		if err != nil {
			t.Fatal(err)
		}

		// The untouched record verifies.
		good := &memStore{}
		_ = good.Put(context.Background(), metas[0], orig)
		if rep := e.verifyWith(good, head, 0); !rep.OK {
			t.Fatalf("untouched record should verify: %+v", rep)
		}

		for i := range orig {
			for _, flip := range []byte{0x01, 0x20} {
				bad := append([]byte(nil), orig...)
				bad[i] ^= flip
				meta := metas[0]
				// An attacker who can write the store can also re-key the record.
				meta.ID = record.ID(bad)
				st := &memStore{}
				_ = st.Put(context.Background(), meta, bad)
				rep := e.verifyWith(st, head, 0)
				r := result(t, rep, "unit")
				if rep.OK || r.OK {
					t.Fatalf("byte %d ^ %#x (%q): verify passed a tampered record", i, flip, orig[i])
				}
				if !strings.Contains(r.Reason, "signature") {
					t.Fatalf("byte %d ^ %#x: reason %q is not a signature reason", i, flip, r.Reason)
				}
			}
		}
	})
}

func TestVerifyRejectsUntrustedSigner(t *testing.T) {
	t.Run("AC-807-5 a key that is not trusted fails and names the signer", func(t *testing.T) {
		e := newEnv(t, onePolicy)
		mallory := testutil.NewKey(t, "mallory@example.com")
		e.run(e.check("unit"), mallory.Signer)
		rep := e.verify(testutil.Head(t, e.repo), 0)
		r := result(t, rep, "unit")
		if rep.OK || r.OK {
			t.Fatal("verify accepted a record signed by an untrusted key")
		}
		if !strings.Contains(r.Reason, "mallory@example.com") || !strings.Contains(r.Reason, "untrusted") {
			t.Fatalf("reason %q should say the signer mallory@example.com is untrusted", r.Reason)
		}
	})

	t.Run("AC-807-5 a trusted signer not allowed for the check fails and names the signer", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		tester := testutil.NewKey(t, "tester@example.com")
		ci := testutil.NewKey(t, "ci@example.com")
		e := newEnvWith(t, repo, tester, `
signers:
  tester: $TESTER
  ci: `+ci.Public+`
checks:
  - name: unit
    run: "true"
    signers: [ci]
`)
		e.run(e.check("unit"), tester.Signer)
		rep := e.verify(testutil.Head(t, repo), 0)
		r := result(t, rep, "unit")
		if rep.OK || r.OK {
			t.Fatal("verify accepted a record from a signer the check does not allow")
		}
		if !strings.Contains(r.Reason, `"tester"`) || !strings.Contains(r.Reason, "not allowed") {
			t.Fatalf("reason %q should say signer \"tester\" is not allowed", r.Reason)
		}

		// The allowed signer passes.
		e.run(e.check("unit"), ci.Signer)
		if rep := e.verify(testutil.Head(t, repo), 0); !rep.OK {
			t.Fatalf("record from the allowed signer should verify: %+v", rep)
		}
	})
}

func TestVerifyRejectsDifferentCommand(t *testing.T) {
	t.Run("AC-807-6 a record of a different command fails verify", func(t *testing.T) {
		e := newEnv(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true && true"
`)
		subset := e.check("unit")
		subset.Run = "true"
		e.run(subset, e.key.Signer)
		rep := e.verify(testutil.Head(t, e.repo), 0)
		r := result(t, rep, "unit")
		if rep.OK || r.OK {
			t.Fatal("verify accepted a record of a different command")
		}
		if !strings.Contains(r.Reason, "command") || !strings.Contains(r.Reason, `"true"`) {
			t.Fatalf("reason %q should name the command mismatch", r.Reason)
		}
	})
}

func TestVerifyReusesUnchangedInputs(t *testing.T) {
	t.Run("AC-807-7 a record from another commit verifies while its inputs are unchanged", func(t *testing.T) {
		e := newEnv(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
    inputs: ["src/**"]
`)
		testutil.WriteFile(t, e.repo, "src/a.go", "package a\n")
		first := testutil.Commit(t, e.repo, "add src")
		e.run(e.check("unit"), e.key.Signer)

		testutil.WriteFile(t, e.repo, "docs/readme.md", "docs\n")
		second := testutil.Commit(t, e.repo, "docs only")
		rep := e.verify(second, 0)
		r := result(t, rep, "unit")
		if !rep.OK || !r.OK {
			t.Fatalf("record from %s should be reused on %s: %+v", first[:7], second[:7], rep)
		}
		if !r.Reused || r.RecordCommit != first {
			t.Fatalf("result should be reused from %s: %+v", first, r)
		}

		testutil.WriteFile(t, e.repo, "src/a.go", "package a // changed\n")
		third := testutil.Commit(t, e.repo, "change src")
		rep = e.verify(third, 0)
		r = result(t, rep, "unit")
		if rep.OK || r.OK {
			t.Fatal("verify reused a record after a declared input changed")
		}
		if !strings.Contains(r.Reason, "inputs changed") {
			t.Fatalf("reason %q should say inputs changed", r.Reason)
		}
	})
}

func TestVerifyNamesMissingCheck(t *testing.T) {
	t.Run("AC-807-8 a check with no record fails and is named; all checks pass means exit 0", func(t *testing.T) {
		e := newEnv(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
  - name: lint
    run: "true"
`)
		head := testutil.Head(t, e.repo)
		e.run(e.check("unit"), e.key.Signer)
		rep := e.verify(head, 0)
		if rep.OK {
			t.Fatal("verify passed with a check missing")
		}
		lint := result(t, rep, "lint")
		if lint.OK || lint.Status != verify.StatusMissing || !strings.Contains(lint.Reason, "lint") {
			t.Fatalf("lint should be missing with a reason naming it: %+v", lint)
		}
		if !result(t, rep, "unit").OK {
			t.Fatalf("unit should pass: %+v", rep)
		}
		if len(rep.Failures) != 1 || !strings.Contains(rep.Failures[0], "lint") {
			t.Fatalf("one failure naming lint expected: %q", rep.Failures)
		}

		e.run(e.check("lint"), e.key.Signer)
		if rep := e.verify(head, 0); !rep.OK || len(rep.Failures) != 0 {
			t.Fatalf("every check has a record, verify should pass: %+v", rep)
		}
	})

	t.Run("AC-807-8 a failing run is not a usable record", func(t *testing.T) {
		e := newEnv(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "exit 3"
`)
		e.run(e.check("unit"), e.key.Signer)
		rep := e.verify(testutil.Head(t, e.repo), 0)
		r := result(t, rep, "unit")
		if rep.OK || r.OK || r.Status != verify.StatusFail || !strings.Contains(r.Reason, "exit 3") {
			t.Fatalf("a failed run should fail verify with its exit code: %+v", r)
		}
	})
}

func TestSampleRerunCatchesFalsePass(t *testing.T) {
	t.Run("AC-807-13 verify --sample 100 reruns checks and catches a false pass", func(t *testing.T) {
		e := newEnv(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "exit 1"
  - name: lint
    run: "true"
`)
		ctx := context.Background()
		head := testutil.Head(t, e.repo)
		tree := testutil.Tree(t, e.repo, head)
		e.run(e.check("lint"), e.key.Signer)

		// A trusted signer claims "exit 1" passed.
		unit := e.check("unit")
		fp, err := fingerprint.Compute(ctx, e.repo, tree, fingerprint.Spec{Command: unit.Run, Inputs: unit.Inputs})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		st := record.New(head, tree, record.Predicate{
			Check: "unit", Command: unit.Run, Inputs: unit.Inputs, Fingerprint: fp,
			ExitCode: 0, Result: record.Pass, StartedAt: now, FinishedAt: now, Signer: "tester@example.com",
		})
		data, err := record.Seal(st, e.key.Signer)
		if err != nil {
			t.Fatal(err)
		}
		meta := store.Meta{ID: record.ID(data), Check: "unit", Commit: head, Tree: tree, Signer: "tester@example.com", Fingerprint: fp, Result: record.Pass, FinishedAt: now}
		if err := e.store.Put(ctx, meta, data); err != nil {
			t.Fatal(err)
		}

		if rep := e.verify(head, 0); !rep.OK {
			t.Fatalf("without sampling the forged record looks valid; got %+v", rep)
		}
		rep := e.verify(head, 100)
		r := result(t, rep, "unit")
		if rep.OK || r.OK || !r.Sampled {
			t.Fatalf("sampling should catch the false pass: %+v", rep)
		}
		if !strings.Contains(r.Reason, "rerun") {
			t.Fatalf("reason %q should say the rerun disagreed", r.Reason)
		}
		if l := result(t, rep, "lint"); !l.OK || !l.Sampled {
			t.Fatalf("lint reruns and agrees: %+v", l)
		}
		found := false
		for _, f := range rep.Failures {
			found = found || strings.Contains(f, "unit")
		}
		if !found {
			t.Fatalf("failures should name unit: %q", rep.Failures)
		}
	})
}
