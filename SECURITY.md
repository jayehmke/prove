# Security policy

prove decides whether a CI job trusts a check it did not rerun, so a flaw in
how it signs or verifies records is a security issue. Please report it
privately.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository:
[Report a vulnerability](https://github.com/jayehmke/prove/security/advisories/new)
(the Security tab, then "Report a vulnerability").

Please don't open a public issue, pull request or discussion about it until
a fix is released.

Helpful details:

- the prove version or commit, and how you installed it
- what an attacker can do (for example, make `prove verify` pass without a
  valid, trusted record)
- steps or a small repository that reproduces it

## Supported versions

No version of prove has been released yet. Until the first release, fixes
land on `main`. After it, fixes go into the latest release.

## What to expect

- We acknowledge your report within 5 working days.
- We tell you whether we confirm it, and keep you updated while we work on
  a fix.
- We publish a GitHub security advisory with the fix, and credit you there
  unless you'd rather not be named.
