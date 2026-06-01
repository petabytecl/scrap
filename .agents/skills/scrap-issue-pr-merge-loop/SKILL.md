---
name: scrap-issue-pr-merge-loop
description: Drives one GitHub issue from selection through implementation, PR, CI, Codex review loops, feedback fixes, merge, post-merge verification, and then repeats for the next issue. Use when the user asks to work issues in sequence, follow the full PR/review/merge flow, wait for CI and Codex reviews, resolve conversations, merge, or continue with the next issue.
---

# Issue PR Merge Loop

Use this skill as the outer orchestration loop. It coordinates issue triage,
implementation, PR publication, CI, Codex review, review-thread fixes, merge,
post-merge verification, and selection of the next issue.

## Quick Start

1. Resolve the current issue from the user request, branch, or tracker.
2. Implement the smallest complete slice for that issue.
3. Open a PR, wait for CI, request Codex review, address feedback, wait again.
4. Resolve all addressed conversations, merge, verify post-merge CI, then repeat.

## Workflow

1. Pick exactly one issue.
   - If the user names an issue, use it.
   - Otherwise inspect the tracker for ready issues in priority/order.
   - Read the issue body, comments, labels, linked PRs, and acceptance criteria.
   - Do not close or skip an issue just because a partial scaffold exists.

2. Re-anchor locally.
   - Fetch and switch to the issue's target base branch.
   - Pull with fast-forward only.
   - Confirm the worktree status before starting.
   - Create or reuse a focused branch for the issue.

3. Implement and verify locally.
   - Use `../scrap-tdd/SKILL.md` when the change is feature or bug work.
   - Keep the diff scoped to the issue acceptance criteria.
   - Run the repo's relevant local gates before publishing.
   - Commit with Conventional Commits; use `../scrap-git-commit/SKILL.md` if needed.

4. Publish the PR.
   - Push with upstream tracking for new branches.
   - Create a PR against the correct base branch.
   - Reference the issue and include a concrete test plan.
   - Use `../scrap-yeet/SKILL.md` when the user asks for the publish flow.

5. Wait for PR CI.
   - Use `gh pr checks <pr> --watch` or equivalent polling.
   - Treat Codecov and required status contexts as first-class checks.
   - If CI fails, inspect logs, fix locally, commit, push, and wait again.
   - Use `../scrap-gh-fix-ci/SKILL.md` for nontrivial GitHub Actions failures.

6. Ask Codex for review.
   - Request a Codex review through the repo-supported GitHub mechanism.
   - Codex must review the current PR head, not an older commit.
   - After every consecutive push, request or wait for a fresh Codex review of the new head.
   - Do not treat an older clean Codex review as valid after new commits land.
   - Record the exact head SHA when the review request is made; merge readiness
     must be judged against that same SHA.

7. Wait for and inspect review feedback.
   - Poll PR reviews, top-level comments, and thread-aware review data.
   - Use GraphQL review threads for `isResolved`, `isOutdated`, path, and line state.
   - Do not treat flat comments or a PR summary as complete thread state.
   - Codex Web may report through several GitHub surfaces:
     - a formal PR review (`APPROVED`, `COMMENTED`, or `CHANGES_REQUESTED`);
     - inline review threads with actionable code comments;
     - a top-level PR comment summarizing findings or saying no issues were found;
     - reactions on the `@codex review` trigger comment; and
     - reactions on the PR description/body itself.
   - Treat an `eyes` reaction as "queued/running/acknowledged", not as a final
     clean review.
   - Treat a positive reaction such as `THUMBS_UP` on the trigger comment or PR
     description/body as a clean Codex result only when the PR head SHA still
     matches the requested SHA and there are no Codex review comments, no
     unresolved review threads, and no failing or pending required checks.
   - Treat `CHANGES_REQUESTED`, a negative reaction, a failure comment, or
     unresolved non-outdated threads as actionable or blocked until resolved.
   - If no final Codex signal appears and only `eyes` remains, keep polling or
     retry the request; do not merge based on CI alone.
   - Use `../scrap-gh-address-comments/SKILL.md` when feedback must be implemented.
   - Continue the Codex review loop until Codex finds no new issues on the latest head.

8. Address every actionable review comment.
   - Separate actionable fixes from summaries, duplicates, and stale comments.
   - Implement needed changes, add or update tests, run focused checks, commit, push.
   - Wait for PR CI, Codecov, and a fresh Codex review after every pushed review-fix commit.
   - Resolve conversations only after the fix is pushed, verified, and reviewed.
   - If feedback is invalid or risky, explain why and leave a clear PR response.
   - Keep iterating until every review item is implemented, justified, obsolete, or resolved.

9. Merge only when ready.
   - Confirm PR merge state is clean, required checks are green, Codex has
     returned a final clean signal for the latest head, and conversations are
     resolved.
   - Match the repo's existing merge style unless the user specifies otherwise.
   - Use a head-SHA guard when merging to avoid merging stale commits.
   - Delete the feature branch when the repo workflow allows it.

10. Verify after merge.
    - Confirm the PR state is merged and identify the merge commit.
    - Switch to or update the base branch to the merge commit.
    - Wait for post-merge CI and security scans, including CodeQL when configured.
    - Close or update the issue only after acceptance criteria are actually satisfied.

11. Start the next issue.
    - Refresh the tracker and base branch before choosing the next issue.
    - Report if no ready issue remains or if the next issue needs user signoff.
    - Otherwise repeat the loop from step 1.

## Stop Conditions

- Stop for user input only when the next decision cannot be inferred safely.
- Stop if credentials, branch protection, or missing permissions block progress.
- Stop if review feedback conflicts with the issue acceptance criteria or ADRs.
- Do not stop merely because a PR is open, CI is running, or review is pending; wait.
