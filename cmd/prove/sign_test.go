package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

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
	t.Setenv("PROVE_SSH_KEY", "")
	_ = os.Unsetenv("PROVE_SSH_KEY")
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
