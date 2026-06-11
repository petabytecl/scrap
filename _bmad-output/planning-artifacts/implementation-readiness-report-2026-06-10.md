---
stepsCompleted:
  - step-01-document-discovery
  - step-02-prd-analysis
  - step-03-epic-coverage-validation
  - step-04-ux-alignment
  - step-05-epic-quality-review
  - step-06-final-assessment
readinessStatus: READY
documentsIncluded:
  - type: PRD
    path: _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md
  - type: Architecture
    path: _bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md
  - type: Epics and Stories
    path: _bmad-output/planning-artifacts/epics.md
documentsMissing:
  - type: UX Design
    reason: No UX document found under _bmad-output/planning-artifacts
duplicateResolutions:
  - type: PRD
    selected: _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md
    alternatives:
      - _bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/prd.md
    resolution: User continued after duplicate warning; newest 2026-06-10 PRD selected as the current regenerated artifact.
  - type: Architecture
    selected: _bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md
    alternatives:
      - _bmad-output/planning-artifacts/architecture.md
    resolution: User continued after duplicate warning; newest 2026-06-10 architecture selected as the current regenerated artifact.
---

# Implementation Readiness Assessment Report

**Date:** 2026-06-10
**Project:** scrap

## Step 1: Document Discovery

### PRD Files Found

**Whole Documents:**

- None matching `_bmad-output/planning-artifacts/*prd*.md`.

**Sharded Documents:**

- None matching `_bmad-output/planning-artifacts/*prd*/index.md`.

**PRD Workspace Folders:**

- `_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/`
  - `_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/.decision-log.md` (1,080 bytes, modified 2026-06-07 23:47)
  - `_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/prd.md` (17,811 bytes, modified 2026-06-07 23:46)
  - `_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/review-rubric.md` (3,219 bytes, modified 2026-06-07 23:47)
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/`
  - `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/.decision-log.md` (3,021 bytes, modified 2026-06-10 18:53)
  - `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` (28,877 bytes, modified 2026-06-10 19:18)

### Architecture Files Found

**Whole Documents:**

- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` (31,197 bytes, modified 2026-06-10 19:18)
- `_bmad-output/planning-artifacts/architecture.md` (84,131 bytes, modified 2026-06-08 10:45)

**Sharded Documents:**

- None found.

### Epics & Stories Files Found

**Whole Documents:**

- `_bmad-output/planning-artifacts/epics.md` (73,028 bytes, modified 2026-06-10 20:13)

**Sharded Documents:**

- None found.

### UX Design Files Found

**Whole Documents:**

- None found.

**Sharded Documents:**

- None found.

### Issues Found

- PRD duplicates/candidates were found. The assessment will use `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`; `_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/prd.md` remains an older alternative.
- Architecture duplicates/candidates were found. The assessment will use `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`; `_bmad-output/planning-artifacts/architecture.md` remains an older alternative.
- Warning: UX design document not found. This may reduce assessment completeness if UX-specific acceptance criteria exist outside PRD, architecture, or epics.

### Selected Documents For Assessment

- PRD: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- Architecture: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`
- Epics & Stories: `_bmad-output/planning-artifacts/epics.md`
- UX: none found

## Step 2: PRD Analysis

### Functional Requirements

FR-1: Immutable Document API

Billing services can write, read, head, and find immutable Documents by `(transaction_id, document_name)`.

Consequences:

- Duplicate writes preserve immutability and idempotency boundaries.
- `tenant_id` may be present for future routing but is not storage identity.
- Public delete, overwrite, versioning, and S3-compatible behavior are absent.
- Traceability: `CONTEXT.md`, ADR 0001-0005, Phase 1/2 implementation history.

FR-2: ACK from local replicated durability

S.C.R.A.P. returns write ACK only after the required local and peer durability path, committed metadata, and visibility contract are satisfied.

Consequences:

- Backend upload is not in the write ACK path.
- Document bytes and Block payload bytes do not enter Raft.
- Pebble Projection is derived and rebuildable, not production storage truth.
- Traceability: `CONTEXT.md`, ADR 0001, ADR 0014.

FR-3: All-or-error reads and corruption handling

Reads return verified bytes or a typed failure; S.C.R.A.P. never returns least-bad, partial, or unverified bytes.

Consequences:

- Visible metadata corruption fails closed.
- Block corruption triggers Block Quarantine and repair behavior.
- `DATA_LOSS` is reserved for verified corruption or unrecoverable committed metadata/object mismatch.
- Traceability: `CONTEXT.md`, ADR 0002, ADR 0003, ADR 0014.

FR-4: Raft and peer replication authority

Each required Shard uses Raft metadata authority and peer byte replication so Document visibility is not decided by local files, Backend objects, cached peer addresses, or network location.

Consequences:

- Peer identity includes Cell and Member identity.
- Peer authorization includes Shard scope.
- Projection rebuild uses Raft state and verified Block bytes.
- Traceability: `CONTEXT.md`, ADR 0014, ADR 0024.

FR-5: Multi-Shard startup and routing

V2 implements multi-Shard startup/routing according to fixed hash slots and validated placement config.

Consequences:

- Epics must cover Shard placement, startup, routing, peer authorization scope, evidence, and failure modes.
- Single-Shard remains acceptable for tests/dev but does not satisfy V2 release-ready status.
- Traceability: `CONTEXT.md`, ADR 0024, `internal/cmd/app.go`.
- Decision Gate: DG-2.

FR-6: Backend upload and upload pressure

The Shard leader uploads sealed Blocks to the Backend asynchronously and records upload obligations and confirmations through committed metadata.

Consequences:

- Upload lag can create admission pressure before local durability runway is unsafe.
- Backend inventory, HEAD, or list output is not a consistency oracle.
- Confirmed Upload Catalog is derived and rebuildable.
- Traceability: ADR 0009, ADR 0010.

FR-7: Partial local eviction and full-Block restore

Operators can evict eligible follower-local Block data files and restore full Blocks from the Backend before serving reads that need evicted bytes.

Consequences:

- `.idx` metadata remains local for `HeadDocument` and `FindDocuments`.
- Direct Backend streaming to clients is not part of Phase 4 behavior.
- Missing or corrupt confirmed Backend objects fail closed.
- Traceability: ADR 0016, ADR 0017, ADR 0018.

FR-8: Phase 5 restore-first cold reads

V2 implements restore-first cold reads: when all local `.blk` copies are evicted, `ReadDocument` restores the full Block from the Backend, verifies it, and then serves through the normal local read path.

Consequences:

- Direct Backend ciphertext streaming, range streaming, and per-Frame remote reads are out of V2 unless re-chartered by a later accepted ADR or PRD.
- Backend access must follow committed metadata and explicit restore verification, not inventory/list probes.
- Traceability: `CONTEXT.md`, ADR 0016, ADR 0020.
- Decision Gate: DG-3.

FR-9: Production security mode and surface boundaries

Production `scrapd` startup fails closed when required TLS, role policy, peer identity policy, Transit configuration, or dangerous hook policy is missing, invalid, or contradictory.

Consequences:

- Public, peer, admin, and `scrapctl` paths have separate security handling.
- Non-production mode is explicit and visible, and does not satisfy production readiness.
- Role authorization and peer identity checks run before side effects.
- Traceability: ADR 0019, ADR 0024, issues #399, #401-#404, #430-#434.

FR-10: OpenBao envelope encryption and durable rewrap

Production writes encrypt new Document payload bytes before Block persistence, reads decrypt through the normal path while preserving integrity checks, and operators can durably rewrap envelope metadata through Raft.

Consequences:

- Production writes never fall back to plaintext when Transit is unavailable.
- Frame CRC covers ciphertext bytes; Document SHA-256 verifies plaintext before return.
- Rewrap does not rewrite Block payload bytes and does not leak key material.
- Traceability: ADR 0020, ADR 0021, ADR 0023, issues #400, #405-#407.

FR-11: Async Content Scanner

V2 includes a leader-owned background Content Scanner that scans sealed Block bytes with ClamAV and YARA after ACK and never blocks the write path.

Consequences:

- Scanner unavailability does not block writes.
- Scan progress uses persisted watermarks.
- Scanner work shares I/O budget with Deep Scrub.
- Detection and rescan behavior are observable.
- Traceability: `CONTEXT.md`, ADR 0008.
- Decision Gate: DG-1.

FR-12: Content Quarantine read gate and admin operations

Content Quarantine gates a single Document at metadata level: `ReadDocument` denies bytes, while `HeadDocument` and `FindDocuments` expose metadata with scan status for reconciliation.

Consequences:

- Block bytes are untouched by Content Quarantine.
- Quarantine state is replicated through Raft.
- Operator confirm and release actions are available through the accepted admin surface.
- ADR 0025 amends ADR 0008 so V2 uses existing admin HTTP plus `scrapctl`, not a new gRPC AdminService.
- Traceability: `CONTEXT.md`, ADR 0008, ADR 0019.
- Decision Gate: DG-1.

FR-13: `scrapctl` operational baseline

`scrapctl` supports production-oriented diagnostics and evidence workflows for status, leaders, peers, upload pressure, faults, evidence bundles, and eviction.

Consequences:

- CLI output is actionable and does not leak secrets, raw Document identifiers, raw Backend keys, or unbounded high-cardinality values.
- `scrapctl` is a client/operator path, not a separate storage authority.
- Traceability: ADR 0015, ADR 0016, Phase 4/4.5 implementation docs.

FR-14: `scrapctl` OpenBao bootstrap

V2 implements `scrapctl` OpenBao bootstrap commands for local and prod-like operator workflows.

Consequences:

- Commands initialize, unseal, mount Transit, create the S.C.R.A.P. Transit key through the official OpenBao Go API client, and emit redacted evidence suitable for rehearsal notes.
- Production OpenBao deployment, secret custody, storage backend setup, high-availability topology, and lifecycle remain platform-owned.
- Traceability: `docs/phase-4.5-security-implementation-slices.md`, ADR 0023.
- Decision Gate: DG-4.

FR-15: OTel evidence plane

S.C.R.A.P. emits OpenTelemetry metrics, logs, traces, and profiles sufficient to prove runtime behavior, production safety, and evidence gates.

Consequences:

- New metrics use OTel instruments and low-cardinality attributes.
- Logs use structured `slog` and redact raw Document identifiers, Backend keys, traces, request IDs, secrets, and sensitive dependency text.
- Pprof remains an opt-in admin feature restricted to evidence/operator paths.
- Traceability: ADR 0012, ADR 0013.

FR-16: Major-release evidence and documentation closure

V2 release-ready status requires linked, current, reviewable evidence and operator documentation for every required release claim.

Consequences:

- Required evidence includes Tier 2 prod-like Cilium gate, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3.
- Operator runbooks, alert/query references, incident workflows, and evidence instructions are required unless explicitly de-scoped.
- Issue #429 remains a final gate after feature scope is complete.
- Traceability: ADR 0012, ADR 0015, `docs/prd-closure-policy.md`, `docs/production-rehearsal.md`, issue #429.
- Decision Gate: DG-5.

Total FRs: 16.

### Non-Functional Requirements

NFR-1: Fail-closed security and storage behavior

Missing security config, missing key material, corrupt metadata, corrupt Block bytes, invalid Backend restore, unauthorized peer/admin/public access, and unsafe production hooks fail closed.

NFR-2: Bounded memory and streaming

Production paths do not buffer full Documents, Blocks, uploads, restores, peer transfers, or scans in memory.

NFR-3: Authority separation

Raft is metadata authority; Pebble Projection, Confirmed Upload Catalog, Backend objects, Local Block Lifecycle, audit, and OTel evidence are not storage truth.

NFR-4: Privacy and redaction

Logs, metrics, traces, audit, evidence, screenshots, fixtures, and public tracker comments do not leak secrets, Document bytes, raw identifiers, Backend keys, data keys, or wrapped-key ciphertext.

NFR-5: Operational evidence

Each release claim has current evidence with command, commit/ref, environment, expected result, actual result, artifact path, and redaction proof.

NFR-6: Compatibility discipline

Storage format, wire protocol, dependency/runtime choices, security/encryption/auth contracts, and cross-package ownership changes require ADRs.

NFR-7: Test coverage by risk

Required features need positive path, fail-closed path, restart/rebuild or recovery path where relevant, and the narrowest local/deployed gate that proves the claim.

Total NFRs: 7.

### Additional Requirements

Source precedence:

- Apply source precedence in this order when sources conflict: `CONTEXT.md`, accepted ADRs, master PRD and `docs/v2-scope-reconciliation.md`, GitHub Issues/milestones, older BMAD artifacts/historical phase documents, then V1 reference materials.

Release rules:

- V2 is the major release line.
- There are no intermediate V2 releases.
- Implementation phases are sequencing labels, not release boundaries.
- Closed issues and a closed milestone do not prove release completeness.
- Release-ready requires all required V2 features, documentation, and evidence gates to be complete.
- Accepted ADR scope is required unless explicitly superseded.

Blocking decision gates:

- DG-1: Content Scanner and Content Quarantine stay in V2; existing admin HTTP plus `scrapctl` is the accepted admin surface, closed by ADR 0025.
- DG-2: Multi-Shard startup/routing is required for V2 release-ready status, closed by ADR 0026.
- DG-3: Phase 5 cold reads use restore-first full-Block restore; direct Backend ciphertext streaming is out of V2, closed by ADR 0027.
- DG-4: `scrapctl` owns local/prod-like OpenBao bootstrap helper workflows only, with production OpenBao lifecycle platform-owned.
- DG-5: Runbooks, alert/query references, evidence matrix, and closure policy updates are required for release documentation/evidence.

Acceptance and evidence requirements:

- Core Document API/read-write behavior: unit, integration, and e2e evidence for ACK, duplicate handling, all-or-error reads, and metadata visibility.
- Raft/peer replication: multi-voter replication, restart/rebuild, not-leader, peer authorization, and transfer failure evidence.
- Backend upload and restore: upload confirmation, Confirmed Upload Catalog, restore success, and missing/corrupt Backend object failure evidence.
- Eviction: dry-run, apply, marker/unlink, restore, validation, and local lifecycle health evidence.
- Production security: startup fail-closed, mTLS, authz, peer identity, audit, rate limits, and denied-operation evidence.
- OpenBao encryption and rewrap: encrypted write/read, Transit outage, missing key, auth denied, durable rewrap, and no-key-leakage evidence.
- Content Scanner and Quarantine: scanner scheduling, quarantine command, read denial, metadata scan status, confirm/release, scanner outage, and rescan evidence.
- Phase 5 cold reads: restore-first behavior, Backend failures, encryption interaction, telemetry, and cancellation evidence.
- Multi-Shard routing: hash-slot routing, startup membership, Shard-scope auth, and failure-domain behavior evidence.
- `scrapctl` OpenBao bootstrap: idempotency, error handling, redacted output, and official OpenBao API client usage evidence.
- Documentation and release closure: runbooks, alerts/query references, incident workflows, evidence index, and closure checklist.
- Real S3/IAM Backend proof: linked report from real non-local S3/IAM production rehearsal.

Non-goals unless re-chartered:

- S3-compatible API.
- Public deletion API.
- `tenant_id` as storage identity.
- Tenant-specific key policy.
- Tenant quota authority.
- Cell federation.
- Metadata encryption.
- Hot certificate reload.
- Transparent migration for old unencrypted development Blocks.
- Cloud malware scanning services.
- Backend inventory as read/write consistency oracle.

Open questions:

- OQ-1: Whether issue #429 should be linked to a new master V2 release parent issue after this PRD is accepted.
- OQ-2: Whether the older deleted `_bmad-output/planning-artifacts/epics.md` should be restored for history or replaced entirely by regenerated master V2 epics.

Downstream workflow:

- Regenerate epics/stories from the master PRD, master V2 architecture, ADR 0025, ADR 0026, ADR 0027, and current GitHub tracker state.
- Run implementation readiness against regenerated PRD, architecture, and epics.
- Run sprint planning to regenerate implementation tracking.
- Implement stories with test-first evidence and GitHub tracker alignment.
- Run final release evidence only after all feature scope is complete.

### PRD Completeness Assessment

The selected PRD is comprehensive for release-scope analysis: it defines target users, key journeys, glossary constraints, source precedence, release rules, 16 FRs, 7 NFRs, blocking decision gates, non-goals, acceptance evidence, success metrics, and downstream workflow. It also explicitly separates release readiness from closed issue count or phase completion.

Material cautions for later validation:

- The PRD frontmatter still says `status: draft`.
- Two open questions remain, but neither appears to block implementation-readiness validation directly if the regenerated epics carry the master-scope requirements.
- UX-specific source material is missing; the PRD itself contains user journeys but not a separate UX design artifact.
- This readiness pass must verify that the current `epics.md` is the regenerated master V2 artifact and not the older Phase 4.5 backlog.

## Step 3: Epic Coverage Validation

### Epic FR Coverage Extracted

- FR-1: Covered in Epic 1, with story coverage in Stories 1.2, 1.4, and 1.5.
- FR-2: Covered in Epic 1, with story coverage in Stories 1.1 and 1.5.
- FR-3: Covered in Epic 1, with story coverage in Stories 1.3 and 1.5.
- FR-4: Covered in Epic 2, with story coverage in Stories 2.2, 2.4, and 2.6.
- FR-5: Covered in Epic 2, with story coverage in Stories 2.1, 2.2, 2.3, 2.4, 2.5, and 2.6.
- FR-6: Covered in Epic 3, with story coverage in Stories 3.1, 3.2, and 3.7.
- FR-7: Covered in Epic 3, with story coverage in Stories 3.3 and 3.7.
- FR-8: Covered in Epic 3, with story coverage in Stories 3.4, 3.5, 3.6, and 3.7.
- FR-9: Covered in Epic 4, with story coverage in Stories 4.1, 4.2, and 4.7.
- FR-10: Covered in Epic 4, with story coverage in Stories 4.3, 4.4, and 4.7.
- FR-11: Covered in Epic 5, with story coverage in Stories 5.1, 5.2, 5.3, and 5.7.
- FR-12: Covered in Epic 5, with story coverage in Stories 5.3, 5.4, 5.5, 5.6, and 5.7.
- FR-13: Covered in Epic 6, with story coverage in Story 6.4.
- FR-14: Covered in Epic 4, with story coverage in Stories 4.5, 4.6, and 4.7.
- FR-15: Covered in Epic 6, with story coverage in Stories 6.3, 6.4, and 6.5.
- FR-16: Covered in Epic 6, with story coverage across Stories 6.1 through 6.7.

Total FRs in epics: 16.

### Coverage Matrix

| FR Number | PRD Requirement | Epic Coverage | Status |
| --- | --- | --- | --- |
| FR-1 | Immutable Document API | Epic 1; Stories 1.2, 1.4, 1.5 | Covered |
| FR-2 | ACK from local replicated durability | Epic 1; Stories 1.1, 1.5 | Covered |
| FR-3 | All-or-error reads and corruption handling | Epic 1; Stories 1.3, 1.5 | Covered |
| FR-4 | Raft and peer replication authority | Epic 2; Stories 2.2, 2.4, 2.6 | Covered |
| FR-5 | Multi-Shard startup and routing | Epic 2; Stories 2.1, 2.2, 2.3, 2.4, 2.5, 2.6 | Covered |
| FR-6 | Backend upload and upload pressure | Epic 3; Stories 3.1, 3.2, 3.7 | Covered |
| FR-7 | Partial local eviction and full-Block restore | Epic 3; Stories 3.3, 3.7 | Covered |
| FR-8 | Phase 5 restore-first cold reads | Epic 3; Stories 3.4, 3.5, 3.6, 3.7 | Covered |
| FR-9 | Production security mode and surface boundaries | Epic 4; Stories 4.1, 4.2, 4.7 | Covered |
| FR-10 | OpenBao envelope encryption and durable rewrap | Epic 4; Stories 4.3, 4.4, 4.7 | Covered |
| FR-11 | Async Content Scanner | Epic 5; Stories 5.1, 5.2, 5.3, 5.7 | Covered |
| FR-12 | Content Quarantine read gate and admin operations | Epic 5; Stories 5.3, 5.4, 5.5, 5.6, 5.7 | Covered |
| FR-13 | `scrapctl` operational baseline | Epic 6; Story 6.4 | Covered |
| FR-14 | `scrapctl` OpenBao bootstrap | Epic 4; Stories 4.5, 4.6, 4.7 | Covered |
| FR-15 | OTel evidence plane | Epic 6; Stories 6.3, 6.4, 6.5 | Covered |
| FR-16 | Major-release evidence and documentation closure | Epic 6; Stories 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7 | Covered |

### Missing Requirements

No PRD FRs are missing from the current regenerated epics document.

### Extra FRs In Epics Not Found In PRD

None. The epics document repeats the same FR-1 through FR-16 inventory as the selected PRD and does not introduce an additional numbered FR outside that set.

### Coverage Statistics

- Total PRD FRs: 16.
- FRs covered in epics: 16.
- Coverage percentage: 100%.

### Coverage Notes

- The current `epics.md` is the regenerated master V2 artifact: its frontmatter identifies the master PRD, master V2 architecture artifact, `docs/v2-scope-reconciliation.md`, and ADR 0025 through ADR 0027 as inputs.
- This step confirms traceability presence only. Story quality, vertical slicing, dependency order, and acceptance criteria quality remain for later steps.

## Step 4: UX Alignment Assessment

### UX Document Status

Not found. No standalone UX document exists under `_bmad-output/planning-artifacts` using the workflow search patterns:

- `_bmad-output/planning-artifacts/*ux*.md`
- `_bmad-output/planning-artifacts/*ux*/index.md`

The current `epics.md` also records that no UX Design document was found and that no UI-specific UX Design requirements were extracted.

### UX/UI Implication Assessment

No customer-facing web, mobile, dashboard, or frontend application is implied by the selected PRD, architecture, or epics. The product surface is a backend gRPC storage gateway plus operator/admin workflows.

UX-adjacent requirements are still present through operator-facing surfaces:

- `scrapctl` diagnostics and evidence workflows.
- `scrapctl openbao bootstrap`.
- Admin HTTP quarantine operations.
- `scrapctl` quarantine operator workflows.
- Operator runbooks.
- Alert/query references.
- Evidence bundle output and redaction reporting.

### Alignment Issues

No standalone UX-to-PRD or UX-to-architecture alignment check can be performed because no UX document exists.

The implied operator UX is represented in the PRD and epics:

- FR-13 covers `scrapctl` operational diagnostics and evidence workflows.
- FR-14 covers `scrapctl openbao bootstrap`.
- FR-16 covers operator documentation, evidence, and closure.
- Epic 2 covers Shard-aware admin and `scrapctl` diagnostics.
- Epic 4 covers OpenBao bootstrap workflows.
- Epic 5 covers admin HTTP and `scrapctl` quarantine workflows.
- Epic 6 covers runbooks, alert/query references, and release evidence bundles.

The selected architecture also accounts for the implied operator UX by naming `internal/admin`, `internal/scrapctl`, quarantine admin endpoints, OpenBao bootstrap commands, runbook-oriented health/status, and V2 evidence matrix/runbook outputs.

### Warnings

- Warning: No dedicated UX artifact exists. This is acceptable for implementation readiness only if operator CLI/admin workflow behavior remains specified in PRD, architecture, epics, and story acceptance criteria.
- Warning: Operator-facing usability risks should be handled through story acceptance criteria for command output, exact glossary terminology, redaction, actionable errors, runbook validation, and evidence artifact structure.
- Warning: If a future web UI, mobile UI, dashboard, or human-facing console enters scope, implementation readiness should be re-run with a UX design artifact.

## Step 5: Epic Quality Review

### Scope Reviewed

- Epics reviewed: 6.
- Stories reviewed: 39.
- Requirements coverage reviewed: FR-1 through FR-16.
- Architecture starter-template check: no new starter template is selected; the existing Go/gRPC S.C.R.A.P. V2 repository is the brownfield foundation.

### Epic Structure Validation

| Epic | User Value Focus | Independence Result | Notes |
| --- | --- | --- | --- |
| Epic 1: Billing ETL Can Trust Immutable Document Writes and Reads | Pass | Pass | Delivers direct billing-service value around write ACK, immutable identity, read/head/find, restart/rebuild, and redaction. |
| Epic 2: Operators Can Run a Shard-Aware Cell | Pass | Pass | Builds on core gateway behavior and adds multi-Shard startup/routing, Shard-scoped peer auth, and diagnostics. No dependency on later epics found. |
| Epic 3: Operators Can Prove Backend Durability and Restore-First Cold Reads | Pass | Pass with caution | Delivers storage-operator value around upload, pressure, eviction, restore, failure mapping, and cold-read closure. Story 3.6 intentionally limits final production OpenBao proof to Epic 4; this must remain explicit. |
| Epic 4: Operators Can Run Fail-Closed Security and OpenBao Workflows | Pass | Pass | Delivers security/operator value. It does not require Epic 5 or Epic 6 to function. |
| Epic 5: Security Operators Can Contain Unsafe Content Without Mutating Documents | Pass | Pass | Delivers security-operator value around scanner, Content Quarantine, admin HTTP, `scrapctl`, and closure evidence. It appropriately builds on the earlier security/admin boundary. |
| Epic 6: Release Owners Can Prove V2 Readiness | Pass | Pass as final aggregate epic | This is a release-owner value epic. It depends on earlier feature evidence by design and explicitly avoids introducing substitute feature behavior. |

### Story Quality Assessment

Strengths:

- Every story is written as a user story with an actor, goal, and outcome.
- Every story carries `Requirements:` traceability to PRD FRs.
- Acceptance criteria use Given/When/Then structure throughout.
- Most stories include failure-path, redaction, evidence command, and changed-boundary expectations.
- Closure stories use PASS/CONCERNS/FAIL language and prevent silent evidence deferral.
- Brownfield integration is explicit: stories name package boundaries such as `internal/cmd`, `internal/server`, `internal/peer`, `internal/shard`, `internal/admin`, `internal/scrapctl`, `internal/avscan`, and docs/evidence surfaces.

### Dependency Analysis

No critical forward dependency was found.

Acceptable dependency patterns:

- Story 2.3 references Stories 2.1 and 2.2 as prerequisites; those are earlier stories in the same epic.
- Epic 6 depends on prior feature evidence; this is valid because it is the final release-owner aggregate epic and explicitly avoids creating replacement product behavior.
- Story 4.3 defers operational rehearsal evidence to Story 4.7, a later story in the same epic; this is acceptable because the crypto-path tests still remain in Story 4.3.

Dependency caution:

- Story 3.6 AC-3.6.4 marks final production OpenBao interaction as release evidence owned by Epic 4. This is acceptable only because Story 3.6 still requires encryption-compatible restore behavior through existing fixtures or a test envelope adapter. Epic 3 closure must not claim final production OpenBao proof from future Epic 4 work.

### Database/Entity Creation Timing

No relational database or upfront schema-creation anti-pattern applies. State-bearing changes are scoped to the feature that needs them:

- Content Scanner/Quarantine state is introduced with scanner/quarantine stories.
- Multi-Shard routing state is introduced with routing/startup stories.
- Restore/cold-read lifecycle state is introduced with restore stories.
- Evidence matrix/runbooks are introduced with release-evidence stories.

### Starter Template and Brownfield Check

- No starter template is selected.
- No greenfield setup story is required.
- Brownfield integration points are present and explicit.
- Generated proto files are treated as acceptance artifacts only; the epics warn not to hand-edit them.

### Critical Violations

None found.

### Major Issues

None found.

### Minor Concerns

1. Story 4.2 combines authorization, audit, and rate limits across public, peer, admin, and dangerous-operation surfaces. The ACs are specific enough for readiness, but the implementation story may need to be split by surface if it stops being independently completable.

2. Stories 6.2, 6.3, and 6.4 cover broad documentation/evidence surfaces: runbooks, alert/query references, and evidence bundle behavior. They are acceptable as release-owner stories, but execution should keep them checklist-driven or split them if a single story becomes too large.

3. Story 3.6 includes a forward-looking reference to Epic 4 production OpenBao proof. The wording currently contains the boundary, but implementation planning must preserve that boundary so Epic 3 does not close from future Epic 4 evidence.

4. The missing standalone UX artifact leaves operator-facing usability to story ACs. This is acceptable for backend/operator scope but should remain visible during story creation.

### Recommendations

- Keep the six-epic structure.
- Preserve Epic 6 as an aggregation and release-gating epic; do not move feature behavior into Epic 6.
- During `bmad-sprint-planning` and `bmad-create-story`, split Story 4.2 or Epic 6 documentation/evidence stories if the generated implementation story cannot be completed and verified independently.
- Preserve AC-3.6.4's fixture-vs-production-proof distinction when creating implementation stories.
- Ensure every generated story keeps its evidence command, changed-boundary list, redaction proof, and failure-path tests.

## Step 6: Summary and Recommendations

### Overall Readiness Status

READY.

This status means the current master V2 PRD, master V2 architecture artifact, and regenerated `epics.md` are ready to feed implementation planning. It does not mean V2 is release-ready. V2 release closure still requires implementation, evidence, runbooks, rehearsals, and final release gates.

### Evidence Supporting READY

- PRD extraction found 16 FRs and 7 NFRs.
- Epic coverage validation found 16 of 16 PRD FRs covered.
- Coverage percentage: 100%.
- No PRD FRs were missing from the epics.
- No extra numbered FRs were introduced by the epics.
- The selected `epics.md` is the regenerated master V2 artifact, not the older Phase 4.5 backlog.
- The epic structure is user/outcome-oriented, not technical-milestone-oriented.
- No critical forward dependency was found.
- No major story-quality issue was found.
- The architecture confirms no new starter template is selected; this is a brownfield continuation of the existing Go/gRPC S.C.R.A.P. V2 repository.

### Critical Issues Requiring Immediate Action

None.

### Major Issues Requiring Immediate Action

None.

### Warnings and Minor Concerns

1. Duplicate historical/current planning artifacts remain in the planning folder. This report selected the 2026-06-10 master V2 PRD and architecture, but the older Phase 4.5 PRD and architecture remain nearby and can confuse future automation or human readers.

2. No standalone UX artifact exists. This is acceptable for backend/operator scope, but operator-facing usability must remain covered by story ACs for `scrapctl`, admin HTTP, runbooks, evidence output, redaction, and actionable errors.

3. The selected PRD frontmatter still says `status: draft`. The artifact is complete enough for implementation-readiness validation, but downstream process should either accept that draft status intentionally or update it when the master PRD is approved.

4. Story 4.2 is dense: authorization, audit, rate limits, and multiple surfaces are combined. Split it during story creation/execution if it cannot remain independently completable.

5. Stories 6.2, 6.3, and 6.4 are broad release-evidence/documentation stories. Keep them checklist-driven or split them by runbook/alert/evidence-bundle area if execution becomes too large.

6. Story 3.6 references final production OpenBao proof owned by Epic 4. This is acceptable only if Story 3.6 keeps its own encryption-compatible restore proof limited to fixtures/adapters and does not claim future Epic 4 production proof.

### Recommended Next Steps

1. Run `bmad-sprint-planning` against the regenerated master V2 `epics.md` before creating or executing implementation stories.

2. In sprint planning, preserve the current epic order: core Document contract, multi-Shard Cell, Backend/restore-first cold reads, security/OpenBao, Content Scanner/Quarantine, then release evidence closure.

3. Treat Story 4.2 and the broad Epic 6 stories as split candidates during story creation if a single implementation slice cannot produce focused tests and evidence.

4. Keep the final release/evidence distinction explicit: implementation can start, but V2 release closure must wait for feature completion, Tier 2/Tier 3 evidence, production security rehearsal, real S3/IAM rehearsal, runbooks, alert/query references, and closure matrix proof.

5. Optionally archive or rename older Phase 4.5 planning artifacts so future automation does not confuse them with the selected master V2 artifacts.

### Final Note

This assessment identified 0 critical issues, 0 major issues, and 6 warnings/minor concerns across artifact hygiene, UX documentation, PRD status, story sizing, and evidence-boundary categories. The planning set is ready for implementation planning, with the cautions above carried into sprint planning and story generation.

**Assessor:** Codex using `bmad-check-implementation-readiness`

**Completed:** 2026-06-10
