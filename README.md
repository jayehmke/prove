# prove

prove records signed evidence of which checks ran on which code, and lets
anyone verify that evidence in seconds without running the checks again.

```console
$ prove run                 # run the checks in .prove.yaml, sign and store the results
$ prove verify              # every check has a valid, trusted, current record? exit 0 or 1
$ prove show --format md    # what is recorded for this commit, as a table
```

It is one static Go binary. It works offline and with any CI.

## How it works

A **check** is a named command plus the files it reads (`inputs`, globs).
The default input is the whole repository, which is always safe; narrow it
on purpose.

`prove run` refuses a dirty working tree, runs the command, and writes a
**record**: an [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
in a [DSSE envelope](https://github.com/secure-systems-lab/dsse), signed by
your key. Its subject is the git tree (and commit) the check ran on, and its
predicate type is `https://github.com/jayehmke/prove/check/v0.1`.

The record carries the check's **fingerprint**: a sha256 over the command,
the sorted input paths and their contents, and any lockfiles. On a later
commit where none of a check's inputs changed, the fingerprint is the same,
so verify reuses the record instead of asking for a new run.

`prove verify [commit]` checks every policy check and gives one reason per
failure:

| Problem | Reason |
|---|---|
| no record for a check | `no record of check "lint" ...` |
| a record changed after signing | `signature: ...` |
| signed by a key that isn't trusted | `signed by untrusted signer "mallory@example.com" (key SHA256:...)` |
| a trusted signer not allowed for the check | `signer "dev" is not allowed to sign check "release"` |
| a different command (a subset of the tests) | `command differs: the record ran "go test ./pkg/a" but the policy runs "go test ./..."` |
| an input changed since the record | `inputs changed since the record made on abc1234` |
| the run failed | `the check failed: exit 1 on abc1234` |
| a required requirement ID has no passing test | `requirement AC-12-3 has no passing test (...)` |
| `--sample` reran the check and it failed | `sample rerun disagrees: ...` |

### What a signature proves

A signature proves who signed a record and that nothing changed since. It
does not prove the contents are true: a trusted signer could sign a pass it
never earned. prove narrows that with completeness (every policy check needs
a record), command match, fingerprint recompute, `--sample` reruns, and
`signers:` rules that say who may sign which check.

`prove verify` reads `.prove.yaml` from the checkout it runs in. In CI on a
pull request, the PR therefore decides its own checks, signers and required
IDs (it could add its own key), the same trust as a PR editing its CI
workflow. To hold a PR to the base branch's rules, verify with the base
branch's policy.

A check must leave the working tree as it found it. If the command edits
tracked files or writes files git doesn't ignore (`--fix`, `gofmt -w`,
codegen), or moves HEAD (commits, checks out, resets), `prove run` stores
nothing and says what changed, because the command did not test the
committed code.

## Quick start

Install prove: download a binary for linux or macOS from
[Releases](https://github.com/jayehmke/prove/releases) (check it against
`checksums.txt`), or build it with Go:

```console
$ go install github.com/jayehmke/prove/cmd/prove@latest
```

prove signs with your SSH key: git's signing key when `gpg.format=ssh`,
else `~/.ssh/id_ed25519` (see [Signing](#signing)). Then, in any git
repository with everything committed, record a check and verify it. No
config is needed:

```sh quickstart
prove run --name unit -- go test ./...
prove verify
```

Use your own test command after `--`. `prove verify` prints `PASS` and
exits 0. Commit a change and it fails until you run the check again,
unless you narrow the check's inputs (see below).

With no `.prove.yaml`, `prove verify` checks every check recorded for this
tree against its own command, and trusts only this machine's key. That is
for one person on one machine; a team writes a policy.

## .prove.yaml

```yaml
version: 1
checks:
  - name: unit
    run: go test -race ./...
    inputs: ["**/*.go", "go.mod"]   # default: "**", the whole repo
    lockfiles: [go.sum]
  - name: lint
    run: golangci-lint run
signers:
  alice: ssh-ed25519 AAAAC3Nza... alice@example.com
  ci: ssh-ed25519 AAAAC3Nza... ci
```

`prove run` with no arguments runs every check. Every field, per-test
detail from JUnit reports, requirement IDs, `--sample` reruns and
per-check signers are in [docs/policy.md](docs/policy.md).

## In CI

The [GitHub action](action/action.yml) runs `prove verify` and posts
`prove show --format md` as the job summary, pass or fail. The job fails
when verify fails.

```yaml
- uses: actions/checkout@v4
- uses: jayehmke/prove/action@v0.1.0 # a release tag
  with:
    args: --sample 10                # optional
```

It fetches `refs/prove/*` from origin first, so records pushed with the
git store are found, and it verifies the pull request's head commit. It
installs the latest release unless you pin `version:`. Ubuntu runners have
Perl's unrelated `prove` on `PATH`; the action installs its own in front.
Any other CI runs the same two commands: `prove verify`, then
`prove show --format md`.

## Signing

| Variable | Meaning |
|---|---|
| `PROVE_SSH_KEY` | SSH private key file, or a `.pub` file whose key is in ssh-agent. Default: git's `user.signingkey` when `gpg.format=ssh`, then `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`. |
| `PROVE_IDENTITY` | the identity written into records. Default: git's `user.email`. Verify names signers by the trusted key, not by this claim. |
| `PROVE_SIGNER` | the name of a `prove-signer-<name>` adapter instead of SSH. |

SSH signatures use the SSHSIG format with namespace `prove`, so
`ssh-keygen -Y verify -n prove` accepts them and they can't be replayed as
git commit signatures.

## Stores

- **git** (the default) keeps each record as a commit under
  `refs/prove/records/<id>`. It never touches a branch, HEAD, the index or
  the working tree. Share records with
  `git push origin 'refs/prove/*:refs/prove/*'` and
  `git fetch origin 'refs/prove/*:refs/prove/*'`.
- **local** keeps records in a folder (`path:`; default `.git/prove/records`).
- **s3** keeps records in an S3 bucket with `prove-store-s3`, released with
  prove. It never overwrites an object.
- Anything else is a `prove-store-<name>` adapter. See
  [docs/adapters.md](docs/adapters.md).

## Exit codes

`0` everything passed, `1` a check or verification failed, `2` a usage or
setup error (a dirty tree, no key, a bad `.prove.yaml`).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Contributions need a signed CLA.

## License

Apache-2.0.
