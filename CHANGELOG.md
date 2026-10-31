# Changelog

All notable changes to prove are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Everything on `main` so far; nothing has been released yet.

### Added

- `prove run` runs a check and stores a signed record of the result: an
  in-toto Statement in a DSSE envelope, signed with your SSH key, naming the
  exact git tree it ran on.
- `prove verify` checks that every check in `.prove.yaml` has a valid,
  trusted and current record, and gives one reason for each one that
  doesn't. A record is reused on a later commit when none of the check's
  inputs changed.
- `prove show` lists what is recorded for a commit, including as a Markdown
  table.
- `.prove.yaml` policy: checks with their inputs and lockfiles, trusted
  signers and which checks each may sign, requirement IDs read from JUnit
  reports, and `--sample` reruns that spot-check records.
- Stores: git (records under `refs/prove/records/`, the default), a local
  folder, and S3 through the `prove-store-s3` adapter, which keys records
  by check and fingerprint rather than commit. Other stores and
  signers plug in as adapters, and `storetest` is a conformance suite for
  new stores.
- `prove run --stale` runs only the checks that have no valid record, and
  `prove run --commit <rev>` runs on a commit in a clean temporary
  worktree.
- `prove push` shares a commit's records, optionally as branches for hosts
  that accept only branch pushes, or with `--share` into the policy's
  `share:` store (an S3 bucket, say) with no git push at all. verify, plan
  and show read the share store along with the store.
- `prove plan` prints, per check, whether its record is accepted or why it
  must run.
- `--onto <ref>` judges a commit merged into another ref, the way a pull
  request's CI tests it.
- An example git `pre-push` hook that makes and pushes records on every
  push.
- A GitHub action that verifies records and posts them as the job summary,
  or runs the missing checks first (`mode: run`). It installs the prove
  release it is pinned to, checked against `checksums.txt` and its build
  attestation, and never a `prove` already on `PATH`.
- Release builds for linux and macOS on amd64 and arm64, with checksums and
  GitHub build attestations.

### Security

- With no key configured, prove names the `~/.ssh` key it falls back to,
  and refuses that fallback when `CI=true`.
- `golang.org/x/crypto` is v0.55.0, which fixes three vulnerabilities the
  signer can reach (GO-2025-4116, GO-2026-5018, GO-2026-5019). prove now
  needs Go 1.25 and builds with Go 1.25.14.

[Unreleased]: https://github.com/jayehmke/prove/commits/main
