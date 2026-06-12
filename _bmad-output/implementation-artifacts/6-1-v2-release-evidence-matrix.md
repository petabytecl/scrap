---
baseline_commit: 1e368285db815bc32c75e8d131018883bade0d5d
---

# Story 6.1: V2 Release Evidence Matrix

Status: review

## Story

As a release owner,
I want a release evidence matrix mapping FRs, ADRs, issues, commands,
artifacts, and closure status,
so that V2 readiness can be audited from current linked evidence.

## Acceptance Criteria

1. **AC-6.1.1 - Release evidence matrix covers all required sources.** Given all V2 FRs and accepted ADR gates, when the matrix is generated, then every FR, accepted ADR gate, story, issue, command, artifact path, and closure status is represented. Evidence records the generated matrix path and source inputs.
2. **AC-6.1.1a - Matrix schema is explicit.** Given matrix columns are defined, when the matrix is reviewed, then it includes FR/ADR/story, evidence command, artifact path, environment, owner, timestamp, commit/ref, pass/fail status, and redaction check columns. Evidence records the matrix schema.
3. **AC-6.1.2 - Evidence rows are current and attributable.** Given an evidence row references an artifact, when the artifact is reviewed, then it includes command, commit/ref, environment, expected result, actual result, timestamp, and redaction proof. Evidence proves stale or local-only evidence is marked explicitly.
4. **AC-6.1.3 - Gaps cannot silently pass.** Given a requirement lacks current evidence, when closure is evaluated, then the matrix marks `FAIL` or `CONCERNS` with owner and mitigation, never silent pass. Evidence records the release-gate decision for each gap.
5. **AC-6.1.4 - Epic 6 stays aggregation-only.** Given matrix generation needs data from feature epics, when the data is missing, then this story records the gap and does not create substitute feature evidence. Evidence proves Epic 6 stayed aggregation-only.

## Tasks / Subtasks

- [x] Create the V2 release evidence matrix artifact. (AC: 1, 2, 5)
  - [x] Add `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`.
  - [x] Record baseline commit, branch, generation timestamp, source inputs, live GitHub issue queries, and final matrix status.
  - [x] State that Story 6.1 is evidence aggregation only and does not implement missing feature, runbook, alert, Tier 2/Tier 3, or S3/IAM evidence.
- [x] Define and apply the matrix schema. (AC: 2, 3)
  - [x] Include columns for requirement type, requirement ID, source document, owning epic/story, GitHub issue/PR, evidence command, artifact path, environment, owner, timestamp, commit/ref, expected result, actual result, redaction proof, freshness decision, release status, and mitigation/next owner.
  - [x] Use only `PASS`, `CONCERNS`, and `FAIL` as release status values.
  - [x] Require `PASS` rows to have current linked evidence, not only story status, commit history, local notes, or merged PRs.
- [x] Populate all V2 FR rows. (AC: 1, 3, 4)
  - [x] Enumerate FR-1 through FR-16 from the master PRD.
  - [x] Link implementation/evidence artifacts for Epics 1 through 5 where they exist.
  - [x] Mark known release-only or not-yet-final gates as `CONCERNS` or `FAIL` with owner and mitigation, including Tier 2, Tier 3, production security rehearsal, real S3/IAM, runbooks, alert/query references, and final closure policy.
- [x] Populate accepted ADR gate rows. (AC: 1, 3, 4)
  - [x] Include every accepted ADR under `docs/adr/`.
  - [x] Give ADR 0025, ADR 0026, and ADR 0027 dedicated rows because they define late release-scope gates for Content Quarantine admin surface, multi-Shard release boundary, and restore-first cold reads.
  - [x] Do not claim an ADR gate is implemented from the ADR alone; link story/evidence artifacts or mark the gap.
- [x] Reconcile GitHub issue state. (AC: 1, 3, 4)
  - [x] Query the `storage-gateway-v2` milestone with `gh issue list --repo petabytecl/scrap --milestone storage-gateway-v2 --state all --json number,title,state,labels,milestone,url,updatedAt --limit 200`.
  - [x] Query issue `#429` directly because it is a required real S3/IAM gate and may not appear in milestone-only results.
  - [x] Record that live issue `#429` is open and currently has no milestone unless the issue tracker is updated before implementation.
- [x] Classify freshness and scope honestly. (AC: 3, 4, 5)
  - [x] Mark LocalStack/test endpoints as interim evidence only.
  - [x] Mark local-only evidence as local unless policy accepts local proof for that claim.
  - [x] Mark missing GitHub Actions/Tier evidence as `CONCERNS` or `FAIL` according to `docs/prd-closure-policy.md`.
  - [x] Do not create substitute feature evidence in Epic 6; route missing behavior to the owning story/gate.
- [x] Leak-scan the release matrix and referenced public-output snippets. (AC: 2, 3)
  - [x] Scan for shaped credentials, private-key blocks, raw Document identifiers, Backend keys, trace/request IDs, file paths, auth claims, raw logs, token values, generated certificate material, OpenBao initialization data, Document payloads, and raw Backend object keys.
  - [x] Use variables or bracket-split patterns so copied commands do not self-match.
  - [x] Add a false-positive location table for any matches that remain safe.
- [x] Run verification gates and update BMAD tracking. (AC: 1-5)
  - [x] `git diff --check`
  - [x] `make proto-check`
  - [x] `scripts/check-e2e-gates.sh`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - [x] Update this story's Dev Agent Record with debug logs, completion notes, and file list.
  - [x] Move Story 6.1 and `sprint-status.yaml` to `review` for BMAD dev completion; leave final `done` to BMAD code review.

## Dev Notes

### Source Requirements

- Epic 6 reconciles feature evidence into a V2 release decision using `scrapctl`, OpenTelemetry evidence, runbooks, alert/query references, a release evidence matrix, closure policy updates, and final real S3/IAM production rehearsal. It aggregates, audits, bundles, and gates evidence; it must not introduce new product behavior that belongs in Epics 1 through 5. [Source: `_bmad-output/planning-artifacts/epics.md#Epic 6: Release Owners Can Prove V2 Readiness`]
- FR-16 requires linked, current, reviewable evidence and operator documentation for every required release claim. Required evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- Final release closure fails if any required requirement lacks current linked evidence. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#Acceptance and Evidence Matrix`]
- DG-5 requires a V2 release evidence matrix mapping every FR, ADR gate, GitHub issue, verification command, evidence artifact, and closure status. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- The existing PRD closure policy requires GitHub Actions evidence for Tier 2 closure when applicable, defines the Tier 3 evidence workflow, and distinguishes `make production-rehearsal-security` from real S3/IAM `make production-rehearsal`. [Source: `docs/prd-closure-policy.md`]

### Required FR Coverage

The release matrix must enumerate these master PRD FRs exactly:

| FR | Title |
| --- | --- |
| FR-1 | Immutable Document API |
| FR-2 | ACK from local replicated durability |
| FR-3 | All-or-error reads and corruption handling |
| FR-4 | Raft and peer replication authority |
| FR-5 | Multi-Shard startup and routing |
| FR-6 | Backend upload and upload pressure |
| FR-7 | Partial local eviction and full-Block restore |
| FR-8 | Phase 5 restore-first cold reads |
| FR-9 | Production security mode and surface boundaries |
| FR-10 | OpenBao envelope encryption and durable rewrap |
| FR-11 | Async Content Scanner |
| FR-12 | Content Quarantine read gate and admin operations |
| FR-13 | `scrapctl` operational baseline |
| FR-14 | `scrapctl` OpenBao bootstrap |
| FR-15 | OTel evidence plane |
| FR-16 | Major-release evidence and documentation closure |

### ADR Gate Coverage

- Include all accepted ADRs in `docs/adr/` as release evidence inputs. The ADR text is decision evidence, not implementation evidence by itself.
- ADR 0025 amends ADR 0008 so Content Quarantine management uses admin HTTP plus `scrapctl`; link Epic 5 evidence, not only the ADR. [Source: `docs/adr/0025-content-quarantine-admin-surface.md`]
- ADR 0026 requires multi-Shard startup/routing for V2 release-ready status and evidence for at least two Shards, deterministic routing, wrong-Shard peer denial, per-Shard admin status, and non-zero Shard Backend upload/restore behavior. [Source: `docs/adr/0026-multi-shard-v2-release-boundary.md`]
- ADR 0027 requires restore-first cold reads and evidence for all-local-copy eviction, restore-on-read, concurrent read singleflight, Backend transient failure, Backend missing/corrupt failure, encryption interaction, and no raw identifier or Backend key leaks. [Source: `docs/adr/0027-phase-5-restore-first-cold-reads.md`]

### Live Tracker Intelligence

- Live check on 2026-06-12 found `gh issue list --repo petabytecl/scrap --milestone storage-gateway-v2 --state open --json number,title,state,labels,url --limit 100` returned no open milestone issues.
- Live check on 2026-06-12 found issue `#429` is still `OPEN`, has labels `ready-for-human`, `production-readiness`, `v2`, and `e2e`, and has no milestone. Its body requires `env GOFLAGS=-buildvcs=false make production-rehearsal` against real non-local S3/IAM and sanitized `artifacts/production-rehearsal/report.json` evidence. [Source: GitHub issue `petabytecl/scrap#429`, checked 2026-06-12]
- Do not omit issue `#429` just because it is absent from milestone-only issue queries. Record the tracker hygiene gap or update the tracker intentionally during implementation if that is in scope.

### Previous Story Intelligence

- Story 5.7 created a reusable closure artifact pattern: source evidence table, closure matrix, P0 blocker evaluation, current-run verification, leak-scan classification, and final `PASS`/`CONCERNS`/`FAIL` gate. Reuse that shape for the release matrix, but broaden it to FRs, ADRs, issues, commands, environments, owners, commit/refs, and freshness. [Source: `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md`]
- Story 5.7 review fixes matter for Story 6.1: leak scans must cover filesystem paths, auth markers, operator fields, hyphenated keys, and row-level false-positive locations; closure rows need exact proof names instead of generic labels. [Source: `_bmad-output/implementation-artifacts/5-7-content-safety-closure-evidence.md#Review Findings`]
- Epic 3 closure already distinguishes current local/package proof from final real S3/IAM evidence. Do not collapse those into a final release `PASS`. [Source: `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`]
- Epic 4 closure explicitly marks real S3/IAM claims as a future gate owned by Story 6.6 / issue `#429`. Reuse that status instead of reclassifying it as complete. [Source: `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`]

### Implementation Guidance

- Prefer documentation/evidence edits only. Expected new artifact: `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`.
- Do not create runbooks, alert/query references, Tier 2/Tier 3 evidence bundles, or S3/IAM rehearsal reports in Story 6.1. Those are later Epic 6 stories unless the matrix only links or classifies them.
- Do not update `docs/prd-closure-policy.md` in Story 6.1 unless the matrix exposes a contradiction that blocks accurate classification. Policy updates are Story 6.7 scope.
- Do not mark `epic-6` done. Create-story may move `epic-6` to `in-progress`; dev-story should move only Story 6.1 to `review`.
- Use exact glossary terms: Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Content Scanner, Content Quarantine, Block Quarantine.
- Keep public/tracker-safe output redacted. Do not paste token values, private keys, generated certificate material, Document payloads, raw Backend keys, raw bucket object keys, validation tokens, raw logs, trace IDs, request IDs, auth claims, or filesystem paths as evidence content.

### Matrix Status Semantics

- `PASS`: current linked evidence exists and the row includes command, artifact, environment, timestamp, commit/ref, expected result, actual result, and redaction proof.
- `CONCERNS`: evidence exists but is scoped, local-only, stale, missing an external proof required for final closure, or has an explicit owner/mitigation before release.
- `FAIL`: required evidence is missing for a release-blocking claim, the referenced issue/gate is open, or the artifact lacks enough proof to support the claim.

### Testing Requirements

Run these gates before marking Story 6.1 ready for review:

```bash
git diff --check
make proto-check
scripts/check-e2e-gates.sh
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run the live tracker commands used by the matrix and record their timestamped results in the artifact:

```bash
gh issue list --repo petabytecl/scrap --milestone storage-gateway-v2 --state all --json number,title,state,labels,milestone,url,updatedAt --limit 200
gh issue view 429 --repo petabytecl/scrap --json number,title,state,body,comments,labels,milestone,url,updatedAt
```

If only BMAD artifacts change, the broad local gate is still required because this story makes release-readiness claims over the full repo evidence trail.

### Project Structure Notes

- Story file: `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md`.
- Expected matrix artifact: `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`.
- Sprint tracker: `_bmad-output/implementation-artifacts/sprint-status.yaml`.
- Durable policy files stay in `docs/`; do not add docs under `docs/release/` for this story unless a future Epic 6 story explicitly owns that durable documentation surface.
- No conflict with the current unified project structure. Story 6.1 should not touch production Go packages, protobuf contracts, generated files, or deployment manifests.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 6 and Story 6.1 source requirements.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-1 through FR-16 and Acceptance and Evidence Matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-5 release documentation/evidence standard.
- `docs/prd-closure-policy.md` - Tier 2, Tier 3, production rehearsal, and real S3/IAM closure policy.
- `docs/production-rehearsal.md` - production security and real S3/IAM rehearsal target semantics and report fields.
- `docs/v2-scope-reconciliation.md` - release scope reconciliation and issue `#429` final-gate context.
- `docs/adr/0025-content-quarantine-admin-surface.md` - Content Quarantine admin surface gate.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - multi-Shard release boundary gate.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - restore-first cold-read release gate.
- `_bmad-output/project-context.md` - repo rules for release evidence, package boundaries, redaction, and final closure.
- `_bmad-output/implementation-artifacts/5-7-content-safety-closure-evidence.md` - previous closure story and review-fix lessons.
- `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md` - most recent closure matrix pattern.

## Dev Agent Record

### Agent Model Used

Codex (GPT-5)

### Debug Log References

- 2026-06-12T17:43:08-04:00 - Story context created from Epic 6, FR-16, DG-5, ADR 0025, ADR 0026, ADR 0027, PRD closure policy, production rehearsal docs, live issue `#429`, Story 5.7 review lessons, and recent git history.
- 2026-06-12T17:46:51-04:00 - Dev-story started from baseline commit `1e368285db815bc32c75e8d131018883bade0d5d`; Story 6.1 and sprint status moved to `in-progress`.
- 2026-06-12T17:51:16-04:00 - Created `v2-release-evidence-matrix.md`, reconciled live GitHub issue state, classified FR/ADR/story gates, and passed `git diff --check`, `make proto-check`, `scripts/check-e2e-gates.sh`, leak scans, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Created `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` with FR-1 through FR-16 rows, ADR 0001 through ADR 0027 rows, story status rollup, live GitHub issue snapshot, verification rows, and release-sensitive scan classification.
- Current V2 release gate is intentionally `FAIL` because Epic 6 final documentation/evidence gates are incomplete and issue `#429` remains open; this is recorded as visible release evidence, not a Story 6.1 failure.
- No production code changed; Story 6.1 stayed aggregation-only.
- Verification passed: `git diff --check`, `make proto-check`, `scripts/check-e2e-gates.sh`, release scans, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### File List

- `_bmad-output/implementation-artifacts/6-1-v2-release-evidence-matrix.md`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Change Log

- 2026-06-12 - Created Story 6.1 context for V2 release evidence matrix.
- 2026-06-12 - Implemented the V2 release evidence matrix and moved Story 6.1 to review.
