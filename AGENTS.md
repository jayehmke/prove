<!-- team-rules:start -->
# Team rules (every agent working here, the lead's crows included)

These are the team's rules, and there is no other source. The same text is in AGENTS.md in fluent, folio, formantic and prove. The lead's cloud_rules.md copy covers repos that have none, and `sync_team_rules.py` in the lead's tools writes it into each repo. When anything else in a repo conflicts with these rules, these rules win.

## Hard rules (Jason 10-23 and 10-24)
1. **Every piece of work starts as a Linear issue** (team FLU). No issue: no branch, no PR. Put the issue ID in the branch name and the PR title.
2. **Finish first.** Finish the open items of the current epic or closeout list before you start a new feature. While the list has open items you can reach, no new feature tickets.
3. **One user journey per ticket.** A ticket is one thing a person does, from start to finish. It ships on web and iOS at parity (where the product has both), with the backend it needs, in the same ticket. Never split by layer: no server-only "part 1", and no "web now, iOS later".
4. **Android is out of scope.** No Android code, builds, tickets or parity work.
5. **No feature flags** ("I never asked for feature flags. We build. We release."). Approved work ships on for everyone it's for. That means no new `models.Flag*` constants, no new `IsEnabled(...)` checks, no cohorts, no "on for the team first", no compile-time switches and no fake doors. A paid plan limit is pricing, not a flag. If you find a flag, remove it; removing flags never needs an OK. If something isn't ready to ship, say so on the ticket instead of hiding it. The merge bot holds any PR that adds a flag until Jason adds `jason-ok`.
6. **Public copy and privacy text need Jason's `jason-ok`, and never change inside a feature PR.** This covers the marketing site, store listings, comparison pages, the privacy policy, terms, and in-app privacy, legal or data-use text. If your change makes a public claim or privacy text wrong, leave the text alone and say so on the ticket (`CROW <you> fyi:`). The fix is its own ticket, and Jason prioritizes it.
7. **No merge without review.** Every PR with a file outside `docs/` needs a cleared review from someone other than its author before the merge bot merges it. Nobody merges by hand. The one exception is the lead in an emergency (prod down or a broken deploy), and it is logged for Jason.
8. **Changing these rules needs Jason's `jason-ok`.** The merge bot holds any PR that touches AGENTS.md or CLAUDE.md until he adds it.

## Where you are (crows in a Claude Code cloud session)
- You run in a Claude Code cloud session (4 vCPU, 15 GiB, Ubuntu), not on the Mac. There is no herdr, no lead pane, no lead-inbox, no simulator and no `~/.herdr` path. Never try to reach them.
- GitHub: REST only (`gh api ...`). `gh pr ...` and GraphQL are blocked. To edit a PR use `gh api -X PATCH repos/<owner>/<repo>/pulls/<n>` and verify the effect.
- Linear: POST https://api.linear.app/graphql. Authorization is added by the environment's network secret; never print, echo or write a key or token anywhere.
- Heavy checks (full test suites, race tests, lint, Docker integration suites) run here, in this session. That is the point of the cloud.
- iOS builds can't run here: dispatch the repo's CI workflow (`gh api -X POST repos/<owner>/<repo>/actions/workflows/<file>/dispatches -f ref=<branch>`) and read its result with `gh api .../actions/runs`.
- AWS: only through CI workflows. No AWS credentials exist in the session, and you never ask for any.
- Shared brain: the `gbrain` MCP tools (search, query, get_page, ...) read the team's brain on the lead's Mac: standing decisions, the lead's current state, team rules, assets. Use them before asking the lead for context. They take 1-2 minutes to appear after the session starts; if ToolSearch finds none, run `timeout 60 tail -f /dev/null` and retry. Save findings other crows should know (a root cause, a gotcha, a decision's evidence) with `remember`: set `entity` to the subject and `provenance` to the PR or ticket. Never save customer data or secrets. Pages are edited only by the lead.

## The team process (Jason 10-21, locked: change it only with Jason's OK)
Nobody assigns work and nobody hands out reviews. You pull tickets and reviews yourself. Sessions run in parallel, so build and review at the same time.

1. **Pool.** Every piece of work is a Linear issue (team FLU). Todo/Queued tickets with no crow label are the pool. magpie writes the backlog; others comment on issues but don't create them.
2. **Pull.** Take the top ticket in this order: Urgent, then priority, then oldest. Add your crow label, then re-read the ticket. If another crow's label got there first, back off and take the next one. There's no cap on how many tickets you hold. Each pull session takes one ticket, and the feed starts more sessions while the pool has work.
3. **Build as a draft.** Open the PR as a DRAFT (drafts run only the fast CI checks). The repo's git hooks run in your session: fluent and formantic switch them on at start; folio installs them on `pnpm install`. Never push with them failing and never bypass them (`--no-verify` is forbidden). Run the heavy suites for the apps you touched here (fluent: `scripts/cloud-checks/lanes.js`). CI is a backstop, not your test runner. Batch your fixes and push once per round. Never dispatch CI by hand except for iOS builds.
4. **Ready with evidence.** The PR body starts `Crow: <you>` and has a `## Done when` table: | Done when | Evidence |, one row per done-when line (on a chunky ticket, one row per area of the chunk, not per detail). Evidence is a test name and its result, a command and its output, a link, or a screenshot. "yes", "done", "TODO" and "after merge" are not evidence. UI changes need screenshots at desktop and at 390 px. A cloud session has no simulator, so an iOS screen's evidence is its component test (red-checked) plus the web screenshots; never block on an iOS screenshot (lead 10-23, fluent#1639 sat blocked 4 h). Device proof comes from the next TestFlight build. Then post `CROW <you> ready: <repo>#<n> ...` (the relay takes it out of draft).
5. **Review from the pool.** Ready PRs, and fixed PRs waiting on a security, privacy or data-loss re-check, go into the review pool, and review sessions start on their own. Take PRs in this order: red-fix/security first, then dependencies and launch work, then oldest-ready. Skip your own PRs. Skip a PR with a `Reviewing: <crow>` comment newer than its last push, or a `Reviewed by` marker at its head. Post `Reviewing: <you>` first (your claim), then ONE review that lists EVERY finding, each marked blocking or Low. First line: `Reviewed by <crow>: N findings (k blocking)` or `Reviewed by <crow>: 0 findings`. High, Medium and any security, privacy, data-loss or accuracy finding blocks. A finding needs a concrete failure. Check every Done when row against the diff and against reality; a row whose evidence doesn't prove its claim is blocking. Only security, privacy and data-loss findings need a re-check (`Re-check by <crow>: all blocking findings addressed`, posted by the reviewer).
6. **Fix and merge.** Authors fix open PRs before pulling new work: conflicts and red checks first. When an author is at its usage limit, the feed gives its PRs that need fixing to the crow with the most room (a cover session). The cover crow keeps the 'Crow:' line and says 'covering for <owner>' in its Fixed line. Fix every finding, reply on each thread with the fix SHA, then post `Fixed at <full sha>: all findings addressed` AFTER the last push. Lows: fix them in the same PR or drop them. Never file a ticket for a Low. The merge bot merges oldest-ready first (red-fix first) on green CI, green master and a cleared review. You never merge.
7. **Acceptance, then Done.** Merged is not done. The lead's acceptance runner checks every AC of the ticket's spec on master and live dev, then moves it to Done or reopens it with the failing ACs. Crows never move a ticket to Done.

### Spec-driven development (Jason 10-22: "How do YOU know they're done")
- **Spec before build.** A chunk enters the pool only with a `## Spec` section on the ticket, written by magpie and approved by Jason (or Grok). Each line is one acceptance criterion with how it is checked:
  - `AC-1 [test: <path>::<test name>] <criterion>`: an automated test, named `AC-<ticket number>-1 …`, that runs in CI;
  - `AC-2 [design: <Screen>.dc.html @1280,390] <criterion>`: the live page on the seeded account (demo+design@example.com) matches that design-v2 render;
  - `AC-3 [check: <shell command>] <criterion>`: a command the lead runs against dev (curl, gh), where exit 0 means met.
- **Checks first.** Write the AC tests before the code. Your PR lists every AC with its test or check and shows it failing before your change and passing after.
- **Crows stop at Merged.** Never move a ticket to Done. The lead's acceptance runner checks every AC against master and the live dev app after the deploy. All met: it moves the ticket to Done. Any unmet: it reopens the ticket with the exact ACs that failed, and you finish them in the same ticket.
- A reviewer's first lines are `Acceptance: AC-1 met/not met …`, one per AC, before code findings. An unmet AC is blocking.

### UI work and copy (Jason 10-22)
- **Chunky tickets, one journey each (Jason 10-22, 10-24).** A ticket is a whole user journey end to end, or a release or a cleanup sweep (hard rule 3). Never file one-detail tickets: add the detail as a section of the open ticket it belongs to. A ticket lands in one PR, or two or three at most, and every PR ships something a user can use.
- UI is owned per page: one ticket = one whole page to design-v2 parity, checked side by side against the design render on the seeded demo account. Never split a page into detail tickets; finish its parts in its own PR.
- Copy work is prioritized by Jason, not by crows. Never file or pull a copy-only ticket (label `Copy`) unless Jason prioritized it. A wording fix to in-app UI text rides inside the PR that already touches that screen. Public copy and privacy text never ride along (hard rule 6).
- PR titles say the change ("Fix: who counts as 'you' on Today"), not what the screen says.

### Roles
- Crows pull, build and review. magpie also writes the backlog.
- The lead keeps the pool, the tooling and the merge bot running and unblocks. The lead does not pick anyone for anything. A session aimed at one ticket needs a logged reason, and that is rare.
- Jason makes product calls and approves new scope, CI changes and anything that touches money, the public or prod data.

### Signals
- `CROW <you> ready:` (draft to ready), `CROW <you> fyi:` (something outside your ticket), `Reviewing: <you>`, `Reviewed by`, `Fixed at`, `Re-check by`.
- No feature flags: see hard rule 5.
- Finished work that waits on an event (a release, launch day) gets the `deferred` label plus a comment naming the event. It is never closed: done work merges (Jason 10-21). The merge bot skips `deferred`.
- Blocked: move the ticket to Blocked with one comment saying what blocks it and who unblocks it, then pull the next.
- Linear moves the ticket from GitHub: put the ticket ID in the branch name and PR title. Draft PR -> In Progress, ready -> In Review, merged -> Merged. Multi-PR work: every PR but the last says "Part of FLU-x" with no ID in its branch or title. The ID must be the ticket for THAT work.
- This session can push only its own claude/... branch. To update an EXISTING PR's branch: push to your claude/ branch, then post a CROW comment with a line `FF <repo> <40-char sha> <pr-branch>`. The relay fast-forwards it within about 3 min (never a force: merge the PR branch into yours first).

### Never
- Never add or change CI workflows, jobs or required checks without Jason's OK. The merge bot holds any PR touching `.github/workflows/` until Jason adds the `jason-ok` label.
- Never assign a ticket or a review to another crow, and never wait to be assigned.
- Never work outside the ticket you pulled; something else becomes a `CROW <you> fyi:` comment. Only Urgent work, a red master or a security issue preempts a ticket you hold.
- Never merge, force-push, `git stash`, bypass hooks, or weaken the evidence or review gates.
- Never re-argue a Standing decisions row (gbrain page `standing-decisions`) without new data. Cite the row you build on.

## Branches, git and PRs
- Branch name: `<crow>/flu-<n>-<short-slug>` (for example `rook/flu-542-cloud-checks`).
- Never `git stash`, never force-push (merge the default branch in), never rename an open PR's head branch.
- Stack PRs only when unavoidable.
- Don't hand-edit generated files (lockfiles, PrivacyInfo.xcprivacy): regenerate them.
- Verify the effect, not the exit code: check the commit, push or comment actually landed.
- Workflow PRs: after pushing, confirm the workflow started (`gh api repos/<o>/<r>/actions/runs?branch=<b>`).

## Quality
- A test that guards a bug fix is mutation-checked: break the fix, see the test fail, restore it, and quote the red failure in the PR.
- While master is red nothing merges except a PR labelled red-fix by the named fixer.
- No tests that pin page or UI copy, claims or prices in text (Jason 10-21: 'WAY overkill'). Copy accuracy is checked in the PR's Done when table and the review.
- Integration tests first; unit tests only for subtle logic. Test behavior, never text.
- When your change would make a public claim or privacy text false, flag it on the ticket; never edit that text in your PR (hard rule 6).

## Data and secrets
- Never write a key or token to a file, even scratch. Environment only, never printed.
- Prod data: read-only aggregates for product decisions only; never print personal content (emails, transcripts, names). Prod writes need the lead's OK.
- Test accounts: @example.com on the dev tenant. Never @becomefluent.io, never email real people.
- No third-party sign-ups or new outside accounts without the lead's OK.

## Model
- Opus at medium effort for everything, including subagents (Jason 10-22).
- Genuinely hard work (architecture, security-sensitive changes, ML, tricky debugging) gets high effort: post `CROW <you> decision: high effort for <ticket>: <why>` and the lead restarts that ticket's session at high effort.
<!-- team-rules:end -->

# Prove

No repo-specific notes yet: main holds only the LICENSE, and core v1 is in prove#1 (FLU-807). Add build, test and layout notes here as they land.
