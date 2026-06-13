---
title: BMAD Migration Plan
status: draft
created: 2026-06-07
updated: 2026-06-07
project: scrap
source_of_truth: planning-migration
---

# BMAD Migration Plan

## Purpose

Move the existing S.C.R.A.P. V2 planning system into BMAD's artifact flow without
losing the useful GitHub issue history, ADR trail, or phase-slice structure that
already exists.

BMAD should become the planning workspace. GitHub Issues should remain the
published execution tracker.

## Current Inventory

### Installed BMAD Configuration

- Config: `_bmad/bmm/config.yaml`
- Planning artifact target: `_bmad-output/planning-artifacts`
- Implementation artifact target: `_bmad-output/implementation-artifacts`
- Project knowledge target: `docs`
- Project name: `scrap`
- Communication and document language: English

### Existing Planning Sources

- Domain vocabulary and project constraints: `CONTEXT.md`
- ADRs: `docs/adr/0001-*.md` through `docs/adr/0020-*.md`
- Current phase slice docs:
  - `docs/phase-4-eviction-implementation-slices.md`
  - `docs/phase-4.5-security-implementation-slices.md`
- PRD closure evidence policy: `docs/prd-closure-policy.md`
- Issue tracker conventions: `docs/agents/issue-tracker.md`
- Triage labels: `docs/agents/triage-labels.md`

### Existing GitHub Tracker Shape

The current open lane is Phase 4.5:

- Parent PRD issue: `#398` - Phase 4.5 production security and encryption bridge
- ADR issues: `#399`, `#400`
- Implementation slice issues: `#401` through `#408`
- Required label/milestone convention: `v2` label and `storage-gateway-v2`
  milestone for V2 work.

## Target BMAD Shape

### Stable Project Context

BMAD skills are configured to load `**/project-context.md`, but no such file
exists yet. Generate it through `bmad-generate-project-context` before relying
on BMAD implementation workflows.

Expected output:

- `_bmad-output/project-context.md`

This should be a concise implementation memory, not a second specification. It
should point agents to `CONTEXT.md`, `docs/go-style-guide.md`, ADR rules, GitHub
issue conventions, and the V2 spike boundary.

### Planning Artifacts

Use `_bmad-output/planning-artifacts` for BMAD-native artifacts:

- PRD workspaces from `bmad-prd` under `prds/`
- Architecture output from `bmad-create-architecture`
- Epics and stories output from `bmad-create-epics-and-stories`
- Readiness reports from `bmad-check-implementation-readiness`
- This migration plan as a temporary routing document

### Project Knowledge

Keep durable source documents in `docs/`:

- ADRs stay in `docs/adr/`
- Long-lived engineering standards stay in `docs/`
- Agent operational rules stay in `docs/agents/`
- Research remains under `docs/research/`

Do not move ADRs into `_bmad-output`. BMAD should consume them as architecture
context and produce new architecture summaries or readiness reports as needed.

### GitHub Issues

Keep GitHub Issues as the execution ledger:

- PRD issues remain published tracker records.
- ADR issues remain review/acceptance records for docs in `docs/adr/`.
- Slice issues remain work items for agents.
- BMAD story artifacts should link to the corresponding GitHub issue when a
  published issue already exists.

## Migration Map

| Existing artifact | BMAD role | Action |
| --- | --- | --- |
| `CONTEXT.md` | Domain source of truth | Reference from project context; do not duplicate wholesale |
| `docs/go-style-guide.md` | Implementation standard | Reference from project context |
| `docs/adr/*.md` | Architecture decisions | Keep in place; summarize or reference from architecture artifact |
| `docs/phase-4.5-security-implementation-slices.md` | Existing epics/stories input | Convert into BMAD epics/stories or validate against generated stories |
| Issue `#398` | Published PRD tracker | Convert body into BMAD PRD update/validation input |
| Issues `#399`-`#400` | ADR execution records | Keep as tracker issues linked from architecture artifact |
| Issues `#401`-`#408` | Implementation slices | Treat as existing story candidates |
| `docs/prd-closure-policy.md` | PRD completion rule | Reference from readiness and closure checks |

## Recommended Workflow Order

1. Run `bmad-generate-project-context`.
   - Goal: create `_bmad-output/project-context.md`.
   - Inputs: `CONTEXT.md`, `docs/go-style-guide.md`, `docs/agents/*.md`,
     `docs/adr/*.md`, and current repository structure.

2. Run `bmad-prd` in update or validate mode for Phase 4.5.
   - Goal: turn issue `#398` plus `docs/phase-4.5-security-implementation-slices.md`
     into a BMAD-native PRD workspace under `_bmad-output/planning-artifacts/prds/`.
   - Keep the GitHub issue open until closure evidence is current and linked.

3. Run `bmad-create-architecture` only if Phase 4.5 needs a BMAD architecture
   summary beyond ADR 0019 and ADR 0020.
   - Goal: make implementation-relevant architecture constraints explicit for
     downstream stories.
   - Do not replace accepted ADRs.

4. Run `bmad-create-epics-and-stories`.
   - Goal: convert Phase 4.5 slices into BMAD story artifacts while preserving
     GitHub issue numbers.
   - Existing issues `#401`-`#408` should become the initial story candidates,
     not competing work items.

5. Run `bmad-check-implementation-readiness`.
   - Goal: verify PRD, ADRs/architecture, and stories line up before implementation.
   - Special checks:
     - every security success criterion in `#398` traces to at least one story;
     - every story has testable acceptance criteria;
     - closure evidence expectations from `docs/prd-closure-policy.md` are clear;
     - no story uses forbidden glossary drift from `CONTEXT.md`.

6. Run `bmad-sprint-planning` when implementation should begin.
   - Goal: produce implementation sequencing under `_bmad-output/implementation-artifacts`.
   - GitHub Issues remain the external tracker.

## Migration Rules

- Preserve GitHub issue numbers in BMAD artifacts whenever a story maps to an
  existing issue.
- Keep `Document`, `Transaction`, `Block`, `Frame`, `Shard`, `Cell`, `Member`,
  `Backend`, and other `CONTEXT.md` terms exact.
- Do not convert ADRs into stories. ADRs are architectural authority; stories
  implement consequences.
- Do not close PRD tracker issues until required evidence links are attached.
- Treat `_bmad-output` as generated planning/implementation workspace, not
  canonical domain documentation.

## Immediate Next Step

Use `bmad-generate-project-context` to create the missing project context, then
use `bmad-prd` to validate or update the Phase 4.5 PRD from issue `#398`.
