---
baseline_commit: 9efe29ccc318d0645c9249bd0c1e67eb522e2078
---

# Story 6.7: SCRAP Closure Policy and Final Gate Decision

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want closure policy to enforce SCRAP's no-intermediate-release rule,
so that SCRAP is not called release-ready until every required feature and evidence gate is complete.

## Acceptance Criteria

1. **AC-6.7.1 - Closed work is not enough for release readiness.** Given SCRAP closure policy is updated, when final readiness is evaluated, then closed issues, merged PRs, or closed phase milestones are not enough without current linked evidence. Evidence records the policy diff and review result.
2. **AC-6.7.1a - Required blockers are non-waivable for release PASS.** Given a proposed waiver would bypass required P0 evidence, security evidence, real S3/IAM evidence, or redaction proof, when closure policy is applied, then the waiver is rejected as non-waivable for SCRAP release readiness. Evidence records the non-waivable blocker list.
3. **AC-6.7.2 - Missing required evidence prevents PASS.** Given any required FR, ADR gate, story, runbook, alert/query reference, Tier gate, security rehearsal, or real S3/IAM evidence is missing, when closure is evaluated, then the final decision is `FAIL` or `CONCERNS`, not `PASS`. Evidence records owner, mitigation, and next action for each gap.
4. **AC-6.7.3 - Final decision records non-goals explicitly.** Given final release review completes, when all evidence is current and redacted, then the matrix records `PASS` with linked artifacts and remaining non-goals explicitly out of scope. Evidence records the final release gate decision and non-goal review.
5. **AC-6.7.4 - Every PASS traces to feature evidence.** Given epic-level evidence is rolled into the final matrix, when the final gate is reviewed, then every `PASS` traces back to a feature epic, artifact, command, owner, timestamp, and commit/ref. Evidence records the rollup from Epic 1 through Epic 6.

## Tasks / Subtasks

- [x] Update the durable closure policy. (AC: 1, 2)
  - [x] Update `docs/prd-closure-policy.md` with the SCRAP no-intermediate-release rule and the distinction between progress evidence and release evidence.
  - [x] Add a non-waivable blocker section covering required P0 feature evidence, production security evidence, Tier 2/Tier 3 release evidence, real S3/IAM proof or explicit accepted waiver, redaction proof, and current linked artifacts.
  - [x] State that closed issues, merged PRs, closed milestones, local-only output, screenshots, stale artifacts, and unlinked terminal snippets cannot produce release `PASS`.
  - [x] Preserve existing Tier 2, Tier 3, and production rehearsal guidance; do not weaken Story 6.5 or Story 6.6 gate language.
- [x] Create the final SCRAP closure decision artifact. (AC: 1-5)
  - [x] Add `_bmad-output/implementation-artifacts/closure-policy-final-gate-decision.md`.
  - [x] Record current branch, commit/ref, live issue `#429` state, latest `ci` and `CodeQL Advanced` run URLs for the tested head, and the exact source artifacts reviewed.
  - [x] Record the final gate decision as `FAIL` or `CONCERNS` unless every required blocker is closed with current linked evidence.
  - [x] Include a gap table with owner, mitigation, next action, freshness, and release status for every unresolved release blocker.
  - [x] Include a non-goal review table so explicitly out-of-scope items are visible and cannot be confused with missing required scope.
- [x] Add a static final-closure validator. (AC: 1-5)
  - [x] Prefer a focused script such as `scripts/check-closure-gate.sh` plus Go tests in `scripts/closure_gate_test.go`.
  - [x] Accept final `PASS` only when the closure artifact has no open non-waivable blockers, links current Tier 2/Tier 3 evidence, links production security rehearsal evidence, links real S3/IAM evidence or an explicit accepted waiver, and shows redaction proof.
  - [x] Reject final `PASS` when issue `#429` is open, Tier 2/Tier 3 runtime evidence is missing, CodeQL/CI are not green for the tested ref, redaction proof is missing, or any row relies only on closed issues/merged PRs/local-only output.
  - [x] Allow honest `FAIL` and `CONCERNS` decisions when every gap is ownered with mitigation and next action.
  - [x] Wire the validator into `scripts/check-e2e-gates.sh` so static evidence gates protect future edits.
- [x] Update the release matrix and BMAD tracking without over-claiming. (AC: 1-5)
  - [x] Update `_bmad-output/implementation-artifacts/release-evidence-matrix.md` current release decision, FR-16 row, Story 6.7 row, final gate row, and issue `#429` linkage.
  - [x] Keep Story 6.6 / issue `#429` as `FAIL` while real non-local S3/IAM evidence is unavailable.
  - [x] Keep Story 6.5 Tier 2/Tier 3 runtime evidence gaps visible unless current durable artifacts are linked.
  - [x] Mark this story and sprint status accurately; do not mark Epic 6 or SCRAP release `done`/`PASS` from policy work alone.
- [x] Preserve Epic 6 aggregation scope and redaction discipline. (AC: 1-5)
  - [x] Do not add product behavior, storage authority, admin endpoints, new telemetry instruments, new release dependencies, or substitute feature evidence in Story 6.7.
  - [x] Do not paste raw workflow logs, credentials, private keys, generated certificate material, Document payloads, raw Document identifiers, Backend keys, trace IDs, request IDs, auth claims, host-absolute local paths, data keys, wrapped-key ciphertext, raw dependency output, or raw Backend object keys into committed artifacts.
  - [x] Any waiver language must be explicit, ownered, dated, scoped, and incapable of converting a non-waivable blocker into release `PASS`.
- [x] Verify and close out safely. (AC: 1-5)
  - [x] Run the final-closure validator and its tests.
  - [x] Run `scripts/check-e2e-gates.sh`.
  - [x] Run `git diff --check`.
  - [x] Run `env GOCACHE=/tmp/scrap-go-build make check` before review if scripts or release policy validators changed.
  - [x] Run release-sensitive scans over the story, policy, closure artifact, matrix, validator, tests, and any changed runbook files.
  - [x] Move this story to `review`; leave `done` for BMAD code review after review findings are addressed.

### Review Findings

- [x] [Review][Patch] Separate waiver language from the final PASS allow-list and reject waiver-backed PASS evidence.
- [x] [Review][Patch] Add branch, reviewed head, and GitHub Actions run URLs to the final closure artifact.
- [x] [Review][Patch] Add freshness and release-status fields to the gap table and enforce them in the validator.
- [x] [Review][Patch] Align release-matrix Story 6.2, Story 6.3, and Story 6.7 rows with current BMAD tracking.
- [x] [Review][Patch] Harden final-closure validation for inconsistent statuses, unresolved PASS gaps, non-PASS ownership, unreadable evidence, and prose-only production-security proof.

## Dev Notes

### Current Gate State

- Story 6.6 review fixes were committed and pushed at `d3292e8ef9d8fb185288927e34b0d40b6139efda`.
- Latest live remote checks for `d3292e8ef9d8fb185288927e34b0d40b6139efda` are green:
  - `ci` run `27451802792`: https://github.com/petabytecl/scrap/actions/runs/27451802792
  - `CodeQL Advanced` run `27451802784`: https://github.com/petabytecl/scrap/actions/runs/27451802784
- Live issue `#429` is still `OPEN`, labels `ready-for-human`, `production-readiness`, `main`, `e2e`, milestone `NONE`, updated `2026-06-10T02:56:17Z`: https://github.com/petabytecl/scrap/issues/429
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md` currently keeps SCRAP release gate status as `FAIL`. Preserve that unless every required gate has current linked proof.

### Source Requirements

- The master PRD states that SCRAP has no intermediate releases and is not release-ready until all required SCRAP features and evidence gates are complete. Closed phases and merged issues are evidence inputs, not closure. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-master-2026-06-10/prd.md#0. Document Purpose`]
- FR-16 requires linked, current, reviewable evidence and operator documentation for every required release claim. Required evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- DG-5 requires runbooks, alert/query references, evidence matrix, and closure policy updates as release scope. It requires final closure to reject open decision gates, missing artifacts, stale local-only evidence, and unlinked issue/PR proof. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- `docs/scope-reconciliation.md` is explicit that release evidence is a final gate, issue `#429` remains required, and final evidence must run after product scope is complete. [Source: `docs/scope-reconciliation.md#Source Rules`; `docs/scope-reconciliation.md#Recommended Next Backlog Order`]
- `docs/prd-closure-policy.md` currently covers Tier 2, Tier 3, production rehearsal, and real S3/IAM paths, but it does not yet fully spell out the SCRAP no-intermediate-release rule or non-waivable blocker list. [Source: `docs/prd-closure-policy.md`]

### Existing Gate Surfaces To Reuse

| Gate | Existing surface | Story 6.7 use |
| --- | --- | --- |
| Release matrix | `_bmad-output/implementation-artifacts/release-evidence-matrix.md` | Update current release decision, FR-16, Story 6.7, final gate, and issue `#429` rows. |
| Evidence collection runbook | `docs/runbooks/evidence-collection.md` | Reference for required row fields, redaction requirements, and authority boundary. Update only if closure policy changes operator steps. |
| Tier evidence contract | `_bmad-output/implementation-artifacts/release-tier-gates-evidence.md`; `scripts/check-release-tier-gates.sh` | Do not duplicate Tier validator logic. Final closure validator should consume the matrix/closure artifact and require Tier rows to stay non-PASS when runtime evidence is missing. |
| Real S3/IAM contract | `_bmad-output/implementation-artifacts/real-s3-iam-production-rehearsal-evidence.md`; `scripts/check-real-s3-iam-gate.sh` | Do not duplicate S3 report checks. Final closure validator should reject release `PASS` when issue `#429` is open or real S3/IAM proof is missing. |
| Static evidence gate bundle | `scripts/check-e2e-gates.sh` | Add the final-closure validator after focused tests pass. |
| Live tracker/check evidence | `gh issue view 429`; `gh run list --branch v2` | Refresh before final closure artifact updates because issue and workflow state drift. |

### Expected Current Decision

The expected Story 6.7 implementation result is likely `FAIL`, not `PASS`, because:

- issue `#429` is open;
- real non-local S3/IAM runtime evidence is missing;
- Story 6.5 records Tier 2/Tier 3 hard criteria but not final durable runtime evidence;
- final closure policy has not yet been updated and validated.

This is acceptable if the closure artifact is complete, ownered, and honest. Story 6.7 closes the policy/final-decision story; it does not necessarily close the SCRAP release.

### Non-Waivable Blocker Guidance

Treat these as non-waivable for final SCRAP release `PASS`:

- missing required P0 feature evidence for FRs or accepted ADR gates;
- missing production security evidence or CodeQL/CI failure for the tested release ref;
- missing Tier 2 prod-like evidence required by closure policy;
- missing Tier 3 telemetry/evidence bundle and privacy proof required by closure policy;
- missing real S3/IAM proof for Backend S3 claims while issue `#429` is open, unless there is an explicit accepted waiver that keeps final release below `PASS`;
- missing redaction proof or any public artifact containing credential values, private keys, raw Document identifiers, raw Backend keys, Document payloads, wrapped-key ciphertext, data keys, trace IDs, request IDs, auth claims, raw logs, or host-absolute paths intended for public evidence.

### Architecture Compliance

- Evidence is release proof, not storage authority. It must not override committed Shard state, Backend confirmation semantics, or feature-specific failure behavior.
- Preserve domain terms from `CONTEXT.md`: Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Upload Outbox, Confirmed Upload Catalog, and Pebble Projection.
- Do not introduce an ADR unless Story 6.7 changes deployment, security, auth, wire/storage contracts, dependency choices, or cross-package boundaries. Normal docs, BMAD artifacts, and validators do not require an ADR.
- Application logging remains `log/slog`; this story should not need logging changes.

### File Structure Requirements

Likely NEW:

- `_bmad-output/implementation-artifacts/closure-policy-final-gate-decision.md`
- `scripts/check-closure-gate.sh`
- `scripts/closure_gate_test.go`

Likely UPDATE:

- `docs/prd-closure-policy.md`
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md`
- `scripts/check-e2e-gates.sh`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- this story file

Avoid:

- committing ignored runtime output under `artifacts/`;
- adding a new dependency or package for markdown parsing unless the existing script/test approach cannot express the contract;
- editing production Go packages unless a validator truly requires a reusable helper, which is not expected.

### Testing Requirements

- Use deterministic fixture tests for final closure validator behavior.
- Red-phase tests should first prove an invalid `PASS` closure can fail:
  - release `PASS` with open issue `#429`;
  - release `PASS` with missing Tier 2/Tier 3 runtime artifact links;
  - release `PASS` with closed issues/merged PRs but no evidence rows;
  - release `PASS` with missing redaction proof;
  - honest `FAIL` or `CONCERNS` with owner and mitigation should pass.
- Required local gates for implementation:
  - `go test -count=1 ./scripts`
  - `scripts/check-closure-gate.sh`
  - `scripts/check-e2e-gates.sh`
  - `git diff --check`
  - `env GOCACHE=/tmp/scrap-go-build make check` before review/commit if scripts changed

### Previous Story Intelligence

- Story 6.5 review fixes proved that gate validators must inspect each relevant row and reject inconsistent release `PASS`; do not rely on broad grep-only checks.
- Story 6.6 review fixes proved that row-level `PASS` must trigger release-level validation, JSON type checks must reject booleans as integers, report paths must match, provenance/redaction fields must be explicit, markdown table parsing must tolerate code-span pipes, and issue `#429` cannot be cited as open on `PASS`.
- Story 6.6 deliberately kept issue `#429` and real S3/IAM proof as `FAIL` while no real non-local S3/IAM environment is available. Story 6.7 must not convert that known gap into release `PASS`.
- Recent commits show the preferred pattern: create a focused script validator, add Go fixture tests under `scripts`, wire the validator into `scripts/check-e2e-gates.sh`, update BMAD evidence artifacts, then run broad local and remote gates.

### Research Notes

- Repo-local GitHub code search for release evidence and closure-policy patterns found no implementation that should be imported into this repo.
- Exa prior-art search found generic release readiness guidance that favors deterministic gates with explicit evidence, owner, lineage, and gap severity. Story 6.7 should apply that pattern with repo-local artifacts instead of adding an external tool or dependency.
- No package-registry search is relevant: this story should not add a runtime or tool dependency.

### Project Structure Notes

- Story 6.7 is a release policy and evidence story. Durable policy belongs in `docs/prd-closure-policy.md`; release decision evidence belongs in `_bmad-output/implementation-artifacts/`.
- Keep `_bmad-output/implementation-artifacts/release-evidence-matrix.md` as the aggregate matrix, not a dumping ground for raw logs.
- No UX artifacts are relevant.

### References

- `_bmad-output/planning-artifacts/epics.md:1759` - Story 6.7 source story and acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-master-2026-06-10/prd.md:25` - no intermediate SCRAP release rule.
- `_bmad-output/planning-artifacts/prds/prd-scrap-master-2026-06-10/prd.md:466` - FR-16 major-release evidence and documentation closure.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md:56` - closed phase/issue is not release-ready proof.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md:383` - DG-5 release documentation/evidence standard.
- `docs/prd-closure-policy.md:1` - existing PRD closure policy to update.
- `docs/scope-reconciliation.md:10` - no intermediate release clarification.
- `docs/scope-reconciliation.md:60` - real S3/IAM required final gate.
- `docs/runbooks/evidence-collection.md:70` - required release evidence row fields.
- `_bmad-output/implementation-artifacts/6-5-tier-2-and-tier-3-release-evidence-gates.md` - Tier gate criteria and review lessons.
- `_bmad-output/implementation-artifacts/6-6-real-s3-iam-production-rehearsal-closure.md` - real S3/IAM criteria and review lessons.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md:67` - current release decision.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md:95` - FR-16 gap state.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md:170` - Story 6.6 row.
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md:185` - issue `#429` row.
- GitHub issue `#429`: https://github.com/petabytecl/scrap/issues/429
- Latest green `ci` run for `d3292e8`: https://github.com/petabytecl/scrap/actions/runs/27451802792
- Latest green `CodeQL Advanced` run for `d3292e8`: https://github.com/petabytecl/scrap/actions/runs/27451802784
- Prior-art reference: https://pypi.org/project/secure-sdlc-evidence-collector/
- Prior-art reference: https://bytes.engineer/blog/release-validation-gate-checklist/

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- Red phase: `go test -count=1 ./scripts -run SCRAPClosureGate` initially failed because `scripts/check-closure-gate.sh` was absent.
- Green focused tests: `go test -count=1 ./scripts -run SCRAPClosureGate` passed.
- Final closure validator: `scripts/check-closure-gate.sh` passed.
- Aggregated static gate: `scripts/check-e2e-gates.sh` passed.
- Shell syntax: `bash -n scripts/check-closure-gate.sh scripts/check-e2e-gates.sh` passed.
- Broad scripts tests: `go test -count=1 ./scripts` passed.
- Whitespace check: `git diff --check` passed.
- Release-sensitive scan over changed Story 6.7 files matched only the matrix's documented scanner regex definitions, not committed secret or raw-evidence values.
- Full local gate: `env GOCACHE=/tmp/scrap-go-build make check` passed.
- BMAD code review ran three layers: Blind Hunter, Edge Case Hunter, and Acceptance Auditor.
- Review-fix focused gates passed: `go test -count=1 ./scripts`, `bash -n scripts/check-closure-gate.sh scripts/check-e2e-gates.sh`, `scripts/check-closure-gate.sh`, `scripts/check-e2e-gates.sh`, and `git diff --check`.
- Review-fix broad local gate passed: `env GOCACHE=/tmp/scrap-go-build make check`.

### Completion Notes List

- Added durable SCRAP closure policy language that separates progress evidence from release evidence and makes required blockers non-waivable for final `PASS`.
- Added the final SCRAP closure decision artifact with current honest status `FAIL`, live issue `#429` state, green baseline CI/CodeQL references, ownered gaps, redaction proof, and explicit non-goal review.
- Added a row-aware static closure validator and Go fixture tests for honest `FAIL`, complete `PASS`, and invalid `PASS` cases.
- Wired the closure validator into `scripts/check-e2e-gates.sh` and updated the release matrix without marking Epic 6 or SCRAP release as `PASS`.
- Addressed BMAD code-review findings for closure policy waiver wording, required artifact metadata, gap-table structure, matrix drift, and validator coverage.

### File List

- `_bmad-output/implementation-artifacts/6-7-closure-policy-and-final-gate-decision.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `_bmad-output/implementation-artifacts/closure-policy-final-gate-decision.md`
- `_bmad-output/implementation-artifacts/release-evidence-matrix.md`
- `docs/prd-closure-policy.md`
- `scripts/check-e2e-gates.sh`
- `scripts/check-closure-gate.sh`
- `scripts/closure_gate_test.go`

## Change Log

- 2026-06-12: Implemented Story 6.7 closure policy, final gate decision artifact, validator, release matrix updates, and verification gates.
- 2026-06-12: Addressed Story 6.7 BMAD code-review findings and moved Story 6.7 to done.
