package action

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/testutil"
	"gopkg.in/yaml.v3"
)

// proveBin is a prove built from this checkout.
var proveBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "prove-action-test")
	if err != nil {
		panic(err)
	}
	proveBin = filepath.Join(dir, "prove")
	// Build before any test moves HOME, so the build cache is the usual one.
	if out, err := exec.Command("go", "build", "-o", proveBin, "../cmd/prove").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("go build: %v\n%s", err, out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// sh runs a command in dir with env added and returns its exit code and
// combined output.
func sh(t *testing.T, dir string, env []string, name string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return 0, string(out)
}

// published makes a repository with policy, runs prove there, pushes the
// branch and its records to a bare origin, and returns a fresh clone with
// prove on PATH, as a CI job would see it.
func published(t *testing.T, policy string) (clone string, env []string) {
	t.Helper()
	return publish(t, policy, true)
}

// publish is published, with records only when record is set.
func publish(t *testing.T, policy string, record bool) (clone string, env []string) {
	t.Helper()
	repo := testutil.NewRepo(t)
	key := testutil.NewKey(t, "ci@example.com")
	bin := filepath.Dir(proveBin)
	env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "PROVE_SSH_KEY=" + key.Path}
	testutil.WriteFile(t, repo, ".prove.yaml", strings.ReplaceAll(policy, "$KEY", key.Public))
	testutil.Commit(t, repo, "policy")
	if record {
		if code, out := sh(t, repo.Dir, env, proveBin, "run"); code == 2 { // a failing check (1) still stores its record
			t.Fatalf("prove run: %s", out)
		}
	}

	origin := filepath.Join(t.TempDir(), "origin.git")
	testutil.Git(t, repo.Dir, "init", "-q", "--bare", origin)
	testutil.Git(t, repo.Dir, "push", "-q", origin, "HEAD:refs/heads/main", "refs/prove/*:refs/prove/*")
	clone = filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, repo.Dir, "clone", "-q", "--depth=1", "file://"+origin, clone)
	if refs := testutil.Git(t, clone, "for-each-ref", "refs/prove/"); refs != "" {
		t.Fatalf("a clone should start without records, has:\n%s", refs)
	}
	return clone, env
}

const policyHead = `version: 1
signers:
  ci: $KEY
checks:
  - name: build
    run: "true"
`

func TestActionFailsWhenVerifyFails(t *testing.T) {
	script, err := filepath.Abs("verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, policy string
		code         int
		summary      []string
	}{
		{"passes when every check verifies", policyHead, 0,
			[]string{"## prove:", "**PASS**", "| build | pass | ci |"}},
		{"AC-808-3 exits non-zero when prove verify fails and writes the summary either way", policyHead + `  - name: unit
    run: "false"
`, 1, []string{"## prove:", "**FAIL**", "| build | pass | ci |", "| unit | fail | ci |"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone, env := published(t, tc.policy)
			summary := filepath.Join(t.TempDir(), "summary.md")
			env = append(env, "GITHUB_STEP_SUMMARY="+summary, "PROVE_COMMIT="+strings.TrimSpace(testutil.Git(t, clone, "rev-parse", "HEAD")))
			code, out := sh(t, clone, env, "bash", script)
			if code != tc.code {
				t.Fatalf("verify.sh exited %d, want %d:\n%s", code, tc.code, out)
			}
			got, err := os.ReadFile(summary)
			if err != nil {
				t.Fatalf("no job summary: %v\n%s", err, out)
			}
			for _, want := range tc.summary {
				if !strings.Contains(string(got), want) {
					t.Fatalf("summary lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestActionRunModeRunsMissingRecords(t *testing.T) {
	script, err := filepath.Abs("verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, policy, mode string
		record             bool
		code               int
		out, summary       []string
	}{
		{"verify mode fails a commit with no records", policyHead, "verify", false, 1,
			[]string{"no record of check"}, []string{"**FAIL**"}},
		{"run mode runs a check with no record and passes", policyHead, "run", false, 0,
			[]string{"build  run (no record)", "build is stale (no record)"}, []string{"**PASS**", "| build | pass | ci |"}},
		{"run mode runs nothing that has a valid record", policyHead, "run", true, 0,
			[]string{"build has a current signed record, not running it"}, []string{"**PASS**"}},
		{"run mode fails when the check itself fails", policyHead + `  - name: unit
    run: "false"
`, "run", false, 1, []string{"unit is stale (no record)"}, []string{"**FAIL**", "| build | pass | ci |", "| unit | fail | ci |"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone, env := publish(t, tc.policy, tc.record)
			summary := filepath.Join(t.TempDir(), "summary.md")
			env = append(env, "GITHUB_STEP_SUMMARY="+summary, "PROVE_MODE="+tc.mode,
				"PROVE_COMMIT="+strings.TrimSpace(testutil.Git(t, clone, "rev-parse", "HEAD")))
			code, out := sh(t, clone, env, "bash", script)
			if code != tc.code {
				t.Fatalf("verify.sh exited %d, want %d:\n%s", code, tc.code, out)
			}
			for _, want := range tc.out {
				if !strings.Contains(out, want) {
					t.Fatalf("output lacks %q:\n%s", want, out)
				}
			}
			got, err := os.ReadFile(summary)
			if err != nil {
				t.Fatalf("no job summary: %v\n%s", err, out)
			}
			for _, want := range tc.summary {
				if !strings.Contains(string(got), want) {
					t.Fatalf("summary lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestActionRunModeUsesTheCheckoutsPolicy(t *testing.T) {
	// On a pull request the checkout is the merge commit, whose policy can
	// differ from the head's: here the base changed a check's command after
	// the head was recorded. Run mode must rerun that check under the
	// checkout's policy, the one verify then applies, not skip it and go red.
	script, err := filepath.Abs("verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := testutil.NewRepo(t)
	key := testutil.NewKey(t, "ci@example.com")
	env := []string{"PATH=" + filepath.Dir(proveBin) + string(os.PathListSeparator) + os.Getenv("PATH"), "PROVE_SSH_KEY=" + key.Path}
	testutil.WriteFile(t, repo, ".prove.yaml", strings.ReplaceAll(policyHead, "$KEY", key.Public))
	head := testutil.Commit(t, repo, "policy")
	if code, out := sh(t, repo.Dir, env, proveBin, "run"); code != 0 {
		t.Fatalf("prove run: %s", out)
	}
	testutil.WriteFile(t, repo, ".prove.yaml", strings.ReplaceAll(strings.Replace(policyHead, `run: "true"`, `run: "echo strict"`, 1), "$KEY", key.Public))
	testutil.Commit(t, repo, "base changes the build command")

	origin := filepath.Join(t.TempDir(), "origin.git")
	testutil.Git(t, repo.Dir, "init", "-q", "--bare", origin)
	testutil.Git(t, repo.Dir, "push", "-q", origin, "HEAD:refs/heads/main", "refs/prove/*:refs/prove/*")
	for _, mode := range []string{"verify", "run"} {
		t.Run(mode, func(t *testing.T) {
			clone := filepath.Join(t.TempDir(), "clone")
			testutil.Git(t, repo.Dir, "clone", "-q", "file://"+origin, clone)
			code, out := sh(t, clone, append(env, "PROVE_MODE="+mode, "PROVE_COMMIT="+head,
				"GITHUB_STEP_SUMMARY="+filepath.Join(t.TempDir(), "summary.md")), "bash", script)
			want := map[string]int{"verify": 1, "run": 0}[mode]
			if code != want {
				t.Fatalf("verify.sh in %s mode exited %d, want %d:\n%s", mode, code, want, out)
			}
			if mode == "run" && !strings.Contains(out, "build is stale (command differs") {
				t.Fatalf("run mode should rerun build under the checkout's command:\n%s", out)
			}
		})
	}
}

// TestActionCatchesMergeSkew is a pull request whose head passes alone but
// fails merged into a base that changed the check's inputs since.
func TestActionCatchesMergeSkew(t *testing.T) {
	script, err := filepath.Abs("verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := testutil.NewRepo(t)
	key := testutil.NewKey(t, "ci@example.com")
	env := []string{"PATH=" + filepath.Dir(proveBin) + string(os.PathListSeparator) + os.Getenv("PATH"), "PROVE_SSH_KEY=" + key.Path}
	testutil.WriteFile(t, repo, ".prove.yaml", strings.ReplaceAll(`version: 1
signers:
  ci: $KEY
checks:
  - name: web
    run: grep -qx "export toKeepKey" web/lib.ts || ! grep -q toKeepKey web/app.ts
    inputs: ["web/**"]
`, "$KEY", key.Public))
	testutil.WriteFile(t, repo, "web/lib.ts", "export toKeepKey\n")
	testutil.WriteFile(t, repo, "web/app.ts", "\n")
	testutil.Commit(t, repo, "base")
	testutil.Git(t, repo.Dir, "checkout", "-q", "-b", "feature")
	testutil.WriteFile(t, repo, "web/app.ts", "uses toKeepKey\n")
	head := testutil.Commit(t, repo, "use toKeepKey")
	if code, out := sh(t, repo.Dir, env, proveBin, "run"); code != 0 {
		t.Fatalf("the head alone should pass: %s", out)
	}
	testutil.Git(t, repo.Dir, "checkout", "-q", "main")
	testutil.WriteFile(t, repo, "web/lib.ts", "\n")
	testutil.Commit(t, repo, "drop toKeepKey")
	// GitHub's refs/pull/<n>/merge: the head merged into the base.
	testutil.Git(t, repo.Dir, "checkout", "-q", "--detach", "main")
	testutil.Git(t, repo.Dir, "merge", "-q", "--no-edit", "feature")
	merge := testutil.Head(t, repo)

	origin := filepath.Join(t.TempDir(), "origin.git")
	testutil.Git(t, repo.Dir, "init", "-q", "--bare", origin)
	testutil.Git(t, repo.Dir, "push", "-q", origin, "main", "feature", merge+":refs/pull/1/merge", "refs/prove/*:refs/prove/*")
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, repo.Dir, "clone", "-q", "file://"+origin, clone)
	testutil.Git(t, clone, "fetch", "-q", "origin", "refs/pull/1/merge")
	testutil.Git(t, clone, "checkout", "-q", "--detach", merge)

	for _, tc := range []struct {
		name, commit, mode string
		code               int
	}{
		{"the head alone verifies (what the head default checked)", head, "verify", 0},
		{"the merge commit (the default now) fails verify", merge, "verify", 1},
		{"the merge commit fails run mode: web reruns on the merge and fails", merge, "run", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(env, "GITHUB_STEP_SUMMARY="+filepath.Join(t.TempDir(), "summary.md"), "PROVE_COMMIT="+tc.commit, "PROVE_MODE="+tc.mode)
			if code, out := sh(t, clone, env, "bash", script); code != tc.code {
				t.Fatalf("verify.sh exited %d, want %d:\n%s", code, tc.code, out)
			}
		})
	}
}

func TestActionRunsItsScripts(t *testing.T) {
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Runs struct {
			Using string `yaml:"using"`
			Steps []struct {
				Run   string            `yaml:"run"`
				Shell string            `yaml:"shell"`
				Env   map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	// On a pull request github.sha is the head merged into the base.
	if got := a.Inputs["commit"].Default; got != "${{ github.sha }}" {
		t.Fatalf("commit defaults to %q, want the merge commit ${{ github.sha }}", got)
	}
	if a.Runs.Using != "composite" || len(a.Runs.Steps) != 2 {
		t.Fatalf("want a composite action with two steps, got %+v", a.Runs)
	}
	for i, script := range []string{"install.sh", "verify.sh"} {
		step := a.Runs.Steps[i]
		if step.Run != `"$GITHUB_ACTION_PATH/`+script+`"` || step.Shell != "bash" {
			t.Fatalf("step %d runs %q in %q, want %s in bash", i, step.Run, step.Shell, script)
		}
		if info, err := os.Stat(script); err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s must exist and be executable: %v", script, err)
		}
		// Every input reaches a script.
		for _, v := range step.Env {
			name := strings.TrimSuffix(strings.TrimPrefix(v, "${{ inputs."), " }}")
			if _, ok := a.Inputs[name]; !ok {
				t.Fatalf("step %d passes %s, which is not an input", i, v)
			}
		}
	}
}

// release writes a release folder as goreleaser publishes it for version:
// an archive holding binary, and checksums.txt listing checksum for it (the
// archive's own sha256 when checksum is empty). It returns the folder and
// the archive's sha256.
func release(t *testing.T, version string, binary []byte, checksum string) (dir, digest string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "prove", Mode: 0o755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	asset := "prove_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	digest = fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	if checksum == "" {
		checksum = digest
	}
	dir = filepath.Join(t.TempDir(), "releases")
	for path, data := range map[string][]byte{
		"download/v" + version + "/" + asset:      buf.Bytes(),
		"download/v" + version + "/checksums.txt": []byte(checksum + "  " + asset + "\n"),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, digest
}

// fakeGH stands in for `gh attestation verify`: it logs its arguments and
// passes only a file whose sha256 is listed in $FAKE_GH_ATTESTED, as
// GitHub's attestation store would.
const fakeGH = `#!/bin/sh
echo "$@" >>"$FAKE_GH_LOG"
[ "$1 $2" = "attestation verify" ] || exit 2
d=$({ sha256sum "$3" 2>/dev/null || shasum -a 256 "$3"; } | cut -d' ' -f1)
grep -qx "$d" "$FAKE_GH_ATTESTED" || { echo "no attestation for sha256:$d" >&2; exit 1; }
`

// installPath is a PATH holding only the tools install.sh needs, so the
// runner's own gh and Perl's prove stay out of the tests. With gh set it
// also holds fakeGH; extra is put in front.
func installPath(t *testing.T, gh bool, extra ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"awk", "cat", "curl", "cut", "dirname", "grep", "gzip", "mkdir", "mktemp", "sha256sum", "shasum", "tar", "tr", "uname"} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if gh {
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(append(extra, bin), string(os.PathListSeparator))
}

func TestInstallChecksTheRelease(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("releases are for linux and darwin")
	}
	script, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(proveBin)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	pinned := strings.TrimSpace(string(raw))

	// planted is a prove an earlier job left on PATH that passes everything.
	planted := t.TempDir()
	if err := os.WriteFile(filepath.Join(planted, "prove"), []byte("#!/bin/sh\necho prove 9.9.9-planted\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, version, served, checksum string
		attested, gh                    bool
		tamper                          bool
		env                             []string
		path                            []string
		fail                            string // the error wanted; empty: installs the release
		fromPath                        bool   // uses the planted prove
	}{
		{name: "a pinned version", version: "1.2.3", served: "1.2.3", attested: true, gh: true},
		{name: "AC-938-1 no version installs the release action/VERSION pins", served: pinned, attested: true, gh: true},
		{name: "AC-938-1 latest is refused", version: "latest", served: pinned, attested: true, gh: true,
			fail: "must be an exact release"},
		{name: "AC-938-1 a prove on PATH is not used by default", served: pinned, attested: true, gh: true,
			path: []string{planted}},
		{name: "AC-938-1 a prove on PATH is used with from-path: true", served: pinned, attested: true, gh: true,
			path: []string{planted}, env: []string{"PROVE_FROM_PATH=true"}, fromPath: true},
		{name: "a checksum mismatch", version: "v1.2.3", served: "1.2.3", checksum: strings.Repeat("0", 64), attested: true, gh: true,
			fail: "does not match checksums.txt"},
		{name: "AC-938-2 a tampered archive with a matching checksums.txt", version: "1.2.3", served: "1.2.3", attested: true, gh: true, tamper: true,
			fail: "has no build attestation"},
		{name: "AC-938-2 an archive with no attestation", version: "1.2.3", served: "1.2.3", gh: true,
			fail: "has no build attestation"},
		{name: "AC-938-2 no gh to check the attestation", version: "1.2.3", served: "1.2.3", attested: true,
			fail: "GitHub CLI (gh) is needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, digest := release(t, tc.served, binary, tc.checksum)
			if tc.tamper {
				// The attacker replaces the archive and rewrites checksums.txt
				// to match; the attestation still names the genuine digest.
				evil := append(append([]byte{}, binary...), []byte("tampered")...)
				evilDir, _ := release(t, tc.served, evil, "")
				dir = evilDir
			}
			attested := filepath.Join(t.TempDir(), "attested")
			list := ""
			if tc.attested {
				list = digest + "\n"
			}
			if err := os.WriteFile(attested, []byte(list), 0o644); err != nil {
				t.Fatal(err)
			}
			ghLog := filepath.Join(t.TempDir(), "gh.log")
			ghPath := filepath.Join(t.TempDir(), "github_path")
			code, out := sh(t, t.TempDir(), append([]string{
				"PROVE_VERSION=" + tc.version, "PROVE_RELEASES=file://" + dir,
				"RUNNER_TEMP=" + t.TempDir(), "GITHUB_PATH=" + ghPath,
				"FAKE_GH_ATTESTED=" + attested, "FAKE_GH_LOG=" + ghLog,
				"PROVE_FROM_PATH=", "PATH=" + installPath(t, tc.gh, tc.path...),
			}, tc.env...), "bash", script)
			added, _ := os.ReadFile(ghPath)
			switch {
			case tc.fail != "":
				if code == 0 || len(added) > 0 || !strings.Contains(out, tc.fail) {
					t.Fatalf("installed prove (exit %d, GITHUB_PATH %q), want %q:\n%s", code, added, tc.fail, out)
				}
			case tc.fromPath:
				if code != 0 || len(added) > 0 || !strings.Contains(out, filepath.Join(planted, "prove")) {
					t.Fatalf("from-path should use the prove on PATH (exit %d, GITHUB_PATH %q):\n%s", code, added, out)
				}
			default:
				if code != 0 {
					t.Fatalf("install.sh exited %d:\n%s", code, out)
				}
				bin := filepath.Join(strings.TrimSpace(string(added)), "prove")
				if c, v := sh(t, ".", nil, bin, "version"); c != 0 || !strings.HasPrefix(v, "prove ") || strings.Contains(v, "planted") {
					t.Fatalf("installed %s does not run: %d %s", bin, c, v)
				}
				args, _ := os.ReadFile(ghLog)
				want := "--repo Feral-Intelligence/prove --signer-workflow Feral-Intelligence/prove/.github/workflows/release.yml --source-ref refs/tags/v" + tc.served + " --deny-self-hosted-runners"
				if !strings.Contains(string(args), want) {
					t.Fatalf("gh attestation verify ran with %q, want %q", args, want)
				}
			}
		})
	}
}

// TestActionPinsItsRelease keeps the action's pin an exact release, which the
// release workflow checks against the tag it publishes.
func TestActionPinsItsRelease(t *testing.T) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if v := strings.TrimSpace(string(raw)); !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`).MatchString(v) {
		t.Fatalf("action/VERSION is %q, want an exact release such as 1.2.0", v)
	}
}
