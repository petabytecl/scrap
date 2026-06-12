# Story 5.7: Content Safety Closure Evidence

Status: ready-for-dev

## Story

As a release owner,
I want scanner, quarantine, admin, and `scrapctl` evidence linked,
so that Epic 5 cannot close from scanner happy-path tests alone.

## Acceptance Criteria

1. **AC-5.7.1 - Epic 5 evidence chain is linked.** Given Epic 5 evidence is collected, when closure is evaluated, then scheduling, scanner outage, watermarks, rescan, detection-to-Raft, read denial, metadata scan status, admin confirm/release, `scrapctl`, race handling, and redaction evidence are linked. Evidence records artifact paths and owning stories.
2. **AC-5.7.2 - Closure artifact is leak-scanned.** Given scanner signatures, YARA rule text, dependency logs, sensitive content, or raw payloads may exist in implementation or evidence surfaces, when artifacts are reviewed, then those values are excluded from closure output. Evidence records the leak-scan command and result.
3. **AC-5.7.3 - Unsafe-read and authority gaps block closure.** Given any P0 unsafe-read or quarantine-authority evidence is missing, when closure is evaluated, then closure is `FAIL`, not deferred to Epic 6. Evidence records `PASS`, `CONCERNS`, or `FAIL` using V2 release gate language.

## Tasks / Subtasks

- [ ] Create the aggregate Epic 5 closure artifact before changing any code. (AC: 1-3)
  - [ ] Add `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md`.
  - [ ] Record baseline commit, current branch, story scope, source evidence files, and final gate language.
  - [ ] State that this story is evidence closure, not a new scanner/admin/read-path implementation slice.
- [ ] Build a trace matrix from Story 5.1 through Story 5.6 evidence. (AC: 1)
  - [ ] Link Story 5.1 evidence for post-ACK scheduling, scanner outage visibility, telemetry bounds, crash/poison/duplicate scheduling, and scanner redaction.
  - [ ] Link Story 5.2 evidence for persisted scanner watermarks, restart-safe resume, rescan priority, rollback/conflict behavior, and progress-only authority.
  - [ ] Link Story 5.3 evidence for metadata-only `QuarantineDocument`, sparse Content Quarantine Projection state, scanner-not-authority boundary, Raft replay, and restart behavior.
  - [ ] Link Story 5.4 evidence for quarantined read denial, bounded `FAILED_PRECONDITION`, metadata `scan_status`, read/quarantine race failure, replay, and corrupt-state fail-closed behavior.
  - [ ] Link Story 5.5 evidence for admin HTTP list/inspect, confirm/release through committed Raft authority, authz, rate limits, audit, redaction, and post-release read convergence.
  - [ ] Link Story 5.6 evidence for `scrapctl quarantine` list/inspect/confirm/release/evidence, admin HTTP routing proof, typed failures, strict response handling, and output/report redaction.
- [ ] Evaluate release-gate status with explicit blockers. (AC: 1, 3)
  - [ ] Mark each Epic 5 closure row `PASS`, `CONCERNS`, or `FAIL`.
  - [ ] Treat missing unsafe-read denial, read/quarantine race, committed Raft authority, or confirm/release convergence proof as `FAIL`.
  - [ ] Treat stale commands, missing artifact paths, missing redaction commands, or local-only proof gaps as at least `CONCERNS` unless current reruns resolve them.
  - [ ] Do not move Epic 5 to `done` unless every Story 5.1-5.7 closure row is `PASS`.
- [ ] Run leak and secret-shape scans over the closure scope. (AC: 2)
  - [ ] Scan all Epic 5 story files, evidence files, and touched scanner/quarantine/admin/CLI code paths.
  - [ ] Use shell variables and bracket-split patterns so the command does not self-match copied sensitive text.
  - [ ] Check for scanner signature or rule material, scanner dependency diagnostics, raw payload markers, raw Document identity query strings, Backend keys, trace/request IDs, filesystem paths, auth claims, and free-form operator payloads.
  - [ ] Record exact commands, scope, and `PASS`/`FAIL` results in the closure artifact.
- [ ] Run verification gates appropriate for an evidence-closure slice. (AC: 1-3)
  - [ ] `git diff --check`
  - [ ] `make proto-check`
  - [ ] `scripts/check-e2e-gates.sh`
  - [ ] `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - [ ] Re-run narrower Story 5 focused package tests if the audit changes production code or if a closure row depends on current package behavior not already covered by the broad gate.
- [ ] Update BMAD tracking after closure evaluation. (AC: 1-3)
  - [ ] Update this story's Dev Agent Record with debug log references, completion notes, and file list.
  - [ ] Update `epic-5-content-safety-closure-evidence.md` with the final decision.
  - [ ] If every closure row passes, update `sprint-status.yaml` for Story 5.7 to `done`; leave `epic-5` `in-progress` unless the workflow explicitly closes the epic.

## Dev Notes

### Source Requirements

- Epic 5 requires security operators to scan sealed Block bytes after ACK, quarantine suspicious Documents through Raft-owned metadata, deny unsafe reads, preserve `HeadDocument` and `FindDocuments` reconciliation metadata, and confirm/release quarantine through admin HTTP plus `scrapctl`. [Source: `_bmad-output/planning-artifacts/epics.md#Epic 5: Security Operators Can Contain Unsafe Content Without Mutating Documents`]
- FR-11 requires a leader-owned background Content Scanner that scans sealed Block bytes with ClamAV and YARA after ACK and never blocks the write path. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-11: Async Content Scanner`]
- FR-12 requires metadata-level Content Quarantine: `ReadDocument` denies bytes while `HeadDocument` and `FindDocuments` expose metadata with scan status. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-12: Content Quarantine read gate and admin operations`]
- Final release closure fails if any required requirement lacks current linked evidence. Epic 5 closure should use the same `PASS`/`CONCERNS`/`FAIL` release-gate vocabulary. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#Acceptance and Evidence Matrix`]
- ADR 0025 amends ADR 0008 so Content Quarantine management uses existing admin HTTP plus `scrapctl`; do not introduce a new admin gRPC service for this story. [Source: `docs/adr/0025-content-quarantine-admin-surface.md#Decision`]

### Architecture Guardrails

- Content Scanner and Content Quarantine are separate from Deep Scrub and Block Quarantine. Content Quarantine is metadata-level Document gating; Block bytes and `.blk`/`.idx` files are untouched. [Source: `CONTEXT.md#Language`]
- Raft owns Content Quarantine state. Pebble Projection materializes read-side state. Scanner watermarks are progress evidence, not Document visibility authority. Evidence, logs, audit, and OTel observe behavior; they do not decide state. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#Authority Patterns`]
- Public errors and evidence must not leak scanner signatures, YARA rule text, dependency logs, raw Document identifiers, Backend keys, trace IDs, request IDs, filesystem paths, or unbounded payloads. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#Error, Redaction, and Evidence Patterns`]
- No ADR is needed for this story unless the implementation changes deployment, security, auth, wire/storage contracts, or closure policy semantics. A pure BMAD evidence artifact does not require an ADR. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]

### Required Evidence Inputs

Use these artifacts as the source of truth. Do not summarize closure from memory or from story status alone.

| Owning story | Evidence artifact | Closure coverage to verify |
| --- | --- | --- |
| 5.1 | `_bmad-output/implementation-artifacts/epic-5-content-scanner-engine-boundary-evidence.md` | Post-ACK scheduling, scanner outage/lag visibility, bounded telemetry, crash/poison/duplicate scheduling, scanner redaction. |
| 5.2 | `_bmad-output/implementation-artifacts/epic-5-scanner-watermarks-rescan-evidence.md` | Persisted watermarks, restart resume, signature-version rescan, rollback/conflict duplicate safety, progress-only authority. |
| 5.3 | `_bmad-output/implementation-artifacts/epic-5-quarantinedocument-raft-projection-evidence.md` | Metadata-only `QuarantineDocument`, Projection prefix, scanner-not-authority boundary, committed replay/restart, corrupt-state rejection. |
| 5.4 | `_bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md` | Quarantined read denial, no bytes before denial, metadata `scan_status`, read/quarantine race, replay, corrupt-state fail-closed behavior. |
| 5.5 | `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md` | Admin list/inspect, confirm/release through committed Raft authority, authz, rate limits, audit, redaction, post-release read behavior. |
| 5.6 | `_bmad-output/implementation-artifacts/epic-5-scrapctl-quarantine-operator-workflow-evidence.md` | CLI list/inspect/confirm/release/evidence, admin HTTP routing, typed failures, strict response validation, redacted stdout/stderr/report surfaces. |

### Previous Story Intelligence

- Story 5.6 review fixes matter for closure: transport errors must not wrap URL-bearing errors, HTTP failure bodies must not echo raw identity values, admin success/reason/lifecycle/scan values are allowlisted, success JSON decoding is bounded and strict, filtered empty evidence runs still check operator-supplied filters, unsafe admin base URLs are rejected, and evidence writes are atomic.
- Story 5.5 review fixes matter for closure: auth/rate-limit/method denials are JSON-only, duplicate query parameters are rejected, confirm/release only report committed outcomes, repeated confirm is idempotent, release lifecycle reflects committed state, Shard 0 metadata is bounded, and route-not-found maps to bounded route-unavailable behavior.
- Story 5.4 review fixes matter for closure: the read gate must fail closed before bytes are served, metadata remains available with bounded scan status, and corrupt quarantine state maps to data-loss behavior rather than permission to serve.
- Story 5.3 review fixes matter for closure: scanner detection does not own quarantine authority, detection batches are prevalidated, missing/corrupt metadata fails closed, and scanner progress waits for matching committed apply before advancing.
- Story 5.2 review fixes matter for closure: persisted frontier conflicts reset to duplicate-safe rescan, scanner progress resolves the current Projection after swaps, scanner watermark keys do not perturb replica consistency hashing, and frontier gaps do not advance persisted progress.
- Story 5.1 review fixes matter for closure: scanner work uses stream opener boundaries rather than local path handoff, missing scanner engines are visible, seal notifications occur after seal proposal work, scheduler loops recover into bounded status, and failed Blocks remain visible in lag.

### Implementation Guidance

- Prefer documentation/evidence edits only. If the audit finds a production behavior gap, record `FAIL` or `CONCERNS` in the closure artifact, patch the specific gap with tests, and run BMAD code review before claiming Story 5.7 done.
- The aggregate closure artifact should be concise and machine-checkable enough for Epic 6 release evidence work to reuse. Include tables for source artifact, command, result, gap, and final decision.
- Keep closure artifact statements evidence-backed. Avoid claims such as "covered by tests" unless the exact command, test name, artifact path, and result are listed.
- Use exact glossary terms: Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Content Scanner, Content Quarantine, Block Quarantine.
- Do not add runtime dependencies, new scan engines, cloud malware services, deployment overlays, or operator workflows unless the audit exposes a blocking gap that cannot be closed by evidence alone.
- Do not mark `epic-5` `done` as part of story creation. The dev pass may mark Story 5.7 `done` after closure evidence passes; epic closure should remain a separate explicit workflow decision unless BMAD instructions say otherwise.

### Leak Scan Template

Keep commands in the final artifact, but define sensitive patterns in shell variables so copied commands do not self-match. Adapt the exact scope after implementation.

```bash
scan_scope="_bmad-output/implementation-artifacts/5-[1-7]-*.md _bmad-output/implementation-artifacts/epic-5-*.md internal/avscan internal/quarantine internal/index internal/shard internal/admin internal/scrapctl proto/scrap/v1"
secret_shape_pattern='(?i)(AKIA[0-9A-Z]{16}|xox[baprs]-[0-9A-Za-z-]{10,}|-----BEGIN (RSA|EC|OPENSSH|PRIVATE) KEY-----)'
scanner_sensitive_pattern='(?i)([s]ignature payload|[r]ule source|[c]lamd dependency log|[y]ara dependency log|[r]aw scanner payload|[r]aw document bytes)'
identity_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[b]ackend[_-]?key|[t]race[_-]?id|[r]equest[_-]?id|[f]ile[_-]?path|[a]uth[_-]?claim)'

rg -n --pcre2 "$secret_shape_pattern" $scan_scope
rg -n --pcre2 "$scanner_sensitive_pattern" $scan_scope
rg -n --pcre2 "$identity_sensitive_pattern" $scan_scope
```

Expected final result is no matches, or a documented false-positive table with why each match is safe and not a leak.

## Testing Requirements

Run these gates before marking Story 5.7 done:

```bash
git diff --check
make proto-check
scripts/check-e2e-gates.sh
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If production code changes, add or update focused tests first and rerun the affected package gates before the broad gate. If only BMAD artifacts change, still run the broad gate because Story 5.7 is a closure claim over existing behavior.

## Project Structure Notes

- Expected new artifact: `_bmad-output/implementation-artifacts/epic-5-content-safety-closure-evidence.md`.
- Expected tracking update after dev completion: `_bmad-output/implementation-artifacts/sprint-status.yaml`.
- Production code should remain unchanged unless evidence audit exposes a real gap.
- No conflict with the current unified project structure. Evidence artifacts stay under `_bmad-output/implementation-artifacts`; durable policy changes belong in `docs/` only if the closure policy changes.

## References

- `_bmad-output/planning-artifacts/epics.md` - Epic 5 and Story 5.7 source requirements.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-11, FR-12, NFR-4, NFR-5, and release evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1, authority patterns, redaction/evidence patterns, and package boundaries.
- `CONTEXT.md` - domain vocabulary and Content Scanner / Content Quarantine definitions.
- `docs/adr/0008-async-content-scanning-architecture.md` - accepted scanner/quarantine architecture.
- `docs/adr/0025-content-quarantine-admin-surface.md` - admin HTTP plus `scrapctl` amendment.
- `_bmad-output/project-context.md` - repo package boundaries, testing rules, evidence and redaction rules.
- `_bmad-output/implementation-artifacts/5-6-scrapctl-quarantine-operator-workflow.md` - previous story record and review fixes.

## Dev Agent Record

### Agent Model Used

TBD during dev-story

### Debug Log References

- 2026-06-12T17:18:10-04:00 - Story context created from Epic 5, FR-11/FR-12, DG-1, ADR 0008, ADR 0025, prior Story 5.1-5.6 evidence artifacts, and recent git history.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Story 5.7 is ready for dev and scoped to aggregate closure evidence.

### File List

- `_bmad-output/implementation-artifacts/5-7-content-safety-closure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
