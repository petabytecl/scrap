# Issue tracker: GitHub

GitHub issues are the external publication mirror for BMAD-tracked work in this
repo. Use the `gh` CLI for all operations.

Planning source of truth stays in BMAD artifacts:

- PRDs: `_bmad-output/planning-artifacts/prds/`
- Stories, pending/deferred work, and evidence:
  `_bmad-output/implementation-artifacts/`

Query the `storage-gateway-v2` milestone for current open issues:
`gh issue list --state open --milestone "storage-gateway-v2" --json number,title,labels`.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..." --milestone "storage-gateway-v2"`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <number> --comment "..."`

Infer the repo from `git remote -v` — `gh` does this automatically when run inside a clone.

## Required labels and milestone

All issues related to the active SCRAP V2 implementation line **must** include
the `storage-gateway-v2` milestone. Add
`--milestone "storage-gateway-v2"` to every `gh issue create` command.

## When a skill says "publish to the issue tracker"

Create a GitHub issue with the `storage-gateway-v2` milestone.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
