# Issue tracker: GitHub

Issues and PRDs for this repo live as GitHub issues. Use the `gh` CLI for all operations.

Infer the repo from `git remote -v` -- `gh` does this automatically when run inside a clone.

## Version labeling

This branch tracks **v1**. Every issue created must include the `v1` label
**and** the `storage-gateway-v1` milestone.
A v2 effort exists on a separate branch — do not apply `v2` labels or the
`storage-gateway-v2` milestone from this branch.

## Conventions

- Create an issue: `gh issue create --title "..." --body "..." --label "v1" --milestone "storage-gateway-v1"`
- Read an issue: `gh issue view <number> --comments`
- List issues: `gh issue list --state open --json number,title,body,labels,comments,milestone`
- Comment on an issue: `gh issue comment <number> --body "..."`
- Apply/remove labels: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- Set milestone: `gh issue edit <number> --milestone "storage-gateway-v1"`
- Close: `gh issue close <number> --comment "..."`

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
