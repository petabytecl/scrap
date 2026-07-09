---
title: S.C.R.A.P. V2 Master Release Scope
status: draft
created: 2026-06-10
updated: 2026-06-10
project: scrap
release_line: v2
github_repository: petabytecl/scrap
github_milestone: storage-gateway-v2
source_reconciliation: docs/archive/obsolete-pre-bmad/scope-reconciliation.md
labels:
  - prd
  - v2
  - release-scope
  - production-readiness
---

# PRD: S.C.R.A.P. V2 Master Release Scope

## 0. Document Purpose

This PRD is the canonical planning baseline for the S.C.R.A.P. V2 major
release. It reconciles `CONTEXT.md`, accepted ADRs, prior BMAD artifacts,
current GitHub tracker state, and `docs/archive/obsolete-pre-bmad/scope-reconciliation.md` after the
release rule was clarified: V2 has no intermediate releases, and V2 is not
release-ready until all required V2 features and evidence gates are complete.

This document does not replace accepted ADRs. It defines release scope,
decision gates, functional requirements, non-goals, acceptance evidence, and
the required downstream workflow order. Architecture details that require new
or superseding ADRs are captured as blocking gates rather than silently decided
here.

Current source-state note: the previous Phase 4.5 PRD remains at
`_bmad-output/planning-artifacts/prds/prd-scrap-2026-06-07/prd.md`; the current
working tree has `_bmad-output/planning-artifacts/epics.md` deleted and this
master PRD is intended to be the source for regenerated epics after decision
gates are resolved.

## 1. Vision

S.C.R.A.P. V2 is a production-ready, transaction-scoped Document storage gateway
for billing ETL workflows. Billing services write and read immutable Documents
through a gRPC API while S.C.R.A.P. owns the hard storage guarantees: ACK only
after local replicated durability, metadata authority through Raft, rebuildable
Pebble Projection state, verified Block and Frame integrity, asynchronous
Backend durability, and operator-visible repair, restore, security, encryption,
and evidence behavior.

The V2 release must avoid the V1 failure mode where spike work blurred into
production before the specification was complete. Closed phase milestones and
merged issues are evidence inputs, not release closure. The release is complete
only when the required product behavior, security posture, operator workflows,
documentation, and current linked evidence all line up.

This PRD therefore treats scope closure as explicit release-blocking gates.
Content Scanner and Content Quarantine, multi-Shard release boundaries, Phase 5
cold-read behavior, `scrapctl` OpenBao bootstrap, operator runbooks, and final
S3/IAM rehearsal evidence cannot be hidden under "later release" because there
is no intermediate V2 release.

## 2. Target Users

### 2.1 Jobs To Be Done

- Billing service engineers need immutable Documents to be written and read by
  `(transaction_id, document_name)` with strong read-after-write behavior and
  typed failure modes.
- Platform operators need a Cell that can be started, diagnosed, secured,
  repaired, restored, and rehearsed without relying on undocumented scripts or
  raw logs.
- Storage engineers need clear authority boundaries between Raft, Pebble
  Projection, Block files, Backend objects, OpenBao Transit, audit events, and
  evidence bundles.
- Security reviewers need proof that production mTLS, authorization, audit,
  rate limits, encryption, rewrap, quarantine, and secret redaction work under
  positive and negative cases.
- Release owners need a traceable V2 closure rule that separates implemented
  slices, deferred decisions, and final release evidence.

### 2.2 Non-Users

- Public S3 clients are not target users; S.C.R.A.P. is not an S3-compatible
  API.
- External malware-analysis services are not target users; Content Scanner
  scope, if kept, is self-hosted ClamAV plus YARA.
- Tenant billing, tenant quota, and tenant-specific key-policy owners are not
  target users for this V2 release unless V2 is re-chartered.
- Cell federation owners are not target users for this V2 release unless V2 is
  re-chartered.

### 2.3 Key User Journeys

- **UJ-1. Davi writes and reads billing Documents through the gateway.** Davi,
  a billing service engineer, streams one or more Documents for a Transaction to
  the public gRPC API. S.C.R.A.P. validates boundaries, writes Block bytes
  without whole-Document heap buffering, replicates required bytes, commits
  metadata through Raft, and returns ACK only when the Document is visible. Later
  reads either return fully verified bytes or a typed all-or-error failure.

- **UJ-2. Olivia proves a Cell is production-ready.** Olivia, a platform
  operator, deploys a prod-like Cell, runs `scrapctl` diagnostics and evidence
  commands, reviews Tier 2, Tier 3, security rehearsal, and S3/IAM rehearsal
  artifacts, and sees each required release claim tied to command output,
  commit/ref, redaction proof, and known gaps.

- **UJ-3. Mara handles local Block eviction and restore.** Mara, an operator,
  approves a bounded eviction campaign for uploaded Blocks, confirms local
  lifecycle markers and telemetry, and later observes `ReadDocument` restoring
  a full Block from the Backend before serving verified bytes. If the Backend is
  missing or corrupt, S.C.R.A.P. fails closed without returning partial bytes.

- **UJ-4. Inez responds to malicious content.** Inez, a security operator,
  inspects a Content Quarantine hit, confirms or releases the affected Document,
  and verifies that `ReadDocument` denies bytes while `HeadDocument` and
  `FindDocuments` preserve reconciliation metadata.

- **UJ-5. Rafa rotates encryption material.** Rafa, a security engineer,
  verifies OpenBao Transit readiness, triggers durable rewrap for encrypted
  Documents, and confirms that all Members converge on new envelope metadata
  through Raft without rewriting Block payload bytes or leaking key material.

## 3. Glossary

Use `CONTEXT.md` as the glossary authority. This PRD repeats only the terms
needed for release scope and traceability.

- **Document** - Immutable file stored in S.C.R.A.P., addressed by
  `(transaction_id, document_name)`.
- **Transaction** - Group of 2-7 related Documents from a billing workflow step.
- **Block** - Append-only file containing Documents as sequential Frames.
- **Frame** - Contiguous chunk of Document bytes inside a Block; CRC-32C covers
  stored bytes.
- **Shard** - Independent Raft group managing a subset of Transactions.
- **Cell** - Complete S.C.R.A.P. deployment identified by permanent `cell_id`.
- **Member** - Storage node within a Cell, identified by `cell_id`,
  `member_hostname`, and durable `member_id`.
- **Backend** - Cloud object store providing cold durability for sealed Blocks;
  not in the write ACK path.
- **Pebble Projection** - Rebuildable read-side metadata projection.
- **Projection Resolution** - Read-side process that resolves visible Pebble
  Projection entries into Document metadata and fail-closed read behavior.
- **Upload Outbox** - Per-Shard durable record of sealed Blocks pending Backend
  upload.
- **Confirmed Upload Catalog** - Derived per-Shard record of Blocks whose
  Backend upload has been confirmed by committed Raft state.
- **Local Block Lifecycle** - Per-Member filesystem evidence about one local
  Block copy.
- **Block Quarantine** - Filesystem-level isolation of corrupt Block files.
- **Content Quarantine** - Metadata-level gate on one Document flagged by the
  Content Scanner.
- **Content Scanner** - Background leader-owned scanner for sealed Block bytes,
  using ClamAV and YARA per ADR 0008.
- **OpenBao Transit** - External key service used for envelope encryption and
  durable rewrap.

## 4. Source Precedence and Release Rules

### 4.1 Source Precedence

When sources conflict, downstream workflows must apply this order:

1. `CONTEXT.md` glossary and durable V2 constraints.
2. Accepted ADRs in `docs/adr/`, unless superseded by a later accepted ADR.
3. This master PRD and `docs/archive/obsolete-pre-bmad/scope-reconciliation.md`.
4. GitHub Issues and milestones.
5. Older BMAD artifacts and historical phase documents.
6. V1 materials as reference only.

### 4.2 Release Rules

- V2 is the major release line.
- There are no intermediate V2 releases.
- Implementation phases are sequencing labels, not release boundaries.
- Closed issues and a closed milestone do not prove release completeness.
- Release-ready requires all required V2 features, documentation, and evidence
  gates to be complete.
- Any confirmed or plausible data-integrity bug is a non-waivable release
  blocker until fixed or explicitly disproven with current evidence. This
  includes bugs that can return unverified Document bytes, ACK unsafe state,
  corrupt or fork replicated state, silently drop Raft authority messages,
  bypass committed metadata authority, publish partial restored Blocks, or let
  release evidence contradict the actual data-integrity status.
- Accepted ADR scope is required unless explicitly superseded.
- Unresolved thermo-nuclear High/Medium findings (`H-01`–`H-19`, `M-01`–`M-12`)
  keep final release status at FAIL until each finding maps to accepted story
  evidence on the exact release SHA.
- Stale evidence (commit/ref mismatch), contradictory PASS/FAIL artifacts,
  failing `make static`, and failing `make vuln` are non-waivable blockers.

### 4.3 Production Authority Gates

Production readiness additionally requires these explicit fail-closed gates:

- **Voter count:** production Cells must declare explicit multi-voter membership
  before readiness or write admission (finding `H-09`).
- **Shared Backend:** production multi-Member or eviction-enabled Cells must use
  an explicitly shared durable Backend; Member-local filesystem Backend is
  rejected (`H-10`).
- **Placement identity:** Cell-wide placement identity is persisted; slot map
  changes that would remap existing Transactions are rejected until a
  coordinated Shard-transfer protocol completes (`H-11`).
- **Peer-to-Raft identity binding:** authenticated peer principals must map to
  the Raft sender ID before message routing (`H-12`).
- **Stored Transit identity:** unwrap/rewrap routes through allow-listed
  envelope mount/key identity; Rewrap is monotonic (`H-18`, `M-06`).
- **Scanner-engine composition:** production Content Scanner composition must
  wire a real plaintext engine/signature provider (`M-05`).
- **Exact-SHA release evidence:** Tier 2, Tier 3, real S3/IAM, and closure
  artifacts must cite the exact candidate SHA with freshness checks (`H-19`).

## 5. Current Tracker Snapshot

Verified on 2026-06-10:

- Branch: `v2`.
- GitHub milestone `storage-gateway-v2`: open milestone, `0` open issues,
  `110` closed issues.
- Open v2 issue found: `#429 Pre-v2 release: capture real S3/IAM production
  rehearsal evidence`.
- Local working tree before this PRD creation included a deleted
  `_bmad-output/planning-artifacts/epics.md` and untracked
  `docs/archive/obsolete-pre-bmad/scope-reconciliation.md`.

The tracker snapshot is progress evidence, not closure evidence.

## 6. Features and Functional Requirements

### 6.1 Core Document Gateway Contract

**Description:** S.C.R.A.P. stores immutable Documents grouped by Transaction
and hides whether bytes are hot locally, available from peers, or restored from
the Backend. The public API remains gRPC DocumentService, not an S3-compatible
surface. Realizes UJ-1.

#### FR-1: Immutable Document API

Billing services can write, read, head, and find immutable Documents by
`(transaction_id, document_name)`.

**Consequences:**

- Duplicate writes preserve immutability and idempotency boundaries.
- `tenant_id` may be present for future routing but is not storage identity.
- Public delete, overwrite, versioning, and S3-compatible behavior are absent.

**Traceability:** `CONTEXT.md`, ADR 0001-0005, Phase 1/2 implementation history.

#### FR-2: ACK from local replicated durability

S.C.R.A.P. returns write ACK only after the required local and peer durability
path, committed metadata, and visibility contract are satisfied.

**Consequences:**

- Backend upload is not in the write ACK path.
- Document bytes and Block payload bytes do not enter Raft.
- Pebble Projection is derived and rebuildable, not production storage truth.

**Traceability:** `CONTEXT.md`, ADR 0001, ADR 0014.

#### FR-3: All-or-error reads and corruption handling

Reads return verified bytes or a typed failure; S.C.R.A.P. never returns
least-bad, partial, or unverified bytes.

**Consequences:**

- Visible metadata corruption fails closed.
- Block corruption triggers Block Quarantine and repair behavior.
- `DATA_LOSS` is reserved for verified corruption or unrecoverable committed
  metadata/object mismatch.

**Traceability:** `CONTEXT.md`, ADR 0002, ADR 0003, ADR 0014.

### 6.2 Replication, Shard Authority, and Topology

**Description:** S.C.R.A.P. uses Shards as independent Raft authorities. Current
core types and protocols carry `shard_id`, while the current `scrapd`
composition wires a single Shard ID `0`. V2 release scope now requires
multi-Shard startup/routing per ADR 0026. Realizes UJ-1 and UJ-2.

#### FR-4: Raft and peer replication authority

Each required Shard uses Raft metadata authority and peer byte replication so
Document visibility is not decided by local files, Backend objects, cached peer
addresses, or network location.

**Consequences:**

- Peer identity includes Cell and Member identity.
- Peer authorization includes Shard scope.
- Projection rebuild uses Raft state and verified Block bytes.

**Traceability:** `CONTEXT.md`, ADR 0014, ADR 0024.

#### FR-5: Multi-Shard startup and routing

V2 implements multi-Shard startup/routing according to fixed hash slots and
validated placement config.

**Consequences:**

- Epics must cover Shard placement, startup, routing, peer authorization scope,
  evidence, and failure modes.
- Single-Shard remains acceptable for tests/dev but does not satisfy V2
  release-ready status.

**Traceability:** `CONTEXT.md`, ADR 0024, `internal/cmd/app.go`.

**Decision Gate:** DG-2.

### 6.3 Backend Upload, Eviction, Restore, and Cold Reads

**Description:** Sealed Blocks upload asynchronously to the Backend through the
Upload Outbox and Confirmed Upload Catalog. Phase 4 implemented partial local
eviction and full-Block restore. V2 includes the Phase 5 restore-first cold-read
product contract per ADR 0027. Realizes UJ-3.

#### FR-6: Backend upload and upload pressure

The Shard leader uploads sealed Blocks to the Backend asynchronously and records
upload obligations and confirmations through committed metadata.

**Consequences:**

- Upload lag can create admission pressure before local durability runway is
  unsafe.
- Backend inventory, HEAD, or list output is not a consistency oracle.
- Confirmed Upload Catalog is derived and rebuildable.

**Traceability:** ADR 0009, ADR 0010.

#### FR-7: Partial local eviction and full-Block restore

Operators can evict eligible follower-local Block data files and restore full
Blocks from the Backend before serving reads that need evicted bytes.

**Consequences:**

- `.idx` metadata remains local for `HeadDocument` and `FindDocuments`.
- Direct Backend streaming to clients is not part of Phase 4 behavior.
- Missing or corrupt confirmed Backend objects fail closed.

**Traceability:** ADR 0016, ADR 0017, ADR 0018.

#### FR-8: Phase 5 restore-first cold reads

V2 implements restore-first cold reads: when all local `.blk` copies are evicted,
`ReadDocument` restores the full Block from the Backend, verifies it, and then
serves through the normal local read path.

**Consequences:**

- Direct Backend ciphertext streaming, range streaming, and per-Frame remote
  reads are out of V2 unless re-chartered by a later accepted ADR or PRD.
- Backend access must follow committed metadata and explicit restore
  verification, not inventory/list probes.

**Traceability:** `CONTEXT.md`, ADR 0016, ADR 0020.

**Decision Gate:** DG-3.

### 6.4 Production Security and Encryption

**Description:** V2 production mode must fail closed across startup gates, mTLS,
authorization, peer identity, audit, rate limits, OpenBao Transit envelope
encryption, and durable rewrap. Realizes UJ-2 and UJ-5.

#### FR-9: Production security mode and surface boundaries

Production `scrapd` startup fails closed when required TLS, role policy, peer
identity policy, Transit configuration, or dangerous hook policy is missing,
invalid, or contradictory.

**Consequences:**

- Public, peer, admin, and `scrapctl` paths have separate security handling.
- Non-production mode is explicit and visible, and does not satisfy production
  readiness.
- Role authorization and peer identity checks run before side effects.

**Traceability:** ADR 0019, ADR 0024, issues #399, #401-#404, #430-#434.

#### FR-10: OpenBao envelope encryption and durable rewrap

Production writes encrypt new Document payload bytes before Block persistence,
reads decrypt through the normal path while preserving integrity checks, and
operators can durably rewrap envelope metadata through Raft.

**Consequences:**

- Production writes never fall back to plaintext when Transit is unavailable.
- Frame CRC covers ciphertext bytes; Document SHA-256 verifies plaintext before
  return.
- Rewrap does not rewrite Block payload bytes and does not leak key material.

**Traceability:** ADR 0020, ADR 0021, ADR 0023, issues #400, #405-#407.

### 6.5 Content Scanner and Content Quarantine

**Description:** ADR 0008 defines asynchronous Content Scanner and
metadata-level Content Quarantine. The current repo has the architecture
decision but not the implementation surface named by that ADR. Realizes UJ-4.

#### FR-11: Async Content Scanner

V2 includes a leader-owned background Content Scanner that scans sealed Block
bytes with ClamAV and YARA after ACK and never blocks the write path.

**Consequences:**

- Scanner unavailability does not block writes.
- Scan progress uses persisted watermarks.
- Scanner work shares I/O budget with Deep Scrub.
- Detection and rescan behavior are observable.

**Traceability:** `CONTEXT.md`, ADR 0008.

**Decision Gate:** DG-1.

#### FR-12: Content Quarantine read gate and admin operations

Content Quarantine gates a single Document at metadata level: `ReadDocument`
denies bytes, while `HeadDocument` and `FindDocuments` expose metadata with scan
status for reconciliation.

**Consequences:**

- Block bytes are untouched by Content Quarantine.
- Quarantine state is replicated through Raft.
- Operator confirm and release actions are available through the accepted admin
  surface.
- ADR 0025 amends ADR 0008 so V2 uses existing admin HTTP plus `scrapctl`, not a
  new gRPC AdminService.

**Traceability:** `CONTEXT.md`, ADR 0008, ADR 0019.

**Decision Gate:** DG-1.

### 6.6 Operator Surface and `scrapctl`

**Description:** Operators need a stable CLI and admin surface for diagnostics,
status, evidence, upload pressure, fault workflows, eviction, and production
security rehearsal. The remaining operator-surface gap is OpenBao bootstrap
ownership. Realizes UJ-2 and UJ-5.

#### FR-13: `scrapctl` operational baseline

`scrapctl` supports production-oriented diagnostics and evidence workflows for
status, leaders, peers, upload pressure, faults, evidence bundles, and eviction.

**Consequences:**

- CLI output is actionable and does not leak secrets, raw Document identifiers,
  raw Backend keys, or unbounded high-cardinality values.
- `scrapctl` is a client/operator path, not a separate storage authority.

**Traceability:** ADR 0015, ADR 0016, Phase 4/4.5 implementation docs.

#### FR-14: `scrapctl` OpenBao bootstrap

V2 implements `scrapctl` OpenBao bootstrap commands for local and prod-like
operator workflows.

**Consequences:**

- Commands initialize, unseal, mount Transit, create the S.C.R.A.P.
  Transit key through the official OpenBao Go API client, and emit redacted
  evidence suitable for rehearsal notes.
- Production OpenBao deployment, secret custody, storage backend setup,
  high-availability topology, and lifecycle remain platform-owned.

**Traceability:** `docs/archive/obsolete-pre-bmad/phase-4.5-security-implementation-slices.md`, ADR 0023.

**Decision Gate:** DG-4.

### 6.7 Telemetry, Evidence, and Release Closure

**Description:** V2 release readiness is evidence-backed. Telemetry and closure
artifacts must prove current behavior without leaking sensitive values. Realizes
UJ-2.

#### FR-15: OTel evidence plane

S.C.R.A.P. emits OpenTelemetry metrics, logs, traces, and profiles sufficient to
prove runtime behavior, production safety, and evidence gates.

**Consequences:**

- New metrics use OTel instruments and low-cardinality attributes.
- Logs use structured `slog` and redact raw Document identifiers, Backend keys,
  traces, request IDs, secrets, and sensitive dependency text.
- Pprof remains an opt-in admin feature restricted to evidence/operator paths.

**Traceability:** ADR 0012, ADR 0013.

#### FR-16: Major-release evidence and documentation closure

V2 release-ready status requires linked, current, reviewable evidence and
operator documentation for every required release claim.

**Consequences:**

- Required evidence includes Tier 2 prod-like Cilium gate, Tier 3 evidence
  bundle, production security rehearsal, and real S3/IAM production rehearsal
  when Backend claims depend on S3.
- Operator runbooks, alert/query references, incident workflows, and evidence
  instructions are required unless explicitly de-scoped.
- Issue #429 remains a final gate after feature scope is complete.

**Traceability:** ADR 0012, ADR 0015, `docs/prd-closure-policy.md`,
`docs/production-rehearsal.md`, issue #429.

**Decision Gate:** DG-5.

## 7. Blocking Decision Gates

These gates must be closed before implementation stories are generated for the
affected scope. ADR 0025 through ADR 0027 now close the architecture decisions
that required durable ADRs.

| Gate | Decision | Resolution | Closure artifact | Blocks |
| --- | --- | --- | --- | --- |
| DG-1 | Keep ADR 0008 Content Scanner / Content Quarantine in V2, or supersede it? | Keep it in V2; use existing admin HTTP plus `scrapctl`, not new admin gRPC. | ADR 0025 | FR-11, FR-12, scanner/quarantine epics |
| DG-2 | Is V2 release single-Shard or multi-Shard? | Multi-Shard startup/routing is required for V2 release-ready status. | ADR 0026 | FR-5, topology/routing epics |
| DG-3 | What is the Phase 5 cold-read shape? | Restore-first full-Block restore; direct Backend ciphertext streaming is out of V2. | ADR 0027 | FR-8, cold-read implementation epics |
| DG-4 | Does `scrapctl` own OpenBao bootstrap? | `scrapctl` owns local/prod-like bootstrap helper workflows only, not production OpenBao lifecycle. | Master architecture | FR-14 |
| DG-5 | What is the V2 release documentation/evidence standard? | Runbooks, alert/query references, evidence matrix, and closure policy updates are required. | Master architecture | FR-16, release closure |

## 8. Cross-Cutting NFRs

- **NFR-1 Fail-closed security and storage behavior:** Missing security config,
  missing key material, corrupt metadata, corrupt Block bytes, invalid Backend
  restore, unauthorized peer/admin/public access, and unsafe production hooks
  fail closed.
- **NFR-2 Bounded memory and streaming:** Production paths do not buffer full
  Documents, Blocks, uploads, restores, peer transfers, or scans in memory.
- **NFR-3 Authority separation:** Raft is metadata authority; Pebble Projection,
  Confirmed Upload Catalog, Backend objects, Local Block Lifecycle, audit, and
  OTel evidence are not storage truth.
- **NFR-4 Privacy and redaction:** Logs, metrics, traces, audit, evidence,
  screenshots, fixtures, and public tracker comments do not leak secrets,
  Document bytes, raw identifiers, Backend keys, data keys, or wrapped-key
  ciphertext.
- **NFR-5 Operational evidence:** Each release claim has current evidence with
  command, commit/ref, environment, expected result, actual result, artifact
  path, and redaction proof.
- **NFR-6 Compatibility discipline:** Storage format, wire protocol,
  dependency/runtime choices, security/encryption/auth contracts, and
  cross-package ownership changes require ADRs.
- **NFR-7 Test coverage by risk:** Required features need positive path,
  fail-closed path, restart/rebuild or recovery path where relevant, and the
  narrowest local/deployed gate that proves the claim.
- **NFR-8 Data-integrity blocker discipline:** Any unresolved data-integrity
  defect or contradictory data-integrity release evidence blocks final V2 PASS.
  Release closure must link the fix, regression test, affected requirement,
  verification command, and artifact before returning to PASS.

## 9. Non-Goals

Unless re-chartered by a later PRD or ADR, V2 does not include:

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

## 10. V2 Release Scope

### 10.1 In Scope

- Existing core Document API and immutable storage contract.
- Multi-voter Raft, peer replication, Member identity, and fail-closed
  Projection Resolution.
- Deep Scrub, Block Quarantine, repair, and rebuild behavior.
- Backend upload, Upload Outbox, upload pressure, and Confirmed Upload Catalog.
- OpenTelemetry evidence plane and trace context in Raft metadata.
- Prod-like Kind/Cilium gates and `scrapctl` validation lanes.
- Phase 4 partial local eviction and full-Block restore.
- Production security mode, mTLS, authorization, peer scope, audit, and rate
  limits.
- OpenBao Transit envelope encryption and durable rewrap.
- Content Scanner and Content Quarantine using existing admin HTTP plus
  `scrapctl`, per ADR 0025.
- Phase 5 restore-first cold-read behavior, per ADR 0027.
- Multi-Shard startup/routing, per ADR 0026.
- `scrapctl` OpenBao bootstrap for local/prod-like workflows.
- Operator runbooks, alert/query references, evidence instructions, and final
  release closure matrix.
- Real S3/IAM production rehearsal for Backend claims.

### 10.2 Out of Scope Unless Re-Chartered

The non-goals in section 9 remain out of scope unless a later accepted ADR or
PRD explicitly adds them to V2.

## 11. Acceptance and Evidence Matrix

| Requirement area | Required evidence | Minimum gate |
| --- | --- | --- |
| Core Document API and read/write behavior | Unit, integration, and e2e evidence for write ACK, duplicate handling, read all-or-error, metadata visibility | `make tier1-check`, targeted e2e |
| Raft/peer replication | Multi-voter replication, restart/rebuild, not-leader, peer authorization, transfer failure evidence | `make integration`, Tier 2 e2e |
| Backend upload and restore | Upload confirmation, Confirmed Upload Catalog, restore success, missing/corrupt Backend object failure | `make integration`, Tier 2 e2e |
| Eviction | Dry-run, apply, marker/unlink, restore, validation, local lifecycle health | Tier 2 e2e plus evidence bundle |
| Production security | Startup fail-closed, mTLS, authz, peer identity, audit, rate limits, denied-operation evidence | `make production-rehearsal-security` |
| OpenBao encryption and rewrap | Encrypted write/read, Transit outage, missing key, auth denied, durable rewrap, no key leakage | `make integration`, `make production-rehearsal-security` |
| Content Scanner and Quarantine | Scanner scheduling, quarantine command, read denial, metadata scan status, confirm/release, scanner outage and rescan | New targeted gates per ADR 0025 |
| Phase 5 cold reads | Restore-first behavior, Backend failures, encryption interaction, telemetry, cancellation | New targeted gates per ADR 0027 |
| Multi-Shard routing | Hash-slot routing, startup membership, Shard-scope auth, failure-domain behavior | New targeted gates per ADR 0026 |
| `scrapctl` OpenBao bootstrap | Idempotency, error handling, redacted output, official OpenBao API client usage | New targeted CLI/integration gates after DG-4 |
| Documentation and release closure | Runbooks, alerts/query refs, incident workflows, evidence index, closure policy | Docs review plus closure checklist |
| Real S3/IAM Backend proof | Linked report from real non-local S3/IAM production rehearsal | `make production-rehearsal` |

Final release closure fails if any required requirement lacks current linked
evidence.

## 12. Success Metrics

**Primary**

- **SM-1:** Every required V2 FR has a linked PRD row, architecture/story source,
  GitHub tracker item, verification command, and evidence artifact.
- **SM-2:** No blocking decision gate remains open before implementation stories
  are generated for that area.
- **SM-3:** Final V2 closure includes green Tier 2 prod-like, Tier 3 evidence,
  production security rehearsal, and real S3/IAM rehearsal evidence where
  applicable.
- **SM-4:** Evidence bundles and public tracker comments contain no secrets, raw
  Document identifiers, raw Backend keys, Document bytes, data keys, or wrapped
  key ciphertext.

**Counter-metrics**

- **SM-C1:** Do not optimize for closed issue count alone; closed issues do not
  prove release completeness.
- **SM-C2:** Do not optimize for shorter PRD/backlog if it hides required
  decision gates.
- **SM-C3:** Do not optimize for passing local unit tests when the claim depends
  on deployed Cell, security, Backend, or evidence behavior.

## 13. Remaining Open Questions

1. **OQ-1:** Should issue #429 be linked to a new master V2 release parent issue
   after this PRD is accepted?
2. **OQ-2:** Should the older deleted `_bmad-output/planning-artifacts/epics.md`
   be restored for history or replaced entirely by regenerated master V2 epics?

## 14. Assumptions Index

- [ASSUMPTION] This master PRD may be created as a draft/partial artifact
  because the user asked for execution after the Party Mode recommendation and
  the remaining uncertainty is represented as blocking decision gates.
- [ASSUMPTION] The default BMAD run-folder pattern is intentionally overridden
  to `prd-scrap-v2-master-2026-06-10` to avoid collision with the older
  Phase 4.5 PRD and to preserve the "V2 master" distinction.
- [ASSUMPTION] The phrase "V2 release scope" replaces "MVP scope" for this
  product because the user explicitly clarified that there are no intermediate
  V2 releases.
- [ASSUMPTION] The old Phase 4.5 epics file should be replaced by regenerated
  master V2 epics instead of being used as current backlog truth.

## 15. Downstream Workflow

1. Run `bmad-create-epics-and-stories` from this master PRD, the master V2
   architecture artifact, ADR 0025, ADR 0026, ADR 0027, and current GitHub
   tracker state.
2. Run `bmad-check-implementation-readiness` against the regenerated PRD,
   architecture, and epics.
3. Run `bmad-sprint-planning` to regenerate implementation tracking.
4. Implement stories with test-first evidence and published GitHub tracker
   alignment.
5. Run final release evidence only after all required feature scope is complete:
   Tier 2 prod-like Cilium, Tier 3 evidence, production security rehearsal, and
   real S3/IAM production rehearsal.
