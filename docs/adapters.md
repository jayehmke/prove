# Adapters

Storage and signing are adapters, so prove works with whatever your team
already uses. Built in are the `local` store (a folder), the `git` store (refs
under `refs/prove/`) and the `ssh` signer. Anything else is an external
program on `PATH`:

- `prove-store-<name>`: a store, selected with `store: {type: <name>}`
- `prove-signer-<name>`: a signer, selected with `prove run --signer <name>`
  (or `PROVE_SIGNER=<name>`) and trusted with `adapter: <name>` in `signers`

Like a git credential helper, prove starts the program once per operation,
writes one JSON request on stdin (one line), and reads one JSON response from
stdout. Every request has `"op"` and `"version": 1`, plus `"options"`: the
store's or signer's settings from `.prove.yaml`.

A response is a JSON object. A non-empty `"error"` field, a non-zero exit, or
output that isn't JSON fails the operation, and prove's message names the
program (and quotes stderr on a non-zero exit). Use stderr for logs, never
stdout.

## Store protocol

Records are stored as entries:

```json
{
  "id": "<sha256 of the envelope, 64 hex>",
  "commit": "<git commit>",
  "tree": "<git tree>",
  "check": "unit",
  "fingerprint": "sha256:...",
  "keyid": "SHA256:...",
  "envelope": { "payloadType": "application/vnd.in-toto+json", "payload": "...", "signatures": [...] }
}
```

Everything except `envelope` is an index for lookups. prove never trusts it:
verify checks the signed envelope only, so a store can't forge a result.

| op | request | response |
|---|---|---|
| `put` | `{"entry": <entry>}` | `{}` — replace any entry with the same `id` |
| `get` | `{"id": "<id>"}` | `{"entry": <entry>}`, or `{"entry": null}` when unknown |
| `list` | `{"filter": {"commit"?, "tree"?, "check"?, "fingerprint"?, "keyid"?}}` | `{"entries": [<entry>...]}` |

A filter field that is absent matches anything. A store may return extra
entries from `list` (prove applies the filter again), but must not leave out
matching ones.

## Signer protocol

| op | request | response |
|---|---|---|
| `sign` | `{"payload": "<base64>"}` | `{"keyid": "<key id>", "sig": "<base64>"}` |
| `verify` | `{"key": "<key>", "keyid": "<key id>", "payload": "<base64>", "sig": "<base64>"}` | `{"ok": true}` or `{"ok": false}` |

`payload` is the DSSE pre-authentication encoding of the record. For an
external signer, the `key` in `signers` is the key ID the adapter reports, so
a policy trusts it like this:

```yaml
signers:
  - identity: ci
    adapter: kms
    key: arn:aws:kms:us-east-1:111122223333:key/abcd
```

prove calls `verify` only for a signature whose `keyid` matches a trusted
signer's `key`, so the adapter answers one question: was this signature over
this payload made by this key.

## Worked example: a store in a shared folder

This store keeps entries as files in a directory given by its options, which
could be a mounted network share. It is a complete adapter; save it as
`prove-store-share` somewhere on `PATH` and make it executable.

```python
#!/usr/bin/env python3
import json, os, sys

req = json.loads(sys.stdin.readline())
root = req["options"]["dir"]
os.makedirs(root, exist_ok=True)

def entries():
    for name in sorted(os.listdir(root)):
        if name.endswith(".json"):
            with open(os.path.join(root, name)) as f:
                yield json.load(f)

op = req["op"]
if op == "put":
    entry = req["entry"]
    with open(os.path.join(root, entry["id"] + ".json"), "w") as f:
        json.dump(entry, f)
    print("{}")
elif op == "get":
    path = os.path.join(root, os.path.basename(req["id"]) + ".json")
    entry = json.load(open(path)) if os.path.exists(path) else None
    print(json.dumps({"entry": entry}))
elif op == "list":
    flt = req.get("filter") or {}
    found = [e for e in entries() if all(e.get(k) == v for k, v in flt.items())]
    print(json.dumps({"entries": found}))
else:
    print(json.dumps({"error": "unknown op " + op}))
```

Use it from `.prove.yaml`:

```yaml
store:
  type: share
  dir: /mnt/team/prove-records
```

`prove run` now sends a `put` like this one (shortened) on the adapter's stdin:

```json
{"op":"put","version":1,"options":{"dir":"/mnt/team/prove-records"},"entry":{"id":"9f2c...","commit":"4be1...","tree":"77a0...","check":"unit","fingerprint":"sha256:5d1e...","keyid":"SHA256:Qm9...","envelope":{...}}}
```

and `prove verify` sends `list` requests filtered by check name, then judges
each returned envelope itself.
