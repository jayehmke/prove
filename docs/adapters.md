# Adapters

Storage and signing are adapters. prove has three built in: the `local` and
`git` stores and the `ssh` signer. Anything else is an external program on
`PATH`, in the style of git credential helpers. prove's releases include
two, [`prove-store-s3`](#s3) and [`prove-store-github`](#github).

| Kind   | Program name           | Picked by                                 |
|--------|------------------------|-------------------------------------------|
| store  | `prove-store-<name>`   | `store: {type: <name>}` in `.prove.yaml`  |
| signer | `prove-signer-<name>`  | `PROVE_SIGNER=<name>` when signing; `{type: <name>, key: ...}` under `signers:` when verifying |

`<name>` is letters, digits, `.`, `_` and `-`. prove refuses any other
name, so an adapter is always a program found on `PATH`, never a file in
the repository.

## The protocol

prove starts the program once per request. It writes one JSON object to the
program's stdin and closes it, then reads one JSON object from its stdout.

- Success: exit 0 and print the answer.
- Failure: print `{"error": "<message>"}`, or exit non-zero with a message on
  stderr. prove fails with that message and the program's name.
- Anything on stdout that is not JSON fails with
  `adapter prove-store-<name> returned invalid JSON`.

Binary data (records, payloads, signatures) is standard base64 with padding.
Record IDs are the sha256 of the record bytes, as 64 lowercase hex digits;
prove checks every ID before it calls an adapter, so an adapter can use one
as a file name or object key.

### Stores

Every request has `"op"` and `"config"` (the `store.options` map from
`.prove.yaml`, or `{}`).

**put** stores a record.

```json
{"op": "put", "config": {...},
 "meta": {"id": "<64 hex>", "check": "unit", "commit": "<sha>", "tree": "<sha>",
          "signer": "alice@example.com", "fingerprint": "sha256:...",
          "result": "pass", "finishedAt": "2026-10-24T12:00:00Z"},
 "record": "<base64 record bytes>"}
```

Answer `{"ok": true}`. A put of an ID that is already stored must succeed.

**get** returns the record bytes exactly as they were put.

```json
{"op": "get", "config": {...}, "id": "<64 hex>"}
```

Answer `{"record": "<base64>"}`, or an error if there is no such record.

**list** returns the metadata of stored records.

```json
{"op": "list", "config": {...},
 "query": {"check": "unit", "commit": "...", "tree": "...", "signer": "...", "fingerprint": "..."}}
```

Every query field is optional; missing means "any". Answer
`{"records": [<meta>, ...]}` with the `meta` objects given to put. prove
filters the answer by the query again, so a store may return more than asked.

A store does not need to be trusted. Meta is only a lookup hint: verify
checks the signature on the record bytes and reads the commit, tree, check,
signer and fingerprint from the signed statement. A store that loses,
changes or invents records can make a check fail, never pass.

### Signers

**sign** signs a message. `payload` is the DSSE pre-authentication encoding
of the record; sign it as given.

```json
{"op": "sign", "payload": "<base64>", "identity": "ci@example.com"}
```

Answer `{"keyid": "<key id>", "sig": "<base64 signature>"}`. The key ID is
stored with the signature and is how verify finds the trusted key.

**verify** checks a signature against a trusted key from `.prove.yaml`.

```json
{"op": "verify", "payload": "<base64>", "sig": "<base64>",
 "keyid": "<key id>", "key": "<the key string from .prove.yaml>"}
```

Answer `{"ok": true}`, or `{"ok": false, "reason": "..."}`.

**key** returns the key ID this signer signs with. prove uses it to trust the
local signer when `.prove.yaml` lists no signers.

```json
{"op": "key"}
```

Answer `{"keyid": "<key id>"}`.

To trust an external signer, list its key ID under `signers:`:

```yaml
signers:
  ci:
    type: kms            # verified by prove-signer-kms
    key: arn:aws:kms:us-east-1:111122223333:key/abcd   # must equal the keyid it signs with
```

## Worked example: a folder store

[`examples/prove-store-dir`](examples/prove-store-dir) is a complete store
adapter in 40 lines of shell and jq. It keeps each record as
`<id>.record` (the base64 bytes) and `<id>.meta` in a folder.

Install it on `PATH`, then point `.prove.yaml` at it:

```yaml
store:
  type: dir
  options:
    dir: /srv/prove-records
checks:
  - name: unit
    run: go test ./...
```

```console
$ prove run
prove: running unit: go test ./...
ok      example.com/pkg   0.012s
prove: unit pass (exit 0), signed by alice@example.com, record 82bc15438e89 in the store adapter prove-store-dir
$ ls /srv/prove-records
82bc1543...2622.meta  82bc1543...2622.record
$ prove verify
prove verify 2e223e3 (tree 3149d64)
  ok   unit  signed by alice@example.com, new
PASS
```

You can drive it by hand the same way prove does:

```console
$ echo '{"op":"list","config":{"dir":"/srv/prove-records"},"query":{}}' | prove-store-dir
{"records": [{"id": "82bc...", "check": "unit", ...}]}
```

A real store (S3, GCS, an HTTP service) has the same three operations; only
the storage calls change.

## Testing a store: the conformance suite

[`storetest`](../storetest/storetest.go) drives a store adapter over this
protocol exactly as prove does. It fails a store whose bytes don't
round-trip, whose meta changes, whose repeated put fails or lists twice,
whose get of a missing record doesn't fail, whose rerun replaces an
earlier record of the same check, or whose list with a query (by check,
commit, tree, signer or fingerprint) drops a matching record. A store may
ignore the query and return everything, since prove filters the answer
again. Run it from a Go test,
whatever language the adapter is in:

```go
import "github.com/jayehmke/prove/storetest"

func TestMyStore(t *testing.T) {
	// prove-store-mine must be on PATH.
	storetest.Run(t, "mine", func(t *testing.T) map[string]any {
		return map[string]any{"dir": t.TempDir()} // options for an empty store
	})
}
```

Every case gets a fresh, empty store from the function you pass.
`prove-store-dir` above and `prove-store-s3` both pass it.

## S3

`prove-store-s3` keeps records in an S3 bucket, or any S3-compatible
server. Install it from a release (it is in the same archive as `prove`)
or with
`go install github.com/jayehmke/prove/cmd/prove-store-s3@latest`.

```yaml
store:
  type: s3
  options:
    bucket: my-prove-records   # required
    prefix: prove              # default "prove"
    region: us-east-1          # default: from the AWS environment
    # endpoint: https://minio.example.com   # an S3-compatible server
    # path_style: true                       # most of those need it
```

Each record is `<prefix>/<commit>/<signer>/<check>.json`, the record bytes
exactly as signed, and `<prefix>/index/<id>.json` points to it with its
meta. Every write is conditional (`If-None-Match: *`), so the adapter never
replaces an object: a rerun of the same check by the same signer on the
same commit goes to `<check>.2.json`, then `<check>.3.json`. Turn on
versioning on the bucket too, so nothing else can destroy a record either.

Credentials come from the standard AWS chain: environment variables,
shared config and SSO profiles, web identity (OIDC from CI), and instance
or task roles. The adapter needs `s3:PutObject`, `s3:GetObject` and
`s3:ListBucket` on the bucket and prefix, and nothing else. For GitHub
Actions, give a role with that policy to the workflow through OIDC
(`aws-actions/configure-aws-credentials`) instead of storing a key.

`list` reads every index entry, so it costs one request per record plus
one per thousand records; keep one prefix per repository.

## GitHub

`prove-store-github` keeps each record as a comment on the GitHub commit it
was made for, so the record shows on that commit and on any pull request
that contains it, and sharing a record makes no git ref. Install it with
prove's release archives or
`go install github.com/jayehmke/prove/cmd/prove-store-github@latest`.

It is meant for copying, not as a repository's main store:

```sh
prove push --to github            # after the commit is on GitHub
prove fetch --from github $(git rev-list origin/main..HEAD)   # in CI, then prove verify
```

A comment opens with `<!-- prove-record v1 -->` and holds the record's meta
and its bytes (base64) in a ```` ```prove-record ```` block. Put skips a
commit that already has the record. Get and list skip any comment whose
bytes don't hash to its id, and anyone who can comment can add only records
that `verify` then checks like any other: a store can make a check fail,
never pass.

Options, all optional: `repo` (`owner/name`; default `$GITHUB_REPOSITORY`,
else the `origin` remote), `api` (default `$GITHUB_API_URL`, else
`https://api.github.com`) and `commit` (where get looks first; `prove fetch`
sets it). The token is `$GH_TOKEN` or `$GITHUB_TOKEN`, else `gh auth token`;
it needs `contents: write` to comment (`contents: read` to fetch). List
without a commit reads every commit comment in the repository, one request
per hundred.
