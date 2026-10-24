# prove

prove lets a branch carry signed evidence of exactly which checks ran on
exactly which code, and lets anyone verify that evidence in seconds without
rerunning it.

You run your tests once, where you already run them. `prove run` records the
result as a signed [in-toto](https://in-toto.io) statement about the git tree.
CI runs `prove verify`, which checks that every check in the policy has a
valid record for this code, and passes without running the suite again.

One static binary, offline, any CI. Apache-2.0.

```sh
go install github.com/jayehmke/prove/cmd/prove@latest
```

## Quick start (no config)

```sh
prove run --name test -- go test ./...
prove verify
```

The first run makes a signing key for your machine
(`~/.config/prove/signing_key`). With no `.prove.yaml`, the input is the whole
repository, records go to the git store, and verify trusts this machine's key.

## A policy

`.prove.yaml` at the repository root:

```yaml
version: 1
store:
  type: git                  # git (refs/prove/*), local, or an adapter name
checks:
  - name: unit
    run: gotestsum --junitfile "$PROVE_OUT/unit.xml" -- -race ./...
    inputs: ["**/*.go", "go.mod", "testdata/**", "!docs/**"]
    lockfiles: [go.sum]
    junit: $PROVE_OUT/unit.xml
    signers: [alice, ci]     # who may sign this check (default: any trusted signer)
  - name: lint
    run: golangci-lint run
signers:
  - identity: alice
    key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... alice@laptop
  - identity: ci
    key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... ci
requirements:
  pattern: 'AC-[0-9]+(-[0-9]+)*'  # requirement IDs in test names (this is the default)
  required: [AC-12-1, AC-12-2]
sample: 5                    # percent of checks verify reruns (default 0)
```

```sh
prove run              # every check
prove run unit         # one check
prove verify           # exit 0 only when every check passes
prove verify --sample 100   # also rerun every check to spot-check
prove show --format md # one line per check, for a PR comment
```

## What a check is

A check is a **named command** plus the **files it reads**, given as globs.
There are no per-language adapters. The default input is the whole repository,
which is always safe; narrow it on purpose.

The **fingerprint** of a check is a sha256 over its command, its sorted input
paths and their contents, and its lockfiles, read from the git tree. When the
fingerprint is unchanged, the result is reused: verify accepts a record made
on another commit when the fingerprint it recomputes on this tree matches.
Change a README and your test records still count; change a `.go` file and
`unit` needs a new run.

`prove run` refuses a dirty working tree (untracked files included), so the
record always describes committed code.

Patterns match whole paths from the root: `*` within a segment, `**` across
segments, a directory name covers everything under it, `!` excludes, and the
last matching pattern wins.

## Per-test results

Point `junit:` at the JUnit XML your tool writes (go via gotestsum, jest,
pytest, Playwright, golangci-lint, ESLint, ruff and swift all can). prove
provides `$PROVE_OUT`, a scratch directory that keeps reports out of the
working tree. The record then lists every test's result and the requirement
IDs found in test names; verify fails when a `required` ID has no passing test
(or has a failing one), and names the ID.

## The record

An in-toto Statement v1 in a DSSE envelope:

- subject: the git tree (`gitTree` digest) and commit (`gitCommit`)
- predicateType: `https://github.com/jayehmke/prove/check/v0.1`
- predicate: check name, command, inputs, lockfiles, fingerprint, exit code,
  result, timing, runner, and optional tests and requirements

The built-in signer makes SSHSIG signatures (the `ssh-keygen -Y sign` format,
namespace `prove`) with an SSH key: `--key`, `$PROVE_SIGNING_KEY`, git's
`user.signingkey` when `gpg.format=ssh`, or the default key. Public key files
and passphrase-protected keys sign through ssh-agent.

## Where records live

The **git store** (the default) keeps each record under
`refs/prove/records/<commit>/<id>`, never on a branch. Share records with:

```sh
git push origin 'refs/prove/*:refs/prove/*'
git fetch origin 'refs/prove/*:refs/prove/*'
```

The **local store** keeps them as files (`store: {type: local, path: ...}`,
default `.git/prove/records`). Anything else is an adapter program; see
[docs/adapters.md](docs/adapters.md).

## What verify trusts

Signatures prove who signed a record and that nothing changed since. They
don't prove the contents are true. prove's defenses:

- **completeness**: every policy check must have a usable record
- **command match**: a record of a different command (a subset of the suite)
  doesn't count
- **fingerprint recompute**: verify hashes the inputs itself
- **signer rules**: only trusted keys, and only the signers allowed per check
- **sampling**: `--sample <pct>` reruns that share of checks in a clean
  worktree and fails on any disagreement with its record

In CI, read the policy from the base branch (`--policy-ref origin/main`), so a
change can't weaken the rules it is judged by.

## Exit codes

`0` success, `1` a check or verification failed, `2` bad usage or setup.
