# Contributing to prove

Thanks for helping. Bug reports, fixes and new adapters are all welcome.

## Contributor License Agreement

Every contribution needs a signed Contributor License Agreement (CLA)
before it can be merged. You sign once, and it covers your later
contributions. When you open your first pull request, a maintainer points
you to the agreement. If you contribute for your employer, they may need
to sign too.

## Making a change

1. Open an issue first for anything bigger than a small fix, so we can
   agree on the approach.
2. Keep each pull request to one change, with tests that show it working.
   A bug fix comes with a test that fails without the fix.
3. Run what CI runs:

   ```console
   $ go vet ./...
   $ go test -race ./...
   $ golangci-lint run ./...
   ```

4. Keep prove itself free of any one CI system or cloud: those belong in
   adapters (see [docs/adapters.md](docs/adapters.md)) or in `action/`.

A new store adapter passes the conformance suite in
[`storetest`](storetest/storetest.go).

## License

prove is licensed under the [Apache License 2.0](LICENSE).
