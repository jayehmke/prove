# Changelog

All notable changes to prove are recorded here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and prove follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing is released yet. This is what is on `main` today.

### Added

- `prove run` runs a check (from `.prove.yaml`, or ad hoc with
  `--name <n> -- <command>`), signs a record of the result and stores it.
  A record is an in-toto Statement in a DSSE envelope about the git tree
  and commit the check ran on. It refuses a dirty working tree, and stores
  nothing when the command changes the working tree or moves HEAD.
- `prove verify` checks that every check in the policy has a valid record:
  the signature, a trusted signer allowed to sign that check, the same
  command, unchanged inputs, a passing run, and the requirement IDs the
  policy requires. It gives one reason per failure. `--sample <pct>` reruns
  that percentage of checks and fails when a rerun disagrees with its
  record.
- Records are reused across commits while a check's inputs are unchanged,
  using a fingerprint over the command, the input files and lockfiles.
- `prove show --format md|json` shows what is recorded for a commit.
- `prove plan` says, per check, whether its record is accepted or the check
  must run, and why.
- `prove run --stale` runs only the checks without a valid record, and
  `prove run --commit <rev>` runs on any commit in a temporary worktree.
- `prove push` pushes the records a verifier of a commit needs, optionally
  as branches (`--as-branch`) for hosts that accept only branch pushes.
- `--onto <ref>` judges a commit as if merged into `ref`, the way a pull
  request's CI tests it, so two branches that pass alone but break
  together fail before either lands.
- Signing with SSH keys (SSHSIG, checkable with `ssh-keygen -Y verify`),
  or with an external `prove-signer-<name>` adapter.
- Stores: git refs (the default), a local folder, S3 (`prove-store-s3`),
  or an external `prove-store-<name>` adapter. A conformance suite
  (`storetest`) checks new store adapters.
- Per-test results from JUnit reports, and requirement IDs a policy can
  require a passing test for.
- A GitHub action that verifies a commit and posts what is recorded as the
  job summary. In `mode: run` it first runs the checks with no valid
  record. It installs only a pinned prove release whose archive matches
  its checksum and its build attestation.
- An example git `pre-push` hook that makes and pushes records on
  `git push`.
- A release workflow that builds static binaries for Linux and macOS
  (amd64 and arm64), with checksums and build attestations.

[Unreleased]: https://github.com/jayehmke/prove/commits/main
