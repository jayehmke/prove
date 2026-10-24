package verify_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/fingerprint"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/run"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/verify"
)

type env struct {
	t      *testing.T
	repo   *gitx.Repo
	policy *config.Config
	store  store.Store
}

func setup(t *testing.T, yaml string) *env {
	t.Helper()
	repo := testutil.Repo(t)
	testutil.Write(t, repo, "src/a.txt", "one")
	testutil.Write(t, repo, "README", "hello")
	e := &env{t: t, repo: repo, store: store.NewGit(repo)}
	e.setPolicy(yaml)
	testutil.Commit(t, repo, "first")
	return e
}

func (e *env) setPolicy(yaml string) {
	e.t.Helper()
	p, err := config.Parse([]byte(yaml))
	if err != nil {
		e.t.Fatal(err)
	}
	e.policy = p
}

// run runs a check as the policy defines it, signed by key.
func (e *env) run(check string, key testutil.Key) *run.Result {
	e.t.Helper()
	ch, ok := e.policy.Check(check)
	if !ok {
		e.t.Fatalf("no check %q", check)
	}
	return e.runCheck(ch, key)
}

func (e *env) runCheck(ch config.Check, key testutil.Key) *run.Result {
	e.t.Helper()
	re, _ := e.policy.RequirementPattern()
	res, err := run.Run(context.Background(), run.Options{
		Repo: e.repo, Check: ch, Pattern: re, Signer: key.Signer, Store: e.store,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) verify(sample float64) *verify.Report {
	e.t.Helper()
	commit := testutil.Git(e.t, e.repo, "rev-parse", "HEAD")
	trust, err := sign.NewTrust(e.policy.Signers, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	rep, err := verify.Verify(context.Background(), verify.Options{
		Repo: e.repo, Commit: commit, Policy: e.policy, Store: e.store, Trust: trust,
		Sample: sample, Rand: rand.New(rand.NewSource(1)), Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

func checkResult(t *testing.T, rep *verify.Report, name string) verify.CheckResult {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no result for check %q in %+v", name, rep.Checks)
	return verify.CheckResult{}
}

// wantFail asserts verify failed with a reason containing every part.
func wantFail(t *testing.T, rep *verify.Report, parts ...string) {
	t.Helper()
	if rep.OK {
		t.Fatalf("verify passed, want a failure mentioning %q", parts)
	}
	all := strings.Join(rep.Failures(), "\n")
	for _, p := range parts {
		if !strings.Contains(all, p) {
			t.Errorf("failures don't mention %q:\n%s", p, all)
		}
	}
}

func wantOK(t *testing.T, rep *verify.Report) {
	t.Helper()
	if !rep.OK {
		t.Fatalf("verify failed:\n%s", strings.Join(rep.Failures(), "\n"))
	}
}

func signersYAML(keys map[string]testutil.Key) string {
	s := "signers:\n"
	for id, k := range keys {
		s += "  - identity: " + id + "\n    key: " + k.Public + "\n"
	}
	return s
}

func TestVerifyRejectsTamperedRecord(t *testing.T) {
	alice := testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: \"false\"\n"+signersYAML(map[string]testutil.Key{"alice": alice}))
	res := e.run("unit", alice)
	if res.ExitCode == 0 {
		t.Fatal("the check should fail")
	}
	wantFail(t, e.verify(0), "unit", "failed")
	original := res.Entry

	payload, err := base64.StdEncoding.DecodeString(original.Envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	tamper := map[string]func(env *record.Envelope){
		// The attack that matters: a failing run edited to claim a pass.
		"result": func(env *record.Envelope) {
			p := strings.Replace(string(payload), `"result":"fail"`, `"result":"pass"`, 1)
			p = strings.Replace(p, `"exitCode":1`, `"exitCode":0`, 1)
			env.Payload = base64.StdEncoding.EncodeToString([]byte(p))
		},
		"payloadType": func(env *record.Envelope) { env.PayloadType += "x" },
		"keyid":       func(env *record.Envelope) { env.Signatures[0].KeyID = "SHA256:" + strings.Repeat("A", 43) },
		"sig": func(env *record.Envelope) {
			sig, _ := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
			sig[len(sig)-1] ^= 1
			env.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
		},
	}
	// And single bytes spread across the whole payload.
	for i := 0; i < len(payload); i += len(payload)/16 + 1 {
		i := i
		tamper[fmt.Sprintf("payload byte %d", i)] = func(env *record.Envelope) {
			p := append([]byte{}, payload...)
			p[i] ^= 0x01
			env.Payload = base64.StdEncoding.EncodeToString(p)
		}
	}
	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			bad := original
			bad.Envelope.Signatures = append([]record.Signature{}, original.Envelope.Signatures...)
			mutate(&bad.Envelope)
			if err := e.store.Put(bad); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := e.store.Put(original); err != nil {
					t.Fatal(err)
				}
			}()
			wantFail(t, e.verify(0), "unit", "signature")
		})
	}
}

func TestVerifyRejectsUntrustedSigner(t *testing.T) {
	alice, bob, mallory := testutil.NewKey(t), testutil.NewKey(t), testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: \"true\"\n    signers: [alice]\n"+
		signersYAML(map[string]testutil.Key{"alice": alice, "bob": bob}))

	t.Run("untrusted key", func(t *testing.T) {
		e.run("unit", mallory)
		wantFail(t, e.verify(0), "unit", "untrusted", sign.KeyID(mallory.Signer.PublicKey()))
	})
	t.Run("trusted signer not allowed for the check", func(t *testing.T) {
		e.run("unit", bob)
		wantFail(t, e.verify(0), "unit", "bob", "not allowed")
	})
	t.Run("allowed signer", func(t *testing.T) {
		e.run("unit", alice)
		rep := e.verify(0)
		wantOK(t, rep)
		if got := checkResult(t, rep, "unit").Signer; got != "alice" {
			t.Errorf("signer = %q, want alice", got)
		}
	})
}

func TestVerifyRejectsDifferentCommand(t *testing.T) {
	alice := testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: go test ./a/... ./b/...\n"+signersYAML(map[string]testutil.Key{"alice": alice}))
	// The same check name, but only part of the suite.
	e.runCheck(config.Check{Name: "unit", Run: "true"}, alice)
	wantFail(t, e.verify(0), "unit", "command")
}

func TestVerifyReusesUnchangedInputs(t *testing.T) {
	alice := testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: test -f src/a.txt\n    inputs: [\"src/**\"]\n"+signersYAML(map[string]testutil.Key{"alice": alice}))
	first := e.run("unit", alice)

	testutil.Write(t, e.repo, "README", "changed, but not an input")
	testutil.Commit(t, e.repo, "readme")
	rep := e.verify(0)
	wantOK(t, rep)
	got := checkResult(t, rep, "unit")
	if !got.Reused || got.RecordCommit != first.Statement.Commit() {
		t.Errorf("result = %+v, want reused from %s", got, first.Statement.Commit())
	}

	testutil.Write(t, e.repo, "src/a.txt", "two")
	testutil.Commit(t, e.repo, "input")
	wantFail(t, e.verify(0), "unit", "inputs changed")
}

func TestVerifyNamesMissingCheck(t *testing.T) {
	alice := testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: \"true\"\n  - name: lint\n    run: \"true\"\n"+signersYAML(map[string]testutil.Key{"alice": alice}))
	e.run("unit", alice)
	rep := e.verify(0)
	wantFail(t, rep, "lint")
	for _, f := range rep.Failures() {
		if strings.Contains(f, "unit") {
			t.Errorf("unit has a record but a failure names it: %q", f)
		}
	}
	e.run("lint", alice)
	wantOK(t, e.verify(0))
}

func TestSampleRerunCatchesFalsePass(t *testing.T) {
	alice := testutil.NewKey(t)
	e := setup(t, "checks:\n  - name: unit\n    run: \"false\"\n  - name: lint\n    run: \"true\"\n"+signersYAML(map[string]testutil.Key{"alice": alice}))
	e.run("lint", alice)

	// A trusted signer's record claims `false` exited 0.
	commit := testutil.Git(t, e.repo, "rev-parse", "HEAD")
	tree := testutil.Git(t, e.repo, "rev-parse", "HEAD^{tree}")
	fp, err := fingerprint.New(e.repo).Compute(commit, fingerprint.Spec{Command: "false", Inputs: []string{"**"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	env, err := record.Seal(record.New(commit, tree, record.Predicate{
		Check: "unit", Command: "false", Inputs: []string{"**"}, Fingerprint: fp,
		ExitCode: 0, Result: record.ResultPass, StartedAt: now, FinishedAt: now,
	}), alice.Signer)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.NewEntry(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Put(entry); err != nil {
		t.Fatal(err)
	}

	wantOK(t, e.verify(0)) // signatures alone can't tell
	rep := e.verify(100)
	wantFail(t, rep, "unit", "rerun")
	if c := checkResult(t, rep, "lint"); !c.OK || !c.Sampled {
		t.Errorf("lint = %+v, want rerun and passing", c)
	}
}
