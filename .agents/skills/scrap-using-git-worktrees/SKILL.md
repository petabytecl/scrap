---
name: scrap-using-git-worktrees
description: Use when starting feature work that needs isolation from current workspace or before executing implementation plans - creates an isolated git worktree under .worktrees/
---

# Using Git Worktrees

## Overview

Ensure work happens in an isolated workspace using `git worktree`. All worktrees live under `.worktrees/` at the repository root.

**Core principle:** Detect existing isolation first. Then create a worktree if needed. Always validate `.gitignore` before creating.

## Step 1: Detect Existing Isolation

**Before creating anything, check if you are already in an isolated workspace.**

```bash
GIT_DIR=$(cd "$(git rev-parse --git-dir)" 2>/dev/null && pwd -P)
GIT_COMMON=$(cd "$(git rev-parse --git-common-dir)" 2>/dev/null && pwd -P)
BRANCH=$(git branch --show-current)
```

**Submodule guard:** `GIT_DIR != GIT_COMMON` is also true inside git submodules. Verify you are not in a submodule:

```bash
git rev-parse --show-superproject-working-tree 2>/dev/null
```

**If `GIT_DIR != GIT_COMMON` (and not a submodule):** You are already in a linked worktree. Skip to Step 4 (Project Setup). Do NOT create another worktree.

**If `GIT_DIR == GIT_COMMON` (or in a submodule):** You are in a normal repo checkout. Proceed to Step 2.

## Step 2: Ensure `.worktrees/` Is Gitignored

**MUST verify before creating any worktree:**

```bash
git check-ignore -q .worktrees 2>/dev/null
```

**If NOT ignored:** Add `.worktrees/` to `.gitignore` and commit the change before proceeding.

```bash
echo ".worktrees/" >> .gitignore
git add .gitignore
git commit -m "chore: add .worktrees/ to gitignore"
```

**Why critical:** Prevents accidentally committing worktree contents to repository.

## Step 3: Create the Worktree

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
BRANCH_NAME="feature/your-branch-name"

git worktree add "$REPO_ROOT/.worktrees/$BRANCH_NAME" -b "$BRANCH_NAME"
cd "$REPO_ROOT/.worktrees/$BRANCH_NAME"
```

If a branch already exists (no `-b`):

```bash
git worktree add "$REPO_ROOT/.worktrees/$BRANCH_NAME" "$BRANCH_NAME"
```

## Step 4: Project Setup

Auto-detect and run appropriate setup:

```bash
# Node.js
[ -f package.json ] && npm install

# Rust
[ -f Cargo.toml ] && cargo build

# Python
[ -f requirements.txt ] && pip install -r requirements.txt
[ -f pyproject.toml ] && pip install -e .

# Go
[ -f go.mod ] && go mod download
```

Skip if no recognizable project manifest exists.

## Step 5: Verify Clean Baseline

Run tests to ensure workspace starts clean:

```bash
# Use project-appropriate command
npm test / cargo test / pytest / go test ./...
```

**If tests fail:** Report failures, ask whether to proceed or investigate.

**If tests pass:** Report ready with path and branch name.

## Cleanup

When done with a worktree:

```bash
cd "$REPO_ROOT"
git worktree remove .worktrees/$BRANCH_NAME
```

## Quick Reference

| Situation                  | Action                              |
| -------------------------- | ----------------------------------- |
| Already in linked worktree | Skip creation (Step 1)              |
| In a submodule             | Treat as normal repo                |
| `.worktrees/` not ignored  | Add to .gitignore + commit first    |
| Branch already exists      | Use `git worktree add` without `-b` |
| Tests fail during baseline | Report + ask before proceeding      |
| No package manifest        | Skip dependency install             |

## Common Mistakes

### Skipping detection

- **Problem:** Creating a nested worktree inside an existing one
- **Fix:** Always run Step 1 before creating anything

### Skipping ignore verification

- **Problem:** Worktree contents get tracked, pollute git status
- **Fix:** Always verify `.gitignore` in Step 2 before creating

### Proceeding with failing tests

- **Problem:** Can't distinguish new bugs from pre-existing issues
- **Fix:** Report failures, get explicit permission to proceed
