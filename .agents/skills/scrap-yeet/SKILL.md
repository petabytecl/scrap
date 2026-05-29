---
name: scrap-yeet
description: "Publish local changes to GitHub by confirming scope, committing intentionally with Conventional Commits, pushing the branch, and opening a draft PR through the GitHub app from this plugin, with `gh` used only as a fallback where connector coverage is insufficient."
---

# GitHub Publish Changes

## Overview

Use this skill only when the user explicitly wants the full publish flow from the local checkout: branch setup if needed, staging, commit, push, and opening a pull request.

This workflow is hybrid:

- Use local `git` for branch creation, staging, commit, and push.
- Prefer the GitHub app from this plugin for pull request creation after the branch is on the remote.
- Use `gh` as a fallback for current-branch PR discovery, auth checks, or PR creation when the connector path cannot infer the repository or head branch cleanly.

All branches, commits, and PR titles MUST follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. The canonical type list, scope guidance, and breaking-change format live in `../scrap-git-commit/SKILL.md` — defer to that skill for the source of truth and do not redefine types here.

## Prerequisites

- Require GitHub CLI `gh`. Check `gh --version`. If missing, ask the user to install `gh` and stop.
- Require authenticated `gh` session. Run `gh auth status`. If not authenticated, ask the user to run `gh auth login` (and re-run `gh auth status`) before continuing.
- Require a local git repository with a clean understanding of which changes belong in the PR.

## Naming conventions

All three names — branch, commit, PR title — derive from the same Conventional Commits type. They MUST agree.

- **Branch**: `<type>/<kebab-description>` when starting from `main`, `master`, or another default branch.
  - Examples: `feat/add-oidc-login`, `fix/race-in-cache-ttl`, `refactor/extract-storage-gateway`, `chore/bump-go-1.24`, `docs/clarify-onboarding`.
- **Commit**: `<type>(<scope>): <description>` — imperative mood, present tense, ≤72 chars on the summary line. Scope is optional but recommended when the diff is confined to one module. Use `<type>!: ...` or a `BREAKING CHANGE:` footer for breaking changes.
- **PR title**: Same format as the commit: `<type>(<scope>): <description>`. This makes squash-merge history self-describing and lets release-note generators read the title directly.

If the diff genuinely spans multiple types, pick the dominant one and split the rest into follow-up PRs. Use `chore:` only when no other type fits — never as a lazy default.

## Workflow

1. Confirm intended scope.
   - Run `git status -sb` and inspect the diff before staging.
   - If the working tree contains unrelated changes, do not default to `git add -A`. Ask the user which files belong in the PR.
2. Classify the change and choose the Conventional Commits type.
   - Analyze the diff and pick the single type that best describes it. Consult `../scrap-git-commit/SKILL.md` for the canonical type table.
   - If the diff is mixed, ask the user to confirm the dominant type or to split the PR.
3. Determine the branch strategy.
   - If on `main`, `master`, or another default branch, create `<type>/<kebab-description>` from the type chosen in step 2.
   - Otherwise stay on the current branch and verify its name still matches the chosen type. If it does not, flag the mismatch to the user before proceeding.
4. Stage only the intended changes.
   - Prefer explicit file paths when the worktree is mixed.
   - Use `git add -A` only when the user has confirmed the whole worktree belongs in scope.
5. Commit using Conventional Commits format.
   - Summary: `<type>(<scope>): <description>` (≤72 chars).
   - Body: explain the WHY when the change is non-obvious.
   - Footer: add `BREAKING CHANGE: <impact>` or use the `<type>!:` suffix when the change breaks consumers.
6. Run the most relevant checks available if they have not already been run.
   - If checks fail due to missing dependencies or tools, install what is needed and rerun once.
7. Push with tracking: `git push -u origin $(git branch --show-current)`.
8. Open a draft PR.
   - Prefer the GitHub app from this plugin for PR creation after the push succeeds.
   - PR title MUST use the same `<type>(<scope>): <description>` format as the primary commit on the branch.
   - Derive `repository_full_name` from the remote, for example by normalizing `git remote get-url origin` or by using `gh repo view --json nameWithOwner`.
   - Derive `head_branch` from `git branch --show-current`.
   - Derive `base_branch` from the user request when specified; otherwise use the remote default branch, for example via `gh repo view --json defaultBranchRef`.
   - If the branch is being pushed from a fork or the PR target differs from the remote that was just pushed, prefer `gh pr create` fallback because the connector PR creation flow expects one repository target and may not encode cross-repo head semantics cleanly.
   - If connector-based PR creation cannot infer the repository or branch cleanly, fall back to `gh pr create --draft --fill --head $(git branch --show-current) --title "<type>(<scope>): <description>"`.
   - Write the PR body to a temp file with real newlines when using CLI fallback so the markdown renders cleanly.
9. Summarize the result with branch name, commit, PR target, validation, and anything the user still needs to confirm. Call out the chosen type so the user can correct it before merge if needed.

## Write Safety

- Never stage unrelated user changes silently.
- Never push without confirming scope when the worktree is mixed.
- Default to a draft PR unless the user explicitly asks for a ready-for-review PR.
- Never invent a Conventional Commits type that is not in the canonical `scrap-git-commit` list. When the diff does not fit cleanly, ask the user.
- Branch, commit, and PR title types MUST match. If a branch was created before this skill ran with a non-conforming name, surface that to the user before committing.
- If the repository does not appear to be connected to an accessible GitHub remote, stop and explain the blocker before making assumptions.

## PR Body Expectations

The PR description should use real Markdown prose and cover:

- what changed
- why it changed
- the user or developer impact
- the root cause when the PR is a fix
- the checks used to validate it
- any `BREAKING CHANGE:` notes from the commits (repeat them in the PR body so reviewers see them without expanding the commit)
