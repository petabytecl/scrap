---
stepsCompleted:
  - 1
  - 2
  - 3
  - 4
  - 5
  - 6
  - 7
  - 8
inputDocuments:
  - CONTEXT.md
  - _bmad-output/project-context.md
  - _bmad-output/planning-artifacts/architecture.md
  - _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md
  - _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/.decision-log.md
  - docs/archive/obsolete-pre-bmad/scope-reconciliation.md
  - docs/adr/0008-async-content-scanning-architecture.md
  - docs/adr/0012-otel-evidence-plane.md
  - docs/adr/0016-phase-4-partial-eviction-boundary.md
  - docs/adr/0019-production-security-boundary.md
  - docs/adr/0020-openbao-envelope-encryption-contract.md
  - docs/adr/0024-production-topology-and-peer-scope-policy.md
  - docs/adr/0025-content-quarantine-admin-surface.md
  - docs/adr/0026-multi-shard-v2-release-boundary.md
  - docs/adr/0027-phase-5-restore-first-cold-reads.md
  - docs/archive/obsolete-pre-bmad/phase-4.5-security-implementation-slices.md
  - docs/prd-closure-policy.md
  - docs/production-rehearsal.md
workflowType: architecture
project_name: scrap
user_name: Coto
date: 2026-06-10
lastStep: 8
status: complete
completedAt: 2026-06-10
source_prd: _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md
supersedes_for_scope: _bmad-output/planning-artifacts/architecture.md
---

# Architecture Decision Document: S.C.R.A.P. V2 Master Release Gates

This architecture artifact resolves the V2 master PRD's blocking decision gates
and now points to the accepted ADRs that close the durable architecture
decisions for epics, stories, and evidence planning.
It does not erase the existing Phase 4.5 architecture artifact. The previous
`_bmad-output/planning-artifacts/architecture.md` remains the Phase 4.5
security/encryption implementation contract; this document is the master V2
release-scope architecture contract for the remaining gates.

## Project Context Analysis

### Requirements Overview

The master V2 PRD defines 16 functional requirements and 5 blocking decision
gates. The important release rule is now explicit: V2 has no intermediate
release boundary. A closed phase, closed milestone, or closed implementation
issue is useful evidence but does not make V2 release-ready.

The remaining architecture work is concentrated in five decisions:

| Gate | Decision | Architecture result |
| --- | --- | --- |
| DG-1 | Keep or supersede ADR 0008 Content Scanner / Content Quarantine | Keep it in V2; ADR 0025 amends the admin surface to existing admin HTTP plus `scrapctl`. |
| DG-2 | Single-Shard or multi-Shard V2 | Multi-Shard startup/routing is required for V2 release-ready status by ADR 0026. |
| DG-3 | Phase 5 cold-read shape | ADR 0027 selects restore-first cold reads; direct Backend ciphertext streaming is out of V2 unless re-chartered. |
| DG-4 | `scrapctl` OpenBao bootstrap ownership | `scrapctl` owns local/prod-like bootstrap helper workflows only, not production OpenBao lifecycle. |
| DG-5 | Release documentation/evidence standard | Runbooks, alert/query references, incident workflows, evidence matrix, and closure policy updates are required release scope. |

### Functional Requirements Implications

- FR-1 through FR-4 are core Document, durability, read, Raft, and Shard
  authority requirements. They remain governed by `CONTEXT.md` and accepted
  early ADRs.
- FR-5 is not satisfied by the current single-Shard `scrapd` composition. The
  current code wires `appShardID = 0`; V2 release scope must introduce a
  Shard routing layer and multi-Shard process composition.
- FR-8 is satisfied by restore-first cold reads, not direct Backend streaming.
  Phase 5 needs all-local-copy eviction and restore-on-read behavior, but does
  not need a second read implementation that streams Backend ciphertext directly
  to clients.
- FR-11 and FR-12 stay in V2. ADR 0008 is accepted and the PRD did not
  supersede it. The implementation needs new proto, Raft, Projection, Shard,
  scanner, admin, `scrapctl`, deployment, and evidence work.
- FR-14 is operator tooling scope. `scrapctl` should remove script-only
  OpenBao setup from local/prod-like rehearsals, but production OpenBao
  deployment and secret custody remain platform-owned.
- FR-16 is release architecture, not polish. Evidence and docs are release
  acceptance surfaces.

### Non-Functional Requirements

- Preserve authority separation: Raft owns metadata and lifecycle decisions;
  Pebble Projection, Backend inventory, Local Block Lifecycle, audit, and OTel
  evidence do not decide state.
- Preserve all-or-error `ReadDocument`: no partial success and no least-bad
  bytes.
- Preserve bounded memory: scans, restores, reads, peer transfers, and uploads
  must stream or bound buffers.
- Preserve redaction: no raw `transaction_id`, `document_name`, Backend keys,
  data keys, wrapped-key ciphertext, private keys, tokens, or raw logs in
  deployed logs, metrics, audit, traces, evidence, or public tracker comments.
- Preserve ADR discipline for wire protocol, storage format, dependency,
  security/encryption/auth, and cross-package boundary changes.

### Scale and Complexity

- Primary domain: distributed Go storage gateway with security, encryption,
  content safety, cold-read lifecycle, and production evidence.
- Complexity level: enterprise infrastructure.
- New architectural components: scanner/quarantine path, Shard router,
  restore-first cold-read policy expansion, OpenBao bootstrap CLI workflow, and
  release evidence/docs closure surface.
- Highest-risk cross-cutting concerns: wire compatibility, Raft metadata shape,
  Shard membership/routing authority, scanner outage behavior, Backend restore
  failure semantics, evidence redaction, and operator runbook completeness.

## Starter Template Evaluation

No new starter template is selected. The existing S.C.R.A.P. V2 repository is
the foundation.

The checked foundation remains:

- Go module `github.com/petabytecl/scrap`.
- gRPC/protobuf API and Buf generation.
- etcd Raft metadata authority.
- Pebble Projection.
- filesystem/S3 Backend abstraction.
- OpenBao Transit envelope encryption.
- OpenTelemetry evidence plane.
- Kind/Cilium production-like evidence path.
- `scrapd` scratch image and `scrapctl` CLI.

Official current-version checks were used only to avoid stale starter thinking:
Go 1.26 is the active Go release line, OpenBao GitHub releases show v2.5.4 as
latest at this check, and pkg.go.dev reports `google.golang.org/grpc` version
1.81.1. This architecture does not introduce new dependency versions; repo pins
remain authoritative.

## Core Architectural Decisions

### Decision Priority Analysis

**Critical decisions that block epics/stories:**

1. ADR 0008 remains V2 scope.
2. V2 release requires multi-Shard startup/routing.
3. V2 cold reads use restore-first full-Block restore; direct Backend
   ciphertext streaming is out of V2.
4. `scrapctl` owns OpenBao bootstrap only for local/prod-like operator
   workflows.
5. Release docs/evidence are required release acceptance artifacts.

**Important decisions that shape implementation:**

- Current admin HTTP remains the operator control surface unless a future ADR
  introduces admin gRPC. ADR 0008 must be amended before scanner/quarantine
  implementation if V2 uses HTTP admin plus `scrapctl` instead of the original
  gRPC AdminService.
- Shard routing belongs in a dedicated routing boundary, not scattered through
  server handlers or Shard internals.
- Scanner work is leader-owned and must share I/O budget with Deep Scrub.
- Content Quarantine is metadata-level Document gating, not Block Quarantine.
- OpenBao bootstrap helper output is evidence-producing and redacted by
  default.

**Deferred decisions:**

- Direct Backend ciphertext streaming.
- S3-compatible API.
- public delete API.
- tenant storage identity, tenant quota, and tenant-specific key policy.
- Cell federation.
- metadata encryption.
- hot certificate reload.
- production OpenBao deployment ownership by S.C.R.A.P.

### DG-1: Content Scanner and Content Quarantine

**Decision:** Keep ADR 0008 in V2. Content Scanner and Content Quarantine are
required unless a later accepted ADR explicitly supersedes them.

**Architecture:**

- Add `internal/avscan` for scanner orchestration, scan engine adapters, scan
  scheduler, watermarks, and scanner evidence helpers.
- Keep `internal/scrub` for integrity verification only. Do not merge Deep
  Scrub and Content Scanner concerns.
- The Shard leader scans sealed Blocks asynchronously after ACK. Scanner
  unavailability does not block writes.
- Scanning is post-ACK and never part of write durability or visibility.
- Scanner progress uses persisted projection state:
  `last_scanned_block_id` and `last_sig_version_scanned`.
- Signature updates reset scanning priority for rescan without changing
  Document identity or write visibility.
- Scanner hits propose a `QuarantineDocument` Raft command.
- Quarantine state is replicated metadata. A dedicated Pebble Projection prefix
  materializes quarantined Document identities.
- `ReadDocument` checks Content Quarantine before serving bytes and returns
  `FAILED_PRECONDITION` with a bounded quarantine reason.
- `HeadDocument` and `FindDocuments` continue to return metadata and include
  scan status.
- Block bytes remain untouched; Content Quarantine does not rename or modify
  `.blk` or `.idx` files.

**Required wire/storage changes:**

- `proto/scrap/v1/raft.proto`: add `QuarantineDocument` command.
- `proto/scrap/v1/document.proto`: add scan status to `HeadDocumentResponse`,
  `ReadDocumentMeta` only if needed for metadata handshake, and `DocumentMeta`.
- Pebble Projection: add quarantine prefix and scanner watermark keys.
- Admin/operator surface: list, inspect, confirm, and release quarantine.

**Admin surface decision:**

ADR 0008 names a gRPC AdminService. Current V2 architecture and code use an
HTTP admin surface plus `scrapctl` client workflows. V2 should not introduce a
new admin gRPC surface just for scanner/quarantine unless a later design proves
that the HTTP admin model cannot satisfy the operator contract.

ADR 0025 now amends ADR 0008: Content Quarantine operations use existing admin
HTTP plus `scrapctl`; admin gRPC remains deferred.

**Evidence requirements:**

- Scanner schedules and scans sealed Blocks.
- Scanner outage does not block writes and is visible.
- Detection creates a `QuarantineDocument` command.
- Quarantined `ReadDocument` denies bytes.
- `HeadDocument` and `FindDocuments` return metadata with scan status.
- Confirm and release converge through Raft.
- Rescan after signature change can quarantine previously clean Documents.
- Logs, metrics, audit, and evidence do not leak raw Document identifiers or
  Document bytes.

### DG-2: Multi-Shard V2 Release Boundary

**Decision:** V2 release-ready status requires multi-Shard startup/routing.
Single-Shard V2 is no longer acceptable as the release contract unless a later
accepted ADR explicitly narrows V2 to single-Shard.

**Rationale:**

`CONTEXT.md` defines Shards, fixed hash slots, and Transactions assigned to
Shards. ADR 0024 already scopes peer authorization by Shard and says future
multi-Shard startup must derive authorized Shards from placement membership.
The current `scrapd` composition wires one Shard ID `0`, which is a development
and phase-implementation reality, not a complete release architecture.

**Architecture:**

- Add a Shard routing boundary, likely `internal/routing`, that owns:
  transaction-to-slot hashing, slot-to-Shard mapping, config validation,
  route lookup, and low-cardinality routing telemetry.
- Add a Shard set composition layer in `internal/cmd`. It constructs multiple
  `shard.Shard` instances from validated membership/placement config.
- `internal/server` routes public API calls by Transaction to the owning Shard
  through a narrow Store-compatible router. Server handlers must not hardcode
  Shard ID logic.
- `internal/peer` receives the configured authorized Shard set and enforces it
  on Shard-carrying RPCs before reaching Raft routing, replication sinks, or
  Block transfer handlers.
- `internal/admin` and `scrapctl` expose per-Shard health, leader, peer,
  upload-pressure, eviction, restore, quarantine, and evidence views.
- Backend keys already include `shard_id`; restore, upload, and Confirmed
  Upload Catalog behavior must stay Shard-scoped.
- Production startup fails closed when Shard membership, slot coverage,
  duplicate ownership, or local Member placement is invalid.

**ADR coverage:**

ADR 0026 defines the multi-Shard V2 release boundary, including slot count, slot
hash function, slot-to-Shard config shape, startup validation, peer Shard
authorization source, migration posture for existing single-Shard dev data, and
evidence gates.

**Evidence requirements:**

- Two or more Shards start in one Cell.
- Transactions route deterministically to the configured owning Shard.
- `FindDocuments` remains Transaction-scoped and never crosses Shard
  authority.
- Wrong-Shard peer RPCs are denied before side effects.
- Admin and `scrapctl` outputs identify Shard-local state without confusing
  Shard, Member, and Cell identity.
- Backend upload/restore/evidence remains Shard-local.
- Startup fails closed on incomplete slot coverage, duplicate slots, unknown
  Shard IDs, or invalid local membership.

### DG-3: Phase 5 Cold-Read Shape

**Decision:** V2 Phase 5 cold reads use restore-first full-Block restore.
Direct Backend ciphertext streaming is not part of V2 release scope unless
explicitly re-chartered by a future ADR.

**Rationale:**

ADR 0016 already chooses full-Block restore for Phase 4 and defers direct cold
streaming. ADR 0020 also defers direct ciphertext streaming. Restore-first keeps
the existing Block/Frame verification path, preserves all-or-error
`ReadDocument`, keeps Backend access behind Confirmed Upload Catalog metadata,
and avoids turning Backend inventory into a read-path oracle.

**Architecture:**

- Phase 5 extends eviction so every local `.blk` copy may become evictable
  after committed upload confirmation and policy gates.
- `ReadDocument` on a Member without a local `.blk` restores the full Block to
  a local staged path, verifies it against retained `.idx` and committed
  Backend metadata, publishes it atomically, and then uses the normal local
  Block reader.
- Restore remains per-Block singleflight and bounded by concurrency, timeout,
  context cancellation, and backpressure.
- If all local copies are evicted, the serving leader can restore from the
  Backend before serving. It must never stream unverified bytes.
- Metadata-only reads do not require restore.
- Direct Backend streaming, range streaming, and per-Frame remote reads are
  out-of-scope.
- Encryption interaction remains unchanged: Backend stores ciphertext Blocks;
  restore retrieves ciphertext; normal read path decrypts and verifies
  plaintext before return.

**ADR coverage:**

ADR 0027 defines the Phase 5 restore-first cold-read boundary. It closes direct
Backend ciphertext streaming for V2 and defines all-local-copy eviction,
serving-leader restore, restore cache/residency after read, error mapping,
telemetry, and evidence gates.

**Evidence requirements:**

- A Block can have all local `.blk` copies intentionally evicted under policy.
- A subsequent `ReadDocument` restores and verifies the full Block before
  serving.
- Concurrent reads coalesce behind one restore.
- Backend transient failure maps to `UNAVAILABLE`.
- Missing or corrupt confirmed Backend object maps to `DATA_LOSS`.
- Encryption and rewrap behavior still fail closed during restore/read.
- Restore does not use Backend list/HEAD as authority beyond committed metadata
  and explicit restore verification.

### DG-4: `scrapctl` OpenBao Bootstrap

**Decision:** `scrapctl` owns OpenBao bootstrap commands for local and
prod-like operator workflows. It does not own production OpenBao deployment,
secret custody, storage backend setup, high-availability OpenBao topology, or
long-term OpenBao lifecycle.

**Architecture:**

- Add `scrapctl openbao bootstrap` as an operator helper command group.
- Use the official OpenBao Go API client.
- Supported workflow scope:
  initialize local/prod-like OpenBao when requested, unseal with provided or
  generated material, mount Transit, create or verify the S.C.R.A.P. Transit
  key, and emit redacted evidence.
- The command is idempotent where OpenBao API semantics allow it:
  existing mount/key with compatible settings is success; incompatible settings
  fail closed with an actionable reason.
- Bootstrap evidence is machine-readable and human-readable, and excludes root
  tokens, unseal keys, private keys, client cert material, Transit tokens, raw
  wrapped keys, and raw dependency logs.
- Production rehearsal may call this command, but a successful command does not
  prove production OpenBao ownership or HA deployment.

**ADR requirement:**

No ADR is needed if scope stays local/prod-like bootstrap helper. Create an ADR
only if S.C.R.A.P. takes responsibility for production OpenBao deployment,
secret custody, OpenBao HA topology, or certificate/trust lifecycle.

**Evidence requirements:**

- Bootstrap succeeds against a fresh local/prod-like OpenBao.
- Bootstrap is idempotent against already-mounted Transit/key state.
- Incompatible state fails closed without mutating unsafe settings.
- Redaction tests prove no root token, unseal key, Transit token, private key,
  or key material appears in stdout, stderr, reports, logs, or tracker-ready
  evidence.
- `make production-rehearsal-security` can use the supported bootstrap path.

### DG-5: Release Documentation and Evidence Standard

**Decision:** V2 release-ready status requires release documentation and
evidence closure artifacts. This is architecture scope because evidence,
runbooks, and alert/query references define the production operating contract.

**Architecture:**

- Add a V2 release evidence matrix that maps every FR, ADR gate, GitHub issue,
  verification command, evidence artifact, and closure status.
- Add operator runbooks for:
  startup/security readiness, mTLS/certificate rotation, OpenBao Transit
  dependency, Backend upload pressure, restore failures, eviction campaigns,
  Block Quarantine repair, Content Quarantine response, multi-Shard routing
  health, and evidence bundle collection.
- Add alert/query references for:
  public/peer/admin availability, write ACK latency, read failures, restore
  failures, Backend upload lag, upload pressure, scrub/quarantine, scanner
  lag/outage, Transit outage, audit sink failure, rate-limit denials, Shard
  leader/peer health, and evidence leak-scan status.
- Update `docs/prd-closure-policy.md` so final V2 closure cannot pass with
  open decision gates, missing evidence artifacts, stale local-only evidence,
  or unlinked issue/PR proof.
- Link issue #429 into the final release evidence matrix as the real S3/IAM
  gate after feature scope is complete.

**ADR requirement:**

No ADR is needed for docs/evidence closure unless the closure policy changes
deployment, security, auth, or wire/storage contracts. Normal runbooks and
closure policy updates belong in docs.

**Evidence requirements:**

- Every release claim has a command, commit/ref, environment, expected result,
  actual result, artifact path, timestamp, and redaction proof.
- Final evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle,
  `make production-rehearsal-security`, and real S3/IAM
  `make production-rehearsal`.
- LocalStack/test endpoints are clearly marked as interim evidence only.
- Public tracker comments do not include secrets, raw logs, raw Backend keys,
  Document bytes, or private material.

## Implementation Patterns and Consistency Rules

### Source of Truth

| Concern | Source |
| --- | --- |
| Glossary and product constraints | `CONTEXT.md` |
| Durable architecture | accepted ADRs under `docs/adr/` |
| Master release scope | master PRD and this architecture artifact |
| Phase 4.5 security/encryption | existing `architecture.md` and Phase 4.5 PRD |
| Execution tracker | GitHub Issues |
| Generated planning artifacts | `_bmad-output/planning-artifacts/` |

### Naming and Package Patterns

- Use exact glossary terms: Document, Transaction, Block, Frame, Shard, Cell,
  Member, Backend, Pebble Projection, Projection Resolution, Block Quarantine,
  Content Quarantine, and Content Scanner.
- Do not use `object`, `blob`, `file`, `node`, `index`, or `queue` when a
  glossary term applies.
- New packages must be domain-specific. Do not add `internal/common`,
  `internal/util`, `internal/shared`, or `internal/helpers`.
- Candidate new packages:
  - `internal/avscan` for Content Scanner.
  - `internal/routing` for Transaction-to-Shard routing.
  - `internal/quarantine` only if quarantine state needs a package shared by
    Shard, admin, and scanner. Prefer keeping authority in `internal/shard`
    and projection helpers in `internal/index` unless duplication proves real.

### Wire and Storage Patterns

- Edit `proto/` sources and regenerate through Buf. Do not edit `gen/`
  directly.
- Raft commands carry metadata only. `QuarantineDocument` must not carry
  Document bytes or scanner payload bytes.
- Scan status fields must be additive proto changes.
- Storage and wire changes need ADR coverage before implementation.
- Pebble Projection prefix changes must be versioned and rebuildable from Raft
  state.

### Authority Patterns

- Raft owns Content Quarantine state, rewrap lifecycle, upload confirmation,
  and Shard metadata.
- Pebble Projection materializes read-side state only.
- Backend stores opaque Blocks and is never a consistency oracle.
- Local Block Lifecycle is per-Member filesystem evidence only.
- Scanner watermarks are progress evidence, not Document visibility authority.
- OTel, logs, audit, and evidence observe behavior; they do not decide state.

### Error, Redaction, and Evidence Patterns

- `ReadDocument` for Content Quarantine returns `FAILED_PRECONDITION` with a
  bounded reason.
- Backend restore transient dependency failures return `UNAVAILABLE`.
- Missing/corrupt confirmed Backend objects return `DATA_LOSS`.
- Scanner outage and lag are operator-visible but do not block writes.
- Public errors do not leak scanner signatures, YARA rule text, Transit policy
  detail, Backend object keys, raw Document identifiers, or dependency logs.
- Evidence artifacts must include command, commit/ref, environment, result,
  artifact path, and redaction proof.

### Test Patterns

- Use unit tests for pure routing, scanner status, config validation, and
  redaction logic.
- Use integration tests for Pebble Projection prefixes, Raft apply behavior,
  OpenBao bootstrap helper behavior, Backend restore, and scanner engine
  boundaries where practical.
- Use Tier 2/Tier 3 when behavior depends on deployed Cell, security surfaces,
  Cilium, OpenBao, Backend, or telemetry/evidence stack.
- Do not use sleeps as synchronization for scanner/rescan tests; use fake
  clocks, explicit triggers, readiness probes, or bounded polling with clear
  failure messages.

## Project Structure and Boundaries

### Planned Structure Additions

```text
internal/
  avscan/                 # Content Scanner scheduler, engine boundary, watermarks
  routing/                # Transaction hash slot and Shard routing contract
  index/                  # existing Pebble Projection; add quarantine/status prefixes
  shard/                  # Raft apply, Content Quarantine authority, restore-first cold reads
  admin/                  # existing HTTP admin; add quarantine operator endpoints
  scrapctl/               # add openbao bootstrap and quarantine operator UX
  security/               # existing production security primitives
  encryption/             # existing OpenBao Transit/envelope primitives
test/
  integration/
    avscan/               # scanner/quarantine projection and engine tests if split
    routing/              # multi-Shard routing and startup validation tests
  e2e/
    multishard/           # deployed multi-Shard behavior
    coldread/             # all-local-copy eviction and restore-first reads
    quarantine/           # Content Scanner and Content Quarantine behavior
docs/
  adr/
    0025-*.md             # accepted Content Quarantine admin-surface ADR
    0026-*.md             # accepted multi-Shard V2 release-boundary ADR
    0027-*.md             # accepted Phase 5 restore-first cold-read ADR
  runbooks/
    *.md                  # V2 operator runbooks
  release/
    v2-evidence-matrix.md
```

### Boundary Map

| Package/surface | Owns | Must not own |
| --- | --- | --- |
| `internal/cmd` | multi-Shard startup composition, config validation, Shard set construction | Transaction routing logic inside handlers, storage behavior |
| `internal/routing` | hash slots, route lookup, Shard map validation | Raft apply, gRPC status mapping, Backend IO |
| `internal/server` | public gRPC boundary, route to owning Shard | hardcoded Shard IDs, scanner internals |
| `internal/peer` | peer authz by Shard set, peer transport | routing authority from addresses, scanner logic |
| `internal/shard` | Raft authority, QuarantineDocument apply, restore-first cold-read orchestration | TLS parsing, OpenBao bootstrap, HTTP rendering |
| `internal/avscan` | scanner scheduling, engine adapters, scanner progress | write ACK decisions, Block Quarantine repair, admin auth |
| `internal/index` | projection prefixes for scan status/quarantine/watermarks | production storage truth |
| `internal/admin` | quarantine admin endpoints, runbook-oriented health/status | public Document API |
| `internal/scrapctl` | operator CLI for quarantine and OpenBao bootstrap | server enforcement, Shard direct imports |
| `internal/backend` | opaque byte object store | envelope parsing, direct streaming authority |

### Requirement-to-Structure Mapping

| Requirement | Structure |
| --- | --- |
| FR-5 multi-Shard | `internal/routing`, `internal/cmd`, `internal/server`, `internal/peer`, `internal/admin`, `internal/scrapctl`, e2e multishard tests |
| FR-8 cold reads | `internal/shard`, `internal/localblock`, `internal/backend`, `internal/eviction`, coldread e2e tests |
| FR-11 scanner | `internal/avscan`, `internal/shard`, `internal/index`, scanner fixtures |
| FR-12 quarantine | `proto`, `internal/shard`, `internal/index`, `internal/admin`, `internal/scrapctl` |
| FR-14 OpenBao bootstrap | `internal/scrapctl`, `test/integration/openbao_*`, production rehearsal scripts |
| FR-16 release closure | `docs/runbooks`, `docs/release`, `docs/prd-closure-policy.md`, issue #429 |

## Architecture Validation Results

### Coherence Validation

The decisions are coherent with the existing architecture:

- Content Scanner is separate from Deep Scrub.
- Content Quarantine is separate from Block Quarantine.
- Multi-Shard routing builds on existing `shard_id` proto/backend fields and
  ADR 0024 Shard-scope peer authorization.
- Restore-first cold reads extend Phase 4 restore instead of adding a second
  streaming read path.
- `scrapctl` OpenBao bootstrap stays operator tooling and does not move
  production OpenBao ownership into SCRAP.
- Evidence and docs closure extend ADR 0012 and the PRD closure policy without
  making evidence a state authority.

### Requirements Coverage Validation

All master PRD decision-gate requirements have architectural support:

- DG-1/FR-11/FR-12: supported by ADR 0025.
- DG-2/FR-5: supported by ADR 0026.
- DG-3/FR-8: supported by ADR 0027.
- DG-4/FR-14: supported without ADR if scoped to local/prod-like helper.
- DG-5/FR-16: supported through docs/closure policy artifacts.

### Gap Analysis

**Critical gaps before epics/stories:**

No critical architecture gaps remain before regenerating epics/stories. ADR
0025, ADR 0026, and ADR 0027 now close the durable architecture gates.

**Material gaps before implementation closure:**

- Content Scanner proto/Raft/Projection/admin/evidence work does not exist.
- Multi-Shard startup/routing and placement validation do not exist.
- All-local-copy eviction and serving-leader restore-first reads do not exist.
- `scrapctl openbao bootstrap` does not exist.
- V2 release evidence matrix and runbook set do not exist.
- Real S3/IAM evidence issue #429 remains open.

**Non-blocking gaps:**

- Exact file names for runbooks and release matrix can be chosen during docs
  implementation.
- Exact scanner fixture layout can be chosen during scanner story creation.
- Direct Backend streaming remains a future research/design topic only.

### Architecture Readiness Assessment

**Overall status:** READY FOR BACKLOG DESIGN, NOT READY FOR IMPLEMENTATION.

This architecture closes the direction of the five master PRD decision gates and
the required ADRs now exist. It intentionally does not claim implementation can
start immediately. The next safe step is to regenerate epics/stories from the
master PRD, this architecture, ADR 0025, ADR 0026, and ADR 0027.

**Confidence level:** medium-high.

Confidence is high in the conservative architecture direction because it follows
accepted ADR boundaries and existing code shape. Confidence is medium for exact
story slicing until `bmad-create-epics-and-stories` turns the decisions into a
validated backlog.

### Architecture Completeness Checklist

**Requirements Analysis**

- [x] Project context thoroughly analyzed
- [x] Scale and complexity assessed
- [x] Technical constraints identified
- [x] Cross-cutting concerns mapped

**Architectural Decisions**

- [x] Critical decisions documented
- [x] Technology foundation specified
- [x] Integration patterns defined
- [x] Performance and operational considerations addressed at architecture level

**Implementation Patterns**

- [x] Naming conventions established
- [x] Structure patterns defined
- [x] Communication patterns specified
- [x] Process patterns documented

**Project Structure**

- [x] Planned structure additions defined
- [x] Component boundaries established
- [x] Integration points mapped
- [x] Requirements to structure mapping complete

## Implementation Handoff

### Completed ADR Work

These ADRs are now accepted inputs for backlog generation:

1. `docs/adr/0025-content-quarantine-admin-surface.md`
2. `docs/adr/0026-multi-shard-v2-release-boundary.md`
3. `docs/adr/0027-phase-5-restore-first-cold-reads.md`

Run `bmad-create-epics-and-stories` using:

- the V2 master PRD;
- this master V2 architecture artifact;
- ADR 0025, ADR 0026, and ADR 0027;
- `docs/archive/obsolete-pre-bmad/scope-reconciliation.md`;
- current GitHub tracker state.

### First Backlog Slices

Recommended order:

1. Content Scanner / Content Quarantine implementation story group.
2. Multi-Shard routing and startup story group.
3. Phase 5 restore-first cold-read story group.
4. `scrapctl openbao bootstrap` story group.
5. V2 release runbooks/evidence matrix story group.
6. Real S3/IAM issue #429 only after required feature scope is complete.

### Handoff Rule

Do not regenerate implementation epics from the old deleted
`_bmad-output/planning-artifacts/epics.md`. Regenerate from the master PRD,
this architecture artifact, and the new accepted ADRs.
