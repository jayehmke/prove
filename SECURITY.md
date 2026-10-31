# Security policy

prove decides whether a check's evidence can be trusted, so a flaw in it
can let unchecked code through. Thanks for reporting one privately.

## Reporting a vulnerability

Please do not open a public issue, pull request or discussion for a
vulnerability.

Report it through GitHub's private vulnerability reporting on this
repository: open the **Security** tab and choose **Report a
vulnerability**, or go straight to
<https://github.com/jayehmke/prove/security/advisories/new>. Only the
maintainers can see the report.

Useful details:

- the prove version or commit, and how you installed it;
- what an attacker can do (for example, get `prove verify` to pass
  without a valid record, or make the GitHub action run a different
  binary);
- the steps or a small repository that shows it.

## What to expect

- We aim to acknowledge your report within a week.
- We confirm the problem, or explain why we don't think it is one, and
  keep you posted on the fix in the private advisory.
- When a fix ships, we publish the advisory, with credit to you unless
  you'd rather stay anonymous.

Please give us a reasonable chance to ship a fix before you disclose
anything publicly.

## Supported versions

prove has no release yet. Until the first one, fixes land on `main`;
build from the latest `main` to get them. Once releases exist, security
fixes go into the latest release.
