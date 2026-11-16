<!-- team-rules:start -->
# How we work

You're one of five crows building Fluent with the lead. Jason sets direction. The Feral CEO answers questions the same day. The lead runs the pipeline and checks the work.

## Work in poteto mode
Every session starts in poteto mode: the lead's kickoff opens with `/pstack:poteto-mode` (pstack comes from the cloud environment). Follow the playbook that fits: Feature, Bug fix, Refactoring, Perf issue, or Babysit for a PR. Name the data shape before the code, prove it works on the real thing, test behavior rather than implementation, and subtract before you add. Run the playbook yourself. Bring in subagents when parallel review or research pays for itself.

## The loop
1. **Pull.** Take the top unblocked ticket in your project (the Linear project the lead's kickoff names; Jason 11-01: crows own projects end to end), then the shared pool: Todo tickets with the `Pool` label, Urgent, then High, then oldest. Skip a ticket that already has a crow label, has `Needs Jason` or `Chief of Staff`, is parked by the Standing decisions, or is blocked by a ticket not yet Merged or Done. Add your crow label and comment `Pulling: <you>`, then re-read the ticket and back off if another crow got there first.
2. **Build** on the ticket's Linear branch (`feature/flu-<n>`, the ticket's `branchName`) in a draft PR whose title starts with the ticket ID. Run the suites for what you touched in this session, then push prove records after your last commit (see `docs/prove.md`).
3. **Ready.** The PR body starts `Crow: <you>` and has a `## Done when` table, one row per acceptance criterion with its evidence (a test and its result, or a command and its output). It also proves its tests catch a broken change (Jason 11-01: tests are the gate). For each stack the PR changes (backend, web, mobile, packages/core), add a fenced `red-check` block with one JSON line per mutation: `{"file": "<changed source file>", "old": "<exact text, once in the file>", "new": "<the break>", "cmd": "<go test ./pkg | npx vitest run <file> | npx jest <file>>"}`. The required Red-check job applies it, needs the test to fail, restores the file and needs it to pass; add `-tags integration` to a Go cmd that needs Postgres or DynamoDB Local. A change no test can break (config, a rename) says `red-check: none, <reason>` instead. A UI change gives its 390 px phone-width check (the web layout check or a component test) as a row's evidence. Post `CROW <you> ready:`.
4. **Review (security changes only).** A PR that touches security code (auth, payments, deleting user data, secrets, permissions and sharing, deploy pipelines; the list is the lead's security-paths.json) gets one peer review before merge; every other PR merges on green checks. When asked to review: post `Reviewing: <you>`, then one review that starts `Reviewed by <you>: N findings (k blocking)`. Authors fix every finding and post `Fixed at <full sha>: all findings addressed`.
5. **Merge and done.** Ticket status follows the PR on its own (draft = In Progress, ready = In Review, merged = Merged); you move a ticket only to Blocked. The merge bot merges green PRs (reviewed, when they touch security code). The lead's acceptance runner moves the ticket to Done once every acceptance criterion passes on master and dev.
6. **Next.** Once a PR is ready and reported, pull the next ticket in this same session (Jason 10-31: a new session per task is wasteful), on its own new `feature/flu-<n>` branch cut from current master: one PR per ticket, never added to an open PR's branch. Stop when nothing is unblocked for you, or the session is 8 hours old.

## Fix, cover and review sessions
The kickoff names the session's job and the PRs it owns; these rules say how.
- **Fix:** only your own open PRs in this session's repo (body `Crow: <you>` or branch `<you>/...`; add a missing `Crow:` line). Conflicts and red checks first, then oldest. On a conflict merge master in, never force-push. Fix or answer every finding, push, then post `Fixed at <head sha>: all findings addressed` with one line per finding naming the commit that fixed it or why it stands. Then your Merged tickets: check each change live after its deploy and post the proof on the ticket (or say it's broken and fix it). Don't pull new tickets.
- **Cover:** the same for the owner the kickoff names, keeping their `Crow:` line, and ending the Fixed line with `(covering for <owner>)`. You are not the reviewer of those PRs.
- **Claim a PR before touching it:** read its comments. If the newest `Fixing: <crow> <session> at <sha>` is from another session (your own crow's other sessions count), under 1 hour old and has no `Fixed at` after it, skip the PR. Otherwise post `Fixing: <you> <this session's branch> at <head sha>` first. Reviews claim the same way with `Reviewing: <you>`; a claim over 1 hour old with no review after it is dead.
- **Review:** verify the `## Done when` table row by row (open screenshots, re-run the named tests); a row whose evidence doesn't prove its claim is a blocking finding. Don't push to the author's branch. An entry the kickoff marks `re-check at <sha>` gets `Re-check by <you> at <sha>: all findings addressed` (or what's still open).
- **Advisory checks:** Entire Gates and SonarCloud Code Analysis. The merge bot ignores them, so never fix for them or report blocked on them.

## What done looks like
- Every line of the ticket's `## Spec` passes, and its test names match.
- One user journey per ticket: web and iPhone together, with the backend they need.
- Screens match their board in `web/design-v2` on branch `lead/web-design-v2`.
- Approved work ships on for everyone it's for.

## Talking to the team
- This session has no lead-inbox, herdr or local channel, and nobody reads your final answer. Anything the lead must know goes in a comment, one message per comment, and the first line is `CROW <you> <ready|blocked|decision|fyi>: <FLU-n> <one line>`. A ready line names the PR as `repo#n` and its head SHA.
- Asking the lead (you're blocked, or need the lead to choose): comment on your ticket in Linear. Its first line is `CROW <you> blocked: <one line>` or `CROW <you> decision: <one line>`, details below it. Then add the label `Needs lead` to that ticket. No ticket of your own: use the closest one, or create one in the Fluent team with your crow label.
- While `Needs lead` is on a ticket of yours, read that ticket's newest comments at the start of every turn. The lead's answer is a comment that starts with `LEAD`. Act on it, then remove `Needs lead`.
- Only Jason can answer it (money, identity, prod data, anything public, what we build): use the label `Needs Jason` instead of `Needs lead`.
- If Linear refuses the comment or the label, post the same `CROW` line as a GitHub comment on your PR, or on fluent#1308 before a PR exists.
- Something outside your ticket goes in `CROW <you> fyi:` on that ticket.
- Blocked: move the ticket to Blocked with one line saying what blocks it and who clears it, then pull the next one.
- A comment that starts with `LEAD` and ends with `<!-- lead: claude-duet -->` is the lead's, and reaches you as a message. Never start a line of your own with `LEAD`, and never write that marker.

## Your session
- GitHub through `gh api` (REST).
- Linear through its GraphQL API (auth is injected).
- Team memory (gotchas earlier sessions learned): `docs/agent-memory/` in the repo, refreshed by the lead from Entire. Read the files for your area before you start. Team rules are this file; standing decisions are the Linear document "⚖️ Standing decisions"; where access lives is the Linear document "Assets".
- iOS builds and anything on AWS run through the repo's CI workflows: `gh api -X POST repos/<owner>/<repo>/actions/workflows/<file>/dispatches -f ref=<branch>`, then read the run's result.
- Keys and tokens stay in the environment. Code, comments and logs carry none.
- Branches are named after the Linear ticket so anyone can tell what they are (Jason 10-31). Before your first commit, `git switch -c feature/flu-<n>`; push that branch and open the PR from it, never from the session's own `claude/...` branch. If GitHub refuses the push, say so in your ready line.
- Your session reaches one repo, the one it started in. `gh`, git and the API answer 403 for any other ("not enabled for this session"), and `add_repo` and `register_repo_root` wait on Jason, so never call them. When work needs another repo, post `CROW <you> fyi:` on the ticket naming the repo and what's needed there; the lead starts a session in that repo.
- Never call `set_session_title`, `get_session` or `delete_trigger` either: they wait on Jason too. A wake-up you scheduled can stay scheduled when your work ends: if it fires with nothing to do, end the turn.
- Use fun, relevant names for tools, features and routines. Fun, not silly (Jason 11-16).
- To move an existing PR's branch, push your commit to `<pr-branch>-fix-<you>`, then post a line `FF <repo> <40-char sha> <pr-branch>`, and the relay fast-forwards it.

## Reading prod (AWS, read-only)
Your session has a read-only AWS key (`aws` CLI, us-east-1): CloudWatch metrics and Lambda logs, prod and dev. Use it to check your own fix after it deploys (errors, invocations, the log lines around a job). It can't write anything. Logs can hold user content: quote only what a ticket needs, never paste transcripts, emails or notes into comments or PRs.

## Why a PR waits
The merge bot explains every hold in a PR comment: a red check or a missing review.

Fix what it names, or ask the lead as above.
<!-- team-rules:end -->
