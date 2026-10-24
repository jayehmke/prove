// Command prove runs checks, signs their results, and verifies them.
//
//	prove run [check...]                 run checks from .prove.yaml
//	prove run --name <n> -- <command...> run a command with no config
//	prove verify [commit] [--sample pct] check every policy check has a valid record
//	prove show [commit] [--format md|json]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/run"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/verify"
)

// Env is what Main needs from the process.
type Env struct {
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "prove:", err)
		os.Exit(2)
	}
	os.Exit(Main(os.Args[1:], Env{Dir: dir, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}))
}

const usage = `prove runs checks, signs their results, and verifies them without rerunning.

Usage:
  prove run [check...] [--key path] [--signer name]
  prove run --name <name> [--input glob]... [--lockfile path]... [--junit path] -- <command...>
  prove verify [commit] [--sample pct] [--config path | --policy-ref ref] [--format text|json]
  prove show [commit] [--format md|json] [--config path | --policy-ref ref]
  prove version
`

// errUsage marks errors that exit 2.
var errUsage = errors.New("usage")

// Main runs prove with args and returns the exit code: 0 success, 1 a check
// or verification failed, 2 bad usage or setup.
func Main(args []string, env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return 2
	}
	var code int
	var err error
	switch args[0] {
	case "run":
		code, err = cmdRun(args[1:], env)
	case "verify":
		code, err = cmdVerify(args[1:], env, false)
	case "show":
		code, err = cmdVerify(args[1:], env, true)
	case "version", "--version":
		fmt.Fprintln(env.Stdout, "prove", run.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, usage)
		return 0
	default:
		err = fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "prove:", strings.TrimPrefix(err.Error(), "usage: "))
		if errors.Is(err, errUsage) {
			fmt.Fprint(env.Stderr, "\n"+usage)
			return 2
		}
		return 1
	}
	return code
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// parse parses flags anywhere among the positional arguments, and returns
// the positionals and anything after "--".
func parse(fs *flag.FlagSet, args []string) (positional, command []string, err error) {
	fs.SetOutput(io.Discard)
	for i, a := range args {
		if a == "--" {
			command = args[i+1:]
			args = args[:i]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errUsage, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, command, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func loadPolicyFile(repo *gitx.Repo, path string) (*config.Config, error) {
	if path == "" {
		return config.Load(filepath.Join(repo.Dir, config.FileName))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return config.Parse(data)
}

func cmdRun(args []string, env Env) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("name", "", "run an ad-hoc check with this name")
	var inputs, lockfiles multiFlag
	fs.Var(&inputs, "input", "input glob for --name (repeatable; default: the whole repo)")
	fs.Var(&lockfiles, "lockfile", "lockfile for --name (repeatable)")
	junitPath := fs.String("junit", "", "JUnit XML path for --name")
	keyFlag := fs.String("key", "", "SSH signing key")
	signerFlag := fs.String("signer", "", "external signer adapter (prove-signer-<name>)")
	configPath := fs.String("config", "", "policy file (default .prove.yaml)")
	positional, command, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	repo, err := gitx.Open(env.Dir)
	if err != nil {
		return 0, err
	}
	cfg, err := loadPolicyFile(repo, *configPath)
	if err != nil {
		return 0, err
	}

	var checks []config.Check
	switch {
	case *name != "":
		if len(command) == 0 || len(positional) > 0 {
			return 0, fmt.Errorf("%w: --name needs a command after --, and no check names", errUsage)
		}
		ch := config.Check{Name: *name, Run: shellJoin(command), Inputs: inputs, Lockfiles: lockfiles, JUnit: *junitPath}
		if p, ok := cfg.Check(*name); ok && p.Run != ch.Run {
			fmt.Fprintf(env.Stderr, "prove: note: the policy's %s runs %q; verify won't count this record for it\n", *name, p.Run)
		}
		checks = []config.Check{ch}
	case len(command) > 0:
		return 0, fmt.Errorf("%w: a command after -- needs --name", errUsage)
	case len(positional) > 0:
		for _, n := range positional {
			ch, ok := cfg.Check(n)
			if !ok {
				return 0, fmt.Errorf("%w: no check %q in %s", errUsage, n, config.FileName)
			}
			checks = append(checks, ch)
		}
	default:
		if len(cfg.Checks) == 0 {
			return 0, fmt.Errorf("%w: no checks in %s; run one with prove run --name <name> -- <command>", errUsage, config.FileName)
		}
		checks = cfg.Checks
	}

	// Refuse a dirty tree before making keys or running anything.
	if err := run.CheckClean(repo); err != nil {
		return 0, err
	}

	var signer record.Signer
	if n := firstNonEmpty(*signerFlag, env.Getenv("PROVE_SIGNER")); n != "" {
		signer = &sign.External{Name: n, Dir: repo.Dir}
	} else {
		s, created, err := sign.LoadOrCreate(env.Getenv, repo, *keyFlag)
		if err != nil {
			return 0, err
		}
		if created != "" {
			fmt.Fprintf(env.Stderr, "prove: made a signing key for this machine at %s\n", created)
			fmt.Fprintf(env.Stderr, "prove: to trust it, add to signers in %s:\n  - identity: <you>\n    key: %s\n",
				config.FileName, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))))
		}
		signer = s
	}
	st, err := store.Open(cfg.Store, repo)
	if err != nil {
		return 0, err
	}
	pattern, err := cfg.RequirementPattern()
	if err != nil {
		return 0, err
	}

	code := 0
	for _, ch := range checks {
		fmt.Fprintf(env.Stderr, "prove: running %s: %s\n", ch.Name, ch.Run)
		res, err := run.Run(context.Background(), run.Options{
			Repo: repo, Check: ch, Pattern: pattern, Signer: signer, Store: st,
			Stdout: env.Stdout, Stderr: env.Stderr,
		})
		if err != nil {
			return 0, err
		}
		result := res.Statement.Predicate.Result
		fmt.Fprintf(env.Stderr, "prove: %s %s (exit %d) on %s, record %s stored\n",
			ch.Name, result, res.ExitCode, short(res.Statement.Commit()), short(res.Entry.ID))
		if res.ExitCode != 0 {
			code = 1
		}
	}
	return code, nil
}

func cmdVerify(args []string, env Env, show bool) (int, error) {
	name := "verify"
	if show {
		name = "show"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	sample := fs.Float64("sample", -1, "percent of checks to rerun locally (default: the policy's sample)")
	configPath := fs.String("config", "", "policy file (default: .prove.yaml at the commit)")
	policyRef := fs.String("policy-ref", "", "read .prove.yaml from this ref, such as the base branch")
	keyFlag := fs.String("key", "", "with no trusted signers in the policy, trust this key")
	defFormat := "text"
	if show {
		defFormat = "md"
	}
	format := fs.String("format", defFormat, "output format")
	positional, command, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if len(positional) > 1 || len(command) > 0 {
		return 0, fmt.Errorf("%w: %s takes at most one commit", errUsage, name)
	}
	repo, err := gitx.Open(env.Dir)
	if err != nil {
		return 0, err
	}
	rev := "HEAD"
	if len(positional) == 1 {
		rev = positional[0]
	}
	commit, err := repo.ResolveCommit(rev)
	if err != nil {
		return 0, fmt.Errorf("%s is not a commit", rev)
	}

	var policy *config.Config
	switch {
	case *configPath != "":
		policy, err = loadPolicyFile(repo, *configPath)
	default:
		ref := commit
		if *policyRef != "" {
			ref = *policyRef
		}
		var data []byte
		var ok bool
		if data, ok, err = repo.ReadFile(ref, config.FileName); err == nil {
			if ok {
				policy, err = config.Parse(data)
			} else {
				policy = config.Zero()
			}
		}
	}
	if err != nil {
		return 0, err
	}
	st, err := store.Open(policy.Store, repo)
	if err != nil {
		return 0, err
	}
	var local ssh.PublicKey
	if len(policy.Signers) == 0 {
		local = localKey(env, repo, *keyFlag)
	}
	trust, err := sign.NewTrust(policy.Signers, local)
	if err != nil {
		return 0, err
	}
	trust.WithDir(repo.Dir)
	pct := policy.Sample
	if *sample >= 0 {
		pct = *sample
	}
	if pct > 100 {
		return 0, fmt.Errorf("%w: --sample is a percent (0-100)", errUsage)
	}
	if show {
		pct = 0
	}
	rep, err := verify.Verify(context.Background(), verify.Options{
		Repo: repo, Commit: commit, Policy: policy, Store: st, Trust: trust,
		Sample: pct, Stdout: env.Stderr, Stderr: env.Stderr,
	})
	if err != nil {
		return 0, err
	}
	if local != nil {
		rep.Notes = append(rep.Notes, fmt.Sprintf("the policy names no signers, so this machine's key (%s) is trusted as %q", sign.KeyID(local), sign.LocalIdentity))
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return 0, err
		}
	case "md":
		writeMarkdown(env.Stdout, rep)
	case "text":
		if show {
			return 0, fmt.Errorf("%w: show formats are md and json", errUsage)
		}
		writeText(env.Stdout, rep)
	default:
		return 0, fmt.Errorf("%w: unknown format %q", errUsage, *format)
	}
	if !show && !rep.OK {
		return 1, nil
	}
	return 0, nil
}

// localKey is this machine's public key, trusted when the policy trusts no
// one. It is nil when there is no key yet.
func localKey(env Env, repo *gitx.Repo, flag string) ssh.PublicKey {
	spec, _, err := sign.KeySpec(env.Getenv, repo, flag)
	if err != nil {
		return nil
	}
	if !strings.HasPrefix(spec, "key::") {
		if _, err := os.Stat(spec); err != nil {
			return nil
		}
	}
	for _, p := range []string{spec + ".pub", spec} {
		if data, err := os.ReadFile(p); err == nil {
			if pub, _, _, _, err := ssh.ParseAuthorizedKey(data); err == nil {
				return pub
			}
		}
	}
	if pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimPrefix(spec, "key::"))); err == nil {
		return pub
	}
	s, err := sign.LoadSSH(spec)
	if err != nil {
		return nil
	}
	return s.PublicKey()
}

func writeText(w io.Writer, rep *verify.Report) {
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if rep.OK {
		reused := 0
		for _, c := range rep.Checks {
			if c.Reused {
				reused++
			}
		}
		fmt.Fprintf(w, "verified %s: %d checks pass (%d reused from earlier commits)", short(rep.Commit), len(rep.Checks), reused)
		if n := len(rep.Requirements); n > 0 {
			fmt.Fprintf(w, ", %d requirements met", n)
		}
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintf(w, "%s is not verified:\n", short(rep.Commit))
	for _, f := range rep.Failures() {
		fmt.Fprintf(w, "  FAIL %s\n", f)
	}
}

func writeMarkdown(w io.Writer, rep *verify.Report) {
	verdict := "❌ not verified"
	if rep.OK {
		verdict = "✅ verified"
	}
	fmt.Fprintf(w, "### prove: %s %s\n\n", short(rep.Commit), verdict)
	for _, c := range rep.Checks {
		mark := "❌"
		if c.OK {
			mark = "✅"
		}
		result := c.Result
		if c.Reason != "" {
			result += ": " + c.Reason
		}
		signer := "no signer"
		if c.Signer != "" {
			signer = "signed by " + c.Signer
		}
		origin := "new"
		if c.Reused {
			origin = "reused from " + short(c.RecordCommit)
		}
		if c.Sampled {
			origin += ", rerun"
		}
		fmt.Fprintf(w, "- %s `%s` · %s · %s · %s\n", mark, c.Name, result, signer, origin)
	}
	if len(rep.Requirements) > 0 {
		required, met := 0, 0
		for _, r := range rep.Requirements {
			if r.Required {
				required++
				if r.Met {
					met++
				}
			}
		}
		fmt.Fprintf(w, "\nRequirements: %d/%d required met, %d found in tests\n\n", met, required, len(rep.Requirements))
		for _, r := range rep.Requirements {
			mark := "✅"
			detail := strings.Join(r.Tests, ", ")
			if !r.Met {
				mark, detail = "❌", r.Reason
			}
			fmt.Fprintf(w, "- %s %s · %s\n", mark, r.ID, detail)
		}
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "\n_%s_\n", n)
	}
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// shellJoin quotes argv into one sh command line.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:,+@%") == "" {
			quoted[i] = a
		} else {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}
