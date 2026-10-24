# Adapters

Storage and signing are adapters. prove has three built in: the `local` and
`git` stores and the `ssh` signer. Anything else is an external program on
`PATH`, in the style of git credential helpers:

| Kind   | Program name           | Picked by                                 |
|--------|------------------------|-------------------------------------------|
| store  | `prove-store-<name>`   | `store: {type: <name>}` in `.prove.yaml`  |
| signer | `prove-signer-<name>`  | `PROVE_SIGNER=<name>` when signing; `{type: <name>, key: ...}` under `signers:` when verifying |

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
