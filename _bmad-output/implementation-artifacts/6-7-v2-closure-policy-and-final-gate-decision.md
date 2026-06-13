# Story 6.7: V2 Closure Policy and Final Gate Decision

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want closure policy to enforce V2's no-intermediate-release rule,
so that V2 is not called release-ready until every required feature and evidence gate is complete.

## Acceptance Criteria

1. **AC-6.7.1 - Closed work is not enough for release readiness.** Given V2 closure policy is updated, when final readiness is evaluated, then closed issues, merged PRs, or closed phase milestones are not enough without current linked evidence. Evidence records the policy diff and review result.
2. **AC-6.7.1a - Required blockers are non-waivable for release PASS.** Given a proposed waiver would bypass required P0 evidence, security evidence, real S3/IAM evidence, or redaction proof, when closure policy is applied, then the waiver is rejected as non-waivable for V2 release readiness. Evidence records the non-waivable blocker list.
3. **AC-6.7.2 - Missing required evidence prevents PASS.** Given any required FR, ADR gate, story, runbook, alert/query reference, Tier gate, security rehearsal, or real S3/IAM evidence is missing, when closure is evaluated, then the final decision is `FAIL` or `CONCERNS`, not `PASS`. Evidence records owner, mitigation, and next action for each gap.
4. **AC-6.7.3 - Final decision records non-goals explicitly.** Given final release review completes, when all evidence is current and redacted, then the matrix records `PASS` with linked artifacts and remaining non-goals explicitly out of scope. Evidence records the final release gate decision and non-goal review.
5. **AC-6.7.4 - Every PASS traces to feature evidence.** Given epic-level evidence is rolled into the final matrix, when the final gate is reviewed, then every `PASS` traces back to a feature epic, artifact, command, owner, timestamp, and commit/ref. Evidence records the rollup from Epic 1 through Epic 6.

## Tasks / Subtasks

- [ ] Update the durable closure policy. (AC: 1, 2)
  - [ ] Update `docs/prd-closure-policy.md` with the V2 no-intermediate-release rule and the distinction between progress evidence and release evidence.
  - [ ] Add a non-waivable blocker section covering required P0 feature evidence, production security evidence, Tier 2/Tier 3 release evidence, real S3/IAM proof or explicit accepted waiver, redaction proof, and current linked artifacts.
  - [ ] State that closed issues, merged PRs, closed milestones, local-only output, screenshots, stale artifacts, and unlinked terminal snippets cannot produce release `PASS`.
  - [ ] Preserve existing Tier 2, Tier 3, and production rehearsal guidance; do not weaken Story 6.5 or Story 6.6 gate language.
- [ ] Create the final V2 closure decision artifact. (AC: 1-5)
  - [ ] Add `_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md`.
  - [ ] Record current branch, commit/ref, live issue `#429` state, latest `ci` and `CodeQL Advanced` run URLs for the tested head, and the exact source artifacts reviewed.
  - [ ] Record the final gate decision as `FAIL` or `CONCERNS` unless every required blocker is closed with current linked evidence.
  - [ ] Include a gap table with owner, mitigation, next action, freshness, and release status for every unresolved release blocker.
  - [ ] Include a non-goal review table so explicitly out-of-scope items are visible and cannot be confused with missing required scope.
- [ ] Add a static final-closure validator. (AC: 1-5)
  - [ ] Prefer a focused script such as `scripts/check-v2-closure-gate.sh` plus Go tests in `scripts/v2_closure_gate_test.go`.
  - [ ] Accept final `PASS` only when the closure artifact has no open non-waivable blockers, links current Tier 2/Tier 3 evidence, links production security rehearsal evidence, links real S3/IAM evidence or an explicit accepted waiver, and shows redaction proof.
  - [ ] Reject final `PASS` when issue `#429` is open, Tier 2/Tier 3 runtime evidence is missing, CodeQL/CI are not green for the tested ref, redaction proof is missing, or any row relies only on closed issues/merged PRs/local-only output.
  - [ ] Allow honest `FAIL` and `CONCERNS` decisions when every gap is ownered with mitigation and next action.
  - [ ] Wire the validator into `scripts/check-e2e-gates.sh` so static evidence gates protect future edits.
- [ ] Update the release matrix and BMAD tracking without over-claiming. (AC: 1-5)
  - [ ] Update `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` current release decision, FR-16 row, Story 6.7 row, final gate row, and issue `#429` linkage.
  - [ ] Keep Story 6.6 / issue `#429` as `FAIL` while real non-local S3/IAM evidence is unavailable.
  - [ ] Keep Story 6.5 Tier 2/Tier 3 runtime evidence gaps visible unless current durable artifacts are linked.
  - [ ] Mark this story and sprint status accurately; do not mark Epic 6 or V2 release `done`/`PASS` from policy work alone.
- [ ] Preserve Epic 6 aggregation scope and redaction discipline. (AC: 1-5)
  - [ ] Do not add product behavior, storage authority, admin endpoints, new telemetry instruments, new release dependencies, or substitute feature evidence in Story 6.7.
  - [ ] Do not paste raw workflow logs, credentials, private keys, generated certificate material, Document payloads, raw Document identifiers, Backend keys, trace IDs, request IDs, auth claims, host-absolute local paths, data keys, wrapped-key ciphertext, raw dependency output, or raw Backend object keys into committed artifacts.
  - [ ] Any waiver language must be explicit, ownered, dated, scoped, and incapable of converting a non-waivable blocker into release `PASS`.
- [ ] Verify and close out safely. (AC: 1-5)
  - [ ] Run the final-closure validator and its tests.
  - [ ] Run `scripts/check-e2e-gates.sh`.
  - [ ] Run `git diff --check`.
  - [ ] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before review if scripts or release policy validators changed.
  - [ ] Run release-sensitive scans over the story, policy, closure artifact, matrix, validator, tests, and any changed runbook files.
  - [ ] Move this story to `review`; leave `done` for BMAD code review after review findings are addressed.

## Dev Notes

### Current Gate State

- Story 6.6 review fixes were committed and pushed at `d3292e8ef9d8fb185288927e34b0d40b6139efda`.
- Latest live remote checks for `d3292e8ef9d8fb185288927e34b0d40b6139efda` are green:
  - `ci` run `27451802792`: https://github.com/petabytecl/scrap/actions/runs/27451802792
  - `CodeQL Advanced` run `27451802784`: https://github.com/petabytecl/scrap/actions/runs/27451802784
- Live issue `#429` is still `OPEN`, labels `ready-for-human`, `production-readiness`, `v2`, `e2e`, milestone `NONE`, updated `2026-06-10T02:56:17Z`: https://github.com/petabytecl/scrap/issues/429
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` currently keeps V2 release gate status as `FAIL`. Preserve that unless every required gate has current linked proof.

### Source Requirements

- The master PRD states that V2 has no intermediate releases and is not release-ready until all required V2 features and evidence gates are complete. Closed phases and merged issues are evidence inputs, not closure. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#0. Document Purpose`]
- FR-16 requires linked, current, reviewable evidence and operator documentation for every required release claim. Required evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- DG-5 requires runbooks, alert/query references, evidence matrix, and closure policy updates as release scope. It requires final closure to reject open decision gates, missing artifacts, stale local-only evidence, and unlinked issue/PR proof. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- `docs/v2-scope-reconciliation.md` is explicit that release evidence is a final gate, issue `#429` remains required, and final evidence must run after product scope is complete. [Source: `docs/v2-scope-reconciliation.md#Source Rules`; `docs/v2-scope-reconciliation.md#Recommended Next Backlog Order`]
- `docs/prd-closure-policy.md` currently covers Tier 2, Tier 3, production rehearsal, and real S3/IAM paths, but it does not yet fully spell out the V2 no-intermediate-release rule or non-waivable blocker list. [Source: `docs/prd-closure-policy.md`]

### Existing Gate Surfaces To Reuse

| Gate | Existing surface | Story 6.7 use |
| --- | --- | --- |
| Release matrix | `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` | Update current release decision, FR-16, Story 6.7, final gate, and issue `#429` rows. |
| Evidence collection runbook | `docs/runbooks/v2-evidence-collection.md` | Reference for required row fields, redaction requirements, and authority boundary. Update only if closure policy changes operator steps. |
| Tier evidence contract | `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md`; `scripts/check-release-tier-gates.sh` | Do not duplicate Tier validator logic. Final closure validator should consume the matrix/closure artifact and require Tier rows to stay non-PASS when runtime evidence is missing. |
| Real S3/IAM contract | `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`; `scripts/check-real-s3-iam-gate.sh` | Do not duplicate S3 report checks. Final closure validator should reject release `PASS` when issue `#429` is open or real S3/IAM proof is missing. |
| Static evidence gate bundle | `scripts/check-e2e-gates.sh` | Add the final-closure validator after focused tests pass. |
| Live tracker/check evidence | `gh issue view 429`; `gh run list --branch v2` | Refresh before final closure artifact updates because issue and workflow state drift. |

### Expected Current Decision

The expected Story 6.7 implementation result is likely `FAIL`, not `PASS`, because:

- issue `#429` is open;
- real non-local S3/IAM runtime evidence is missing;
- Story 6.5 records Tier 2/Tier 3 hard criteria but not final durable runtime evidence;
- final closure policy has not yet been updated and validated.

This is acceptable if the closure artifact is complete, ownered, and honest. Story 6.7 closes the policy/final-decision story; it does not necessarily close the V2 release.

### Non-Waivable Blocker Guidance

Treat these as non-waivable for final V2 release `PASS`:

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

- `_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md`
- `scripts/check-v2-closure-gate.sh`
- `scripts/v2_closure_gate_test.go`

Likely UPDATE:

- `docs/prd-closure-policy.md`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
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
  - `scripts/check-v2-closure-gate.sh`
  - `scripts/check-e2e-gates.sh`
  - `git diff --check`
  - `env GOCACHE=/tmp/scrap-v2-go-build make check` before review/commit if scripts changed

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
- Keep `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` as the aggregate matrix, not a dumping ground for raw logs.
- No UX artifacts are relevant.

### References

- `_bmad-output/planning-artifacts/epics.md:1759` - Story 6.7 source story and acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md:25` - no intermediate V2 release rule.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md:466` - FR-16 major-release evidence and documentation closure.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md:56` - closed phase/issue is not release-ready proof.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md:383` - DG-5 release documentation/evidence standard.
- `docs/prd-closure-policy.md:1` - existing PRD closure policy to update.
- `docs/v2-scope-reconciliation.md:10` - no intermediate release clarification.
- `docs/v2-scope-reconciliation.md:60` - real S3/IAM required final gate.
- `docs/runbooks/v2-evidence-collection.md:70` - required release evidence row fields.
- `_bmad-output/implementation-artifacts/6-5-tier-2-and-tier-3-release-evidence-gates.md` - Tier gate criteria and review lessons.
- `_bmad-output/implementation-artifacts/6-6-real-s3-iam-production-rehearsal-closure.md` - real S3/IAM criteria and review lessons.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:67` - current release decision.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:95` - FR-16 gap state.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:170` - Story 6.6 row.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:185` - issue `#429` row.
- GitHub issue `#429`: https://github.com/petabytecl/scrap/issues/429
- Latest green `ci` run for `d3292e8`: https://github.com/petabytecl/scrap/actions/runs/27451802792
- Latest green `CodeQL Advanced` run for `d3292e8`: https://github.com/petabytecl/scrap/actions/runs/27451802784
- Prior-art reference: https://pypi.org/project/secure-sdlc-evidence-collector/
- Prior-art reference: https://bytes.engineer/blog/release-validation-gate-checklist/

## Dev Agent Record

### Agent Model Used

### Debug Log References

### Completion Notes List

### File List
