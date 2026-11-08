package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/runner"
	"github.com/jayehmke/prove/internal/testutil"
)

const unsignedPolicy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
    inputs: ["src/**"]
  - name: lint
    run: "exit 3"
`

// noKey takes the signing key out of the environment, as in a CI job that
// runs repo code.
func noKey(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PROVE_SSH_KEY", "SSH_AUTH_SOCK"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func readResult(t *testing.T, path string) runner.Result {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r runner.Result
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return r
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRunUnsignedNeedsNoKey(t *testing.T) {
	t.Run("FLU-937 run --unsigned runs every check with no key anywhere, writes a result for each and stores no record", func(t *testing.T) {
		repo, _ := setup(t, unsignedPolicy)
		noKey(t)
		t.Setenv("CI", "true")
		head := testutil.Head(t, repo)
		if code, _, errOut := prove(t, "run"); code != 2 || !strings.Contains(errOut, "no signing key") {
			t.Fatalf("a signed run here should find no key (exit 2), got %d:\n%s", code, errOut)
		}

		out := filepath.Join(t.TempDir(), "results")
		code, _, errOut := prove(t, "run", "--unsigned", "--out", out)
		if code != 1 {
			t.Fatalf("run --unsigned with a failing check exited %d, want 1:\n%s", code, errOut)
		}
		if got := dirNames(t, out); !slices.Equal(got, []string{"lint.json", "unit.json"}) {
			t.Fatalf("results folder holds %v, want one result per check", got)
		}
		for name, exit := range map[string]int{"unit": 0, "lint": 3} {
			r := readResult(t, filepath.Join(out, name+".json"))
			if r.Check != name || r.Commit != head || r.ExitCode != exit || r.Fingerprint == "" {
				t.Fatalf("%s result = %+v, want check %s on %s with exit %d", name, r, name, head, exit)
			}
		}
		if recs := records(t, repo, head); len(recs) != 0 {
			t.Fatalf("an unsigned run stored %d records", len(recs))
		}
	})
}

func TestRunUnsignedRefuses(t *testing.T) {
	noSigners := `
checks:
  - name: unit
    run: "true"
`
	for _, tc := range []struct {
		name, policy string
		args         []string
		env          []string // KEY=VALUE, $KEY is the test key's file
		why          string
	}{
		{"FLU-937 --unsigned without --out", unsignedPolicy, []string{"--unsigned"}, nil, "--out <dir>"},
		{"FLU-937 --out without --unsigned", unsignedPolicy, []string{"--out", "$OUT"}, nil, "--unsigned and --out"},
		{"FLU-937 an ad-hoc command, which sign can't take from the policy", unsignedPolicy,
			[]string{"--unsigned", "--out", "$OUT", "--name", "adhoc", "--", "true"}, nil, "ad-hoc"},
		{"FLU-937 PROVE_SSH_KEY in the environment", unsignedPolicy, []string{"--unsigned", "--out", "$OUT"},
			[]string{"PROVE_SSH_KEY=$KEY"}, "PROVE_SSH_KEY set"},
		{"FLU-937 PROVE_SIGNER in the environment", unsignedPolicy, []string{"--unsigned", "--out", "$OUT"},
			[]string{"PROVE_SIGNER=ssh"}, "PROVE_SIGNER set"},
		{"FLU-937 an ssh-agent socket, which signs without the key file", unsignedPolicy, []string{"--unsigned", "--out", "$OUT"},
			[]string{"SSH_AUTH_SOCK=/tmp/agent.sock"}, "SSH_AUTH_SOCK set"},
		{"FLU-937 a policy with no signers", noSigners, []string{"--unsigned", "--out", "$OUT", "--stale"}, nil, "needs signers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, key := setup(t, tc.policy)
			noKey(t)
			out := filepath.Join(t.TempDir(), "results")
			for _, kv := range tc.env {
				k, v, _ := strings.Cut(kv, "=")
				t.Setenv(k, strings.ReplaceAll(v, "$KEY", key.Path))
			}
			args := []string{"run"}
			for _, a := range tc.args {
				args = append(args, strings.ReplaceAll(a, "$OUT", out))
			}
			code, _, errOut := prove(t, args...)
			if code != 2 || !strings.Contains(errOut, tc.why) {
				t.Fatalf("prove %v exited %d, want 2 saying %q:\n%s", args, code, tc.why, errOut)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused run made %s: %v", out, err)
			}
			if recs := records(t, repo, testutil.Head(t, repo)); len(recs) != 0 {
				t.Fatalf("a refused run stored %d records", len(recs))
			}
		})
	}
}

func TestRunUnsignedKeepsTheKeyFromChecks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the probe reads /proc")
	}
	t.Run("FLU-937 AC-2 a check under run --unsigned finds the key's path in no ancestor's environment or open files, and under a signed run finds it", func(t *testing.T) {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
		dir := t.TempDir()
		probe, report := filepath.Join(dir, "probe.sh"), filepath.Join(dir, "found")
		repo, key := setup(t, `
signers:
  tester: $TESTER
checks:
  - name: probe
    run: "sh `+quote(probe)+`"
`)
		targets := []string{quote(key.Path)}
		if tmp := os.Getenv("RUNNER_TEMP"); tmp != "" {
			targets = append(targets, quote(filepath.Join(tmp, "prove_key")))
		}
		script := `report=` + quote(report) + `
pid=$$
while [ "$pid" -gt 0 ]; do
  for target in ` + strings.Join(targets, " ") + `; do
    if { tr '\0' '\n' <"/proc/$pid/environ"; } 2>/dev/null | grep -qF -- "$target"; then
      echo "environment of $pid has $target" >>"$report"
    fi
    for fd in /proc/"$pid"/fd/*; do
      case "$(readlink "$fd" 2>/dev/null)" in
      *"$target"*) echo "$fd is $target" >>"$report" ;;
      esac
    done
  done
  pid=$(sed -n 's/^PPid:[[:space:]]*//p' "/proc/$pid/status" 2>/dev/null)
  [ -n "$pid" ] || break
done
`
		if err := os.WriteFile(probe, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}

		noKey(t)
		out := filepath.Join(t.TempDir(), "results")
		if code, _, errOut := prove(t, "run", "--unsigned", "--out", out); code != 0 {
			t.Fatalf("run --unsigned exited %d:\n%s", code, errOut)
		}
		if r := readResult(t, filepath.Join(out, "probe.json")); r.ExitCode != 0 {
			t.Fatalf("the probe exited %d", r.ExitCode)
		}
		if found, err := os.ReadFile(report); !os.IsNotExist(err) {
			t.Fatalf("under run --unsigned the probe found the key:\n%s", found)
		}

		t.Setenv("PROVE_SSH_KEY", key.Path)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run exited %d:\n%s", code, errOut)
		}
		if found, err := os.ReadFile(report); err != nil || !strings.Contains(string(found), key.Path) {
			t.Fatalf("under a signed run the probe should find %s (it can see), found %q: %v", key.Path, found, err)
		}
		if recs := records(t, repo, testutil.Head(t, repo)); len(recs) != 1 {
			t.Fatalf("the signed run stored %d records, want 1", len(recs))
		}
	})
}

// unsignedRun runs prove run --unsigned with no key in the environment and
// returns the results folder.
func unsignedRun(t *testing.T, want int, args ...string) string {
	t.Helper()
	noKey(t)
	out := filepath.Join(t.TempDir(), "results")
	if code, _, errOut := prove(t, append([]string{"run", "--unsigned", "--out", out}, args...)...); code != want {
		t.Fatalf("run --unsigned exited %d, want %d:\n%s", code, want, errOut)
	}
	return out
}

// editResult rewrites the JSON of a result file.
func editResult(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	if data, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSignMatchesASignedRun(t *testing.T) {
	t.Run("FLU-937 run --unsigned then sign makes a record prove verify accepts, binding what a plain prove run of the commit binds", func(t *testing.T) {
		repo, key := setup(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "test -f src/a.go"
    inputs: ["src/**"]
    lockfiles: ["go.sum"]
`)
		testutil.WriteFile(t, repo, "src/a.go", "package a\n")
		testutil.WriteFile(t, repo, "go.sum", "lock\n")
		head := testutil.Commit(t, repo, "src")
		out := unsignedRun(t, 0)

		t.Setenv("PROVE_SSH_KEY", key.Path)
		if code, _, errOut := prove(t, "sign", out); code != 0 || !strings.Contains(errOut, "unit pass (exit 0), signed by ") {
			t.Fatalf("prove sign exited %d:\n%s", code, errOut)
		}
		if code, verifyOut, _ := prove(t, "verify"); code != 0 {
			t.Fatalf("prove verify of the signed result exited %d:\n%s", code, verifyOut)
		}
		recs := records(t, repo, head)
		if len(recs) != 1 {
			t.Fatalf("sign stored %d records, want 1", len(recs))
		}
		signed := recs[0]
		if signed.Tree() != testutil.Tree(t, repo, head) || signed.Commit() != head || signed.Predicate.Command != "test -f src/a.go" {
			t.Fatalf("signed record is of tree %s commit %s command %q, want HEAD's and the policy's", signed.Tree(), signed.Commit(), signed.Predicate.Command)
		}

		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run exited %d:\n%s", code, errOut)
		}
		recs = records(t, repo, head)
		if len(recs) != 2 {
			t.Fatalf("want the signed and the plain record, got %d", len(recs))
		}
		plain := recs[0]
		if plain.Predicate.StartedAt.Equal(signed.Predicate.StartedAt) {
			plain = recs[1]
		}
		s, p := signed.Predicate, plain.Predicate
		if signed.Tree() != plain.Tree() || signed.Commit() != plain.Commit() || s.Fingerprint != p.Fingerprint ||
			s.ExitCode != p.ExitCode || s.Check != p.Check || s.Command != p.Command ||
			!slices.Equal(s.Inputs, p.Inputs) || !slices.Equal(s.Lockfiles, p.Lockfiles) {
			t.Fatalf("signed record binds\n%+v\nbut a plain run binds\n%+v", s, p)
		}
	})
}

func TestSignRunsNothing(t *testing.T) {
	t.Run("FLU-937 sign runs no check command, even with a policy that samples every check", func(t *testing.T) {
		ran := filepath.Join(t.TempDir(), "ran")
		repo, key := setup(t, `
sample: 100
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "echo ran >> '`+ran+`'"
`)
		lines := func() int {
			data, _ := os.ReadFile(ran)
			return strings.Count(string(data), "\n")
		}
		out := unsignedRun(t, 0)
		if n := lines(); n != 1 {
			t.Fatalf("the run ran the check %d times, want 1", n)
		}
		t.Setenv("PROVE_SSH_KEY", key.Path)
		if code, _, errOut := prove(t, "sign", out); code != 0 {
			t.Fatalf("prove sign exited %d:\n%s", code, errOut)
		}
		if n := lines(); n != 1 {
			t.Fatalf("prove sign ran the check: it has run %d times", n)
		}
		if recs := records(t, repo, testutil.Head(t, repo)); len(recs) != 1 {
			t.Fatalf("sign stored %d records, want 1", len(recs))
		}
	})

	t.Run("FLU-937 sign of an empty results folder (--stale found nothing to run) loads no key", func(t *testing.T) {
		setup(t, runPolicy)
		if code, _, errOut := prove(t, "run"); code != 0 {
			t.Fatalf("prove run exited %d:\n%s", code, errOut)
		}
		t.Setenv("CI", "true")
		out := unsignedRun(t, 0, "--stale")
		if got := dirNames(t, out); len(got) != 0 {
			t.Fatalf("nothing was stale, yet the results folder holds %v", got)
		}
		if code, _, errOut := prove(t, "sign", out); code != 0 {
			t.Fatalf("prove sign of no results with no key exited %d:\n%s", code, errOut)
		}
	})
}

func TestSignRefusesResults(t *testing.T) {
	const policy = `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "true"
    inputs: ["src/**"]
  - name: lint
    run: "true"
`
	for _, tc := range []struct {
		name string
		// tamper changes the results folder or the repository after the
		// run and returns what the refusal must say.
		tamper func(t *testing.T, repo *gitx.Repo, out string) string
	}{
		{"FLU-937 a result of another commit", func(t *testing.T, repo *gitx.Repo, out string) string {
			ran := testutil.Head(t, repo)
			testutil.WriteFile(t, repo, "docs/notes.md", "not an input of unit\n")
			now := testutil.Commit(t, repo, "docs")
			return short(ran) + ", not " + short(now)
		}},
		{"FLU-937 a check not in the policy", func(t *testing.T, repo *gitx.Repo, out string) string {
			editResult(t, filepath.Join(out, "unit.json"), func(m map[string]any) { m["check"] = "ghost" })
			if err := os.Rename(filepath.Join(out, "unit.json"), filepath.Join(out, "ghost.json")); err != nil {
				t.Fatal(err)
			}
			return `no check "ghost"`
		}},
		{"FLU-937 a result filed under another check's name", func(t *testing.T, repo *gitx.Repo, out string) string {
			if err := os.Rename(filepath.Join(out, "unit.json"), filepath.Join(out, "lint.json")); err != nil {
				t.Fatal(err)
			}
			return `of check "unit", not "lint"`
		}},
		{"FLU-937 a changed fingerprint", func(t *testing.T, repo *gitx.Repo, out string) string {
			forged := "sha256:" + strings.Repeat("0", 64)
			editResult(t, filepath.Join(out, "unit.json"), func(m map[string]any) { m["fingerprint"] = forged })
			return "fingerprint " + forged
		}},
		{"FLU-937 an unknown field: command", func(t *testing.T, repo *gitx.Repo, out string) string {
			editResult(t, filepath.Join(out, "unit.json"), func(m map[string]any) { m["command"] = "true" })
			return `unknown field "command"`
		}},
		{"FLU-937 an unknown field: signer", func(t *testing.T, repo *gitx.Repo, out string) string {
			editResult(t, filepath.Join(out, "unit.json"), func(m map[string]any) { m["signer"] = "ci" })
			return `unknown field "signer"`
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, key := setup(t, policy)
			ran := testutil.Head(t, repo)
			out := unsignedRun(t, 0, "unit")
			why := tc.tamper(t, repo, out)
			t.Setenv("PROVE_SSH_KEY", key.Path)
			code, _, errOut := prove(t, "sign", out)
			if code != 2 || !strings.Contains(errOut, why) {
				t.Fatalf("prove sign exited %d, want 2 saying %q:\n%s", code, why, errOut)
			}
			for _, commit := range []string{ran, testutil.Head(t, repo)} {
				if recs := records(t, repo, commit); len(recs) != 0 {
					t.Fatalf("a refused sign stored %d records for %s", len(recs), short(commit))
				}
			}
		})
	}

	t.Run("FLU-937 sign signs an edited exitCode as written: the exit code is the run's own fact", func(t *testing.T) {
		repo, key := setup(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "exit 1"
`)
		out := unsignedRun(t, 1)
		editResult(t, filepath.Join(out, "unit.json"), func(m map[string]any) { m["exitCode"] = 0 })
		t.Setenv("PROVE_SSH_KEY", key.Path)
		if code, _, errOut := prove(t, "sign", out); code != 0 {
			t.Fatalf("prove sign exited %d:\n%s", code, errOut)
		}
		recs := records(t, repo, testutil.Head(t, repo))
		if len(recs) != 1 || recs[0].Predicate.ExitCode != 0 || recs[0].Predicate.Result != record.Pass {
			t.Fatalf("want one record of exit 0, pass, as the result says; got %d records", len(recs))
		}
	})
}

func TestSignOnto(t *testing.T) {
	t.Run("FLU-937 sign --onto in another clone signs the merge commit run --unsigned --onto tested, which only the run's clone has", func(t *testing.T) {
		repo, key := setup(t, `
signers:
  tester: $TESTER
checks:
  - name: unit
    run: "test -f base.txt && test -f feature.txt"
`)
		testutil.Git(t, repo.Dir, "checkout", "-q", "-b", "feature")
		testutil.WriteFile(t, repo, "feature.txt", "feature\n")
		testutil.Commit(t, repo, "feature")
		testutil.Git(t, repo.Dir, "checkout", "-q", "main")
		testutil.WriteFile(t, repo, "base.txt", "base\n")
		testutil.Commit(t, repo, "base")
		testutil.Git(t, repo.Dir, "checkout", "-q", "feature")
		out := unsignedRun(t, 0, "--onto", "main")

		clone := filepath.Join(t.TempDir(), "clone")
		testutil.Git(t, repo.Dir, "clone", "-q", "--branch", "feature", "file://"+repo.Dir, clone)
		t.Chdir(clone)
		t.Setenv("PROVE_SSH_KEY", key.Path)
		merge := readResult(t, filepath.Join(out, "unit.json")).Commit
		if code, _, errOut := prove(t, "sign", out, "--commit", merge); code != 2 {
			t.Fatalf("the merge commit should be unknown in this clone, but sign --commit %s exited %d:\n%s", short(merge), code, errOut)
		}
		if code, _, errOut := prove(t, "sign", out, "--onto", "origin/main"); code != 0 {
			t.Fatalf("prove sign --onto exited %d:\n%s", code, errOut)
		}
		if code, verifyOut, errOut := prove(t, "verify", "--onto", "origin/main"); code != 0 {
			t.Fatalf("prove verify --onto exited %d:\n%s%s", code, verifyOut, errOut)
		}
	})
}
