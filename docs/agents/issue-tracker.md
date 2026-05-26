# Issue tracker: GitHub

Issues and PRDs for this repo live as GitHub issues. Use the `gh` CLI for all operations.

Current Phase 1 spike-store PRD scope is split across issues #250-#256. Those issue
bodies are the task-level requirements source for the Phase 1 contract boundary.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..." --milestone "storage-gateway-v2"`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <number> --comment "..."`

Infer the repo from `git remote -v` — `gh` does this automatically when run inside a clone.

## Required labels and milestone

All issues related to the V2 implementation **must** include the `v2` label and the
`storage-gateway-v2` milestone. Add `--label "v2" --milestone "storage-gateway-v2"`
to every `gh issue create` command.

## When a skill says "publish to the issue tracker"

Create a GitHub issue with the `v2` label and `storage-gateway-v2` milestone.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
