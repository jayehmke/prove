# .prove.yaml

The policy at the repository root says which checks every commit needs,
who may sign them and where records are kept. Without one, `prove run`
needs `--name <n> -- <cmd>` and `prove verify` checks whatever is recorded
for the tree, trusting only this machine's key.

```yaml
version: 1

checks:
  - name: unit
    run: go test -race ./...
    inputs: ["**/*.go", "go.mod"]   # default: "**", the whole repo
    lockfiles: [go.sum]
    junit: out/junit.xml             # optional per-test results (keep it git-ignored)
    signers: [ci, alice]             # optional: who may sign this check
  - name: lint
    run: golangci-lint run

# Trusted signers: identity -> public key. A string is an SSH public key.
signers:
  alice: ssh-ed25519 AAAAC3Nza... alice@example.com
  ci: ssh-ed25519 AAAAC3Nza... ci
  # kms: {type: kms, key: "<key id>"}   # verified by prove-signer-kms

# Requirement IDs come from test names in the JUnit reports.
requirements:
  pattern: 'AC-\d+-\d+'
  required: [AC-12-1, AC-12-2]

store: git      # git (default), local, s3, or another adapter; see below
share: s3       # optional: where prove push --share copies records; see below
sample: 0       # percent of checks `prove verify` reruns by default
```

prove rejects unknown fields, so a typo fails loudly instead of being
ignored.

## version

`1`, or left out.

## checks

| Field | Meaning |
|---|---|
| `name` | Letters, digits, `.`, `_` and `-`. Unique. |
| `run` | The command, run with `sh -c` at the repository root. |
| `inputs` | Globs of the files the command reads. Default `**`, the whole repository. |
| `lockfiles` | Globs of dependency lockfiles, hashed with the inputs. |
| `junit` | The JUnit XML file the command writes, inside the repository. |
| `signers` | The signers allowed to sign this check. Default: any trusted signer. |

A check's **fingerprint** is a sha256 over `run`, the sorted input paths
and their contents, and the lockfiles. When none of them changed, verify
reuses a record made on an earlier commit. Narrow `inputs` to what the
command really reads to get that reuse; too narrow and a change that
matters is not noticed, so the default is everything.

Globs match repository paths. `**` matches any number of folders; a
pattern without a `/` (like `*.go`) matches at any depth, as in
`.gitignore`; a leading `/` anchors it at the root.

A check must leave the working tree as it found it. If the command edits
tracked files, writes files git doesn't ignore, or moves HEAD, `prove run`
stores nothing and says what changed.

### Per-test detail

Set `junit:` to the JUnit XML file the command writes: go (gotestsum), jest,
pytest, Playwright, golangci-lint, ESLint, ruff and swift can all write one.
The record lists each test's result and the requirement IDs
`requirements.pattern` finds in its name.

## signers

Identity to public key. A string is an SSH public key in
`authorized_keys` form. A map `{type: <name>, key: <key id>}` is checked by
the `prove-signer-<name>` adapter (see [adapters.md](adapters.md)).

Verify names a signer by the trusted key that verifies its signature, not
by the identity the record claims. A record signed by any other key fails
with `signed by untrusted signer`.

With no `signers`, verify trusts only the key of the machine it runs on.
In CI, always list the signers.

`.prove.yaml` is read from the checkout being verified, so in CI a pull
request decides its own signers (it could add its own key). To hold a pull
request to the base branch's rules, verify with the base branch's policy.

## requirements

`pattern` is a regular expression matching one requirement ID in a test
name. Each ID in `required` needs at least one passing test across the
policy's checks; `prove verify` fails with
`requirement AC-12-3 has no passing test` otherwise, and `prove show`
lists every ID found with its tests.

## store

Where records are kept. A string names the type; a map adds options:

```yaml
store:
  type: s3
  options:
    bucket: my-prove-records
    prefix: prove
```

| Type | Records |
|---|---|
| `git` (default) | Commits under `refs/prove/records/<id>` in this repository. Push and fetch `refs/prove/*` to share them. |
| `local` | A folder, `path:` (default `.git/prove/records`). |
| `s3` | An S3 bucket, with `prove-store-s3`. See [adapters.md](adapters.md#s3). |
| anything else | The `prove-store-<type>` adapter on `PATH`, given `options:`. |

## share

A second store, for hosts where records can't be pushed as git refs (a
Claude Code cloud session can push only branches, say). It takes the same
form as `store`, of any type but `git`:

```yaml
share:
  type: s3
  options:
    bucket: my-prove-records
    region: us-east-1
```

- `prove push --share` copies the records a verifier of the commit needs
  into it: the exact bytes, with no git push, so no ref or branch is made.
  It never replaces a record the share store has. A record it can't copy
  is reported, and the push still succeeds: a verifier without that record
  runs the check.
- `prove verify`, `plan`, `show` and `run --stale` read it together with
  `store`. Records are still written to `store` only.
- The share store is untrusted, like any store: verify checks every
  record's signature. If it can't be read (no credentials on this machine,
  say), prove warns once and goes on without it, so a check whose record
  is only there runs instead of passing.

## sample

The percent of passing checks `prove verify` reruns, picked at random, in
a temporary checkout, to catch a signer whose records claim more than its
runs showed. A rerun that
disagrees with the record fails verify. `prove verify --sample <pct>`
overrides it for one run.
