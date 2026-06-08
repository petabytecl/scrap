---
title: Phase 4.5 production security and encryption bridge
status: draft
created: 2026-06-07
updated: 2026-06-07
source_issue: "https://github.com/petabytecl/scrap/issues/398"
source_issue_number: 398
github_milestone: storage-gateway-v2
labels:
  - prd
  - production-readiness
  - architecture
  - v2
---

# PRD: Phase 4.5 Production Security and Encryption Bridge

## 0. Document Purpose

This BMAD PRD captures GitHub PRD issue #398 and its Phase 4.5 slice issues
as a local planning artifact for downstream architecture, story, readiness, and
implementation workflows. GitHub Issues remain the published execution tracker;
this PRD preserves requirement shape, traceability, non-goals, and closure
criteria without replacing accepted ADRs.

Source inputs:

- `CONTEXT.md`
- `docs/adr/0019-production-security-boundary.md`
- `docs/adr/0020-openbao-envelope-encryption-contract.md`
- `docs/phase-4.5-security-implementation-slices.md`
- GitHub issues #398 through #408
- `_bmad-output/project-context.md`

## 1. Vision

Phase 4.5 makes production security and envelope encryption explicit,
testable, and operator-visible before Phase 5 expands Backend-only read
behavior. Phase 4 partial local eviction already lets operator workflows change
local read availability for Documents whose Block data has been evicted. Phase
5 would make Backend restore and cold reads more sensitive, so public, peer,
admin, and crypto boundaries must fail closed before that work begins.

The bridge adds production-mode mTLS, role authorization, peer identity checks,
bounded audit evidence, independent rate limits, OpenBao Transit envelope
encryption, durable rewrap, and prod-like evidence gates. The outcome is not a
new storage feature for billing services; it is the production safety boundary
required before S.C.R.A.P. can rely more heavily on Backend-resident Blocks.

## 2. Target Users

### 2.1 Jobs To Be Done

- Platform operators need production `scrapd` startup to fail closed when TLS,
  role policy, peer identity, Transit, or unsafe admin hook configuration is
  missing or contradictory.
- Storage engineers need public, peer, and admin surfaces to enforce separate
  authentication and authorization without relying on Kubernetes NetworkPolicy
  as the application security boundary.
- Billing services need encrypted Document writes and reads to preserve the
  existing ACK, CRC, SHA-256, Projection Resolution, and DATA_LOSS semantics.
- Operators need audit and evidence bundles that prove security and encryption
  behavior without leaking secrets, Document bytes, or high-cardinality
  identifiers.

### 2.2 Non-Users

- Phase 5 cold-only read implementers are downstream consumers of this PRD, not
  direct scope owners.
- Tenant-policy designers are out of scope for this phase; `tenant_id` remains
  non-authoritative for storage identity.
- Backend ciphertext streaming implementers are out of scope for this phase.

### 2.3 Key User Journeys

- **UJ-1. Olivia validates production readiness before enabling Phase 5.**
  Olivia, a platform operator, starts a prod-like Cell in production security
  mode, checks `scrapctl status`, reviews evidence bundles, and sees mTLS,
  authz, audit, rate-limit, encrypted write/read/restore, crypto outage, and
  rewrap checks pass without secrets in the output. If a required security
  setting is missing, startup fails closed and the evidence explains the missing
  class of configuration.

- **UJ-2. Davi writes and reads encrypted Documents through the normal API.**
  Davi, a billing service engineer, writes a Document in production mode. The
  write is ACK'd only after encryption and envelope persistence succeed. Later
  reads decrypt through the normal path, verify ciphertext CRC and plaintext
  SHA-256, and fail closed with a typed crypto-unavailable error if key material
  is unavailable.

- **UJ-3. Mara rotates encryption material without rewriting Blocks.**
  Mara, an operator, triggers rewrap for existing encrypted Documents. The
  workflow updates envelope metadata through Raft, remains idempotent for
  already-updated envelopes, emits bounded audit evidence, and does not corrupt
  existing readable Documents if Transit fails.

## 3. Glossary

- **Document** - Immutable file stored in S.C.R.A.P., addressed by
  `(transaction_id, document_name)`.
- **Transaction** - Group of related Documents from one billing workflow step.
- **Block** - Append-only file containing Documents as sequential Frames.
- **Frame** - Contiguous chunk of Document bytes inside a Block; CRC-32C covers
  stored bytes.
- **Shard** - Independent Raft group managing a subset of Transactions.
- **Cell** - Complete S.C.R.A.P. deployment identified by a permanent `cell_id`.
- **Member** - Storage node within a Cell, identified by `cell_id`,
  `member_hostname`, and durable `member_id`.
- **Backend** - Cloud object store providing cold durability for sealed Blocks;
  not in the write ACK path.
- **Pebble Projection** - Rebuildable read-side metadata projection.
- **Projection Resolution** - Read-side process that resolves visible Pebble
  Projection Transaction entries into Document metadata.
- **Upload Outbox** - Per-Shard durable record of sealed Blocks pending Backend
  upload.
- **Confirmed Upload Catalog** - Derived per-Shard record of Blocks whose Backend
  upload has been confirmed by committed Raft state.
- **Local Block Lifecycle** - Per-Member filesystem evidence about one local
  Block copy.
- **OpenBao Transit** - External key service used for envelope encryption and
  rewrap operations.
- **Envelope metadata** - Versioned per-Document encryption metadata required to
  decrypt, verify, and rewrap encrypted payload bytes.

## 4. Features

### 4.1 Production Security Mode and Startup Gates

**Description:** `scrapd` must make security mode explicit and reject production
startup when required TLS, role policy, peer identity, Transit, or dangerous
hook configuration is absent or contradictory. Development and test modes remain
available, but they must be visible and must not satisfy production readiness.
Realizes UJ-1.

#### FR-1: Production security mode fails closed

`scrapd` can run in explicit production, development, or test security mode.
Production mode fails startup when required cert/key/client-CA files, role
policy, peer identity policy, Transit configuration, or dangerous hook policy is
missing, invalid, or contradictory.

**Consequences:**

- Missing TLS, role policy, peer identity, or Transit config prevents production
  startup.
- Development/test mode is visible in admin health, `scrapctl status`, metrics,
  diagnostics, and evidence bundles.
- Development/test mode does not satisfy production write ACK readiness or
  Phase 5 entry checks.

**Traceability:** #401, ADR 0019, ADR 0020.

### 4.2 Surface Authentication, Authorization, and Identity

**Description:** Public client gRPC, peer gRPC, admin HTTP or future admin gRPC,
and `scrapctl` calls need separate mTLS and authorization treatment. Peer
identity must use the Cell/Member model, not address or certificate presence
alone. Realizes UJ-1.

#### FR-2: mTLS credentials are wired per surface

Each public, peer, admin, and `scrapctl` path can load and validate server
certificates, server keys, and client CA configuration. Production clients
validate server certificates and present client certificates.

**Consequences:**

- Production mode refuses insecure client or server credentials.
- Local development tests can run only through explicit development/test mode.
- Public, peer, and admin surfaces do not share handler assumptions or
  authorization policy by accident.

**Traceability:** #402, ADR 0019.

#### FR-3: Role authorization and peer identity checks fail closed

Authenticated principals are mapped into role sets for public Document
operations, peer operations, admin reads, and dangerous admin operations. Peer
RPCs also verify matching Cell and Member identity before they can affect
storage state.

**Consequences:**

- Public Document operations require reader or writer roles as appropriate.
- Peer RPCs require peer role plus matching `cell_id`, `member_hostname`, and
  `member_id` relationship.
- Admin read operations and dangerous admin operations require distinct roles.
- Unauthorized requests fail closed and do not perform side effects.

**Traceability:** #403, ADR 0019, `CONTEXT.md`.

### 4.3 Audit Events and Rate Limits

**Description:** Security-sensitive operations must emit bounded audit evidence
and be protected by independent request budgets for public, peer, and admin
surfaces. Realizes UJ-1.

#### FR-4: Security operations are auditable and rate-limited

S.C.R.A.P. emits bounded audit events and rate-limit metrics for public, peer,
and admin security-sensitive operations, with special coverage for repair,
restore, eviction, quarantine, pprof, and fault operations.

**Consequences:**

- Audit events include principal, role, operation, target, result, and reason.
- Audit events and metrics do not include secrets, Document bytes, unbounded
  notes, or high-cardinality identifiers.
- Rate-limit failures are observable through metrics and audit events.
- Dangerous admin operations are denied or audited according to role.

**Traceability:** #404, ADR 0019.

### 4.4 OpenBao Transit Boundary

**Description:** The storage path needs a production OpenBao Transit boundary
and a deterministic fake for unit and integration tests. The fake tests
S.C.R.A.P. contracts; it is not production cryptography. Realizes UJ-2 and
UJ-3.

#### FR-5: Transit boundary supports encryption lifecycle behavior

The Transit boundary supports data-key generation, unwrap, rewrap, readiness,
outage, auth-denied, missing-key, and minimum-version failure behavior.

**Consequences:**

- Production config validates Transit mount, key, and credentials without
  logging secrets.
- Fake Transit tests prove fail-closed behavior without live OpenBao.
- Production crypto behavior remains separated from deterministic test
  behavior.

**Traceability:** #405, ADR 0020.

### 4.5 Encrypted Writes and Reads

**Description:** New Document payload Frames are encrypted before Block writes
and decrypted through the normal read path while preserving CRC, SHA-256,
Projection Resolution, and Raft authority semantics. Realizes UJ-2.

#### FR-6: New Document payload writes and reads are envelope-encrypted

Production writes are ACK'd only when payload encryption and durable envelope
metadata persistence both succeed. Reads decrypt encrypted Document payloads,
verify ciphertext storage integrity and plaintext Document integrity, and fail
closed when key material is unavailable.

**Consequences:**

- Production writes never fall back to plaintext Block payload bytes when
  Transit is unavailable.
- Reads return a typed crypto-unavailable error for Transit outage, sealed
  Transit, auth failure, missing key, or incompatible envelope state.
- Frame CRC verifies ciphertext storage integrity.
- Document SHA-256 verifies plaintext integrity before bytes are returned.
- Tests cover encrypted write/read, Transit outage, missing key, auth-denied,
  and corruption behavior.

**Traceability:** #406, ADR 0020, ADR 0001, ADR 0003, ADR 0014.

### 4.6 Durable Rewrap Workflow

**Description:** Operators can rewrap Document envelope metadata without
rewriting Block payload bytes. Rewrap is a durable metadata lifecycle operation
recorded through Raft authority. Realizes UJ-3.

#### FR-7: Rewrap is durable, idempotent, and auditable

An operator can trigger rewrap for encrypted Documents. Successful rewrap
updates envelope metadata through Raft and converges on all Members.

**Consequences:**

- Rewrap is idempotent for already-updated envelopes.
- Rewrap records audit evidence without logging plaintext, data keys, or
  wrapped-key ciphertext.
- Rewrap failures are visible in admin health/evidence.
- Rewrap failure does not corrupt existing readable Documents.

**Traceability:** #407, ADR 0020.

### 4.7 Prod-like Security and Encryption Evidence Gates

**Description:** Phase 4.5 is not complete until prod-like and evidence gates
prove the security and encryption bridge. Phase 5 entry remains blocked unless
these gates are green. Realizes UJ-1.

#### FR-8: Evidence gates prove Phase 4.5 behavior

Prod-like and evidence workflows prove mTLS, authorization, audit, rate-limit,
encryption, crypto-outage, encrypted write/read/restore, and rewrap behavior.

**Consequences:**

- Evidence bundles record security mode, TLS/authz gate results, audit samples,
  encryption outcomes, and rewrap outcomes without secrets.
- Negative tests prove unauthorized public, peer, and admin requests are denied.
- A fresh encrypted write/read/restore path passes in the prod-like Cell.
- Phase 5 entry remains blocked unless this gate is green.
- Closure evidence follows `docs/prd-closure-policy.md` when GitHub Actions,
  Tier gates, CodeQL, or hosted CI evidence is required.

**Traceability:** #408, #398, docs/prd-closure-policy.md.

## 5. Non-Goals

- Phase 5 cold-only read shape.
- Metadata encryption.
- Tenant-specific key policy.
- Cell federation.
- Direct Backend ciphertext streaming.
- Certificate hot reload for the first implementation; restart-based
  certificate rotation is acceptable if production release runbooks are
  captured.
- Transparent migration for existing unencrypted Blocks unless a later migration
  issue explicitly requires it.

## 6. MVP Scope

### 6.1 In Scope

- Explicit production/development/test security modes.
- Production startup validation for TLS, role policy, peer identity, Transit,
  and dangerous hook configuration.
- Per-surface mTLS and role authorization for public, peer, admin, and
  `scrapctl` paths.
- Peer Cell/Member identity checks.
- Bounded audit events and independent rate limits.
- OpenBao Transit client boundary and deterministic fake Transit.
- Envelope encryption for new Document payload bytes.
- Typed fail-closed read behavior when key material is unavailable.
- Durable, idempotent rewrap through Raft metadata.
- Prod-like evidence gates for security and encryption behavior.

### 6.2 Out of Scope for MVP

- Storage identity changes involving `tenant_id`.
- Encryption of transaction IDs, Document names, sizes, Raft metadata, Pebble
  Projection keys, `.idx` entries, audit events, or telemetry labels.
- Per-Block data keys.
- Transit convergent encryption for Document payloads.
- Direct Transit encryption of every Frame.
- Production plaintext fallback when Transit is down.
- Backend listing or Backend object existence as a consistency source.

## 7. Success Metrics

**Primary**

- **SM-1:** Production security startup gate coverage: production mode fails
  startup for missing or contradictory TLS, role policy, peer identity, Transit,
  and unsafe hook configuration. Validates FR-1.
- **SM-2:** Surface authorization coverage: unauthorized public, peer, and admin
  requests are denied without side effects in prod-like tests. Validates FR-2
  and FR-3.
- **SM-3:** Encryption path coverage: fresh encrypted write/read/restore passes
  in the prod-like Cell, with crypto outage and missing-key failures tested.
  Validates FR-5 and FR-6.
- **SM-4:** Rewrap evidence coverage: rewrap success, idempotency, and failure
  states are visible in admin health/evidence without leaking key material.
  Validates FR-7.
- **SM-5:** Phase 5 gate readiness: evidence bundles link current security mode,
  TLS/authz, audit, rate-limit, encryption, restore, outage, and rewrap results.
  Validates FR-8.

**Counter-metrics**

- **SM-C1:** No secrets, Document bytes, raw Document identifiers, key material,
  or high-cardinality values appear in logs, metrics, traces, evidence bundles,
  screenshots, or CI artifacts. Counterbalances SM-4 and SM-5.
- **SM-C2:** Development/test mode does not satisfy production readiness or
  Phase 5 entry checks. Counterbalances local workflow convenience.

## 8. Constraints and Guardrails

- Use exact `CONTEXT.md` terminology.
- Do not add `tenant_id` to storage identity, Backend object identity,
  cardinality-bearing metrics, or deployed logs without an ADR.
- Backend upload remains outside the write ACK path.
- Raft remains the metadata authority; Pebble Projection remains rebuildable.
- CRC covers stored ciphertext bytes; SHA-256 covers plaintext Document bytes.
- Production encryption paths fail closed when key material is unavailable.
- NetworkPolicy, Cilium policy, Kubernetes RBAC, and host access restrictions
  are defense-in-depth and do not replace application mTLS, authorization, or
  audit checks.

## 9. Published Execution Issues

| Issue | Requirement Area | Status |
| --- | --- | --- |
| #399 | ADR 0019 production security boundary | Open |
| #400 | ADR 0020 OpenBao envelope encryption contract | Open |
| #401 | Production security mode and startup gates | Open |
| #402 | mTLS credentials for public, peer, admin, and `scrapctl` | Open |
| #403 | Role authorization and peer identity checks | Open |
| #404 | Audit events and rate limits | Open |
| #405 | OpenBao Transit client and fake Transit boundary | Open |
| #406 | Encrypted new Document writes and decrypted reads | Open |
| #407 | Durable rewrap workflow and evidence | Open |
| #408 | Prod-like security and encryption evidence gates | Open |

## 10. Open Questions

- **OQ-1:** No phase-blocking open questions were found during migration. ADR
  0019 and ADR 0020 intentionally defer certificate hot reload, metadata
  encryption, tenant-specific key policy, and direct Backend ciphertext
  streaming.

## 11. Assumptions Index

- No inline assumptions were introduced. Requirements were extracted from
  accepted ADRs, the published Phase 4.5 slice document, and GitHub issues
  #398 through #408.
