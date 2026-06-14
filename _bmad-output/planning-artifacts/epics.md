---
stepsCompleted:
  - 1
  - 2
  - 3
  - 4
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md
  - _bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md
  - docs/v2-scope-reconciliation.md
  - docs/adr/0025-content-quarantine-admin-surface.md
  - docs/adr/0026-multi-shard-v2-release-boundary.md
  - docs/adr/0027-phase-5-restore-first-cold-reads.md
lastStep: 4
status: complete
workflowType: epics-and-stories
project_name: scrap
user_name: Coto
date: 2026-06-10
---

# scrap - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for scrap,
decomposing the requirements from the V2 master PRD, the V2 master architecture
artifact, accepted ADRs 0025 through 0027, and the V2 scope reconciliation
document into implementable stories.

This file completed the BMAD create-epics-and-stories workflow. Step 1
extracted the requirements inventory, Step 2 approved the epic structure and FR
coverage map, Step 3 created the detailed story breakdown, and Step 4 validated
coverage and story readiness.

## Requirements Inventory

### Functional Requirements

FR-1: Billing services can write, read, head, and find immutable Documents by
`(transaction_id, document_name)`.

FR-2: S.C.R.A.P. returns write ACK only after the required local and peer
durability path, committed metadata, and visibility contract are satisfied.

FR-3: Reads return verified bytes or a typed failure; S.C.R.A.P. never returns
least-bad, partial, or unverified bytes.

FR-4: Each required Shard uses Raft metadata authority and peer byte replication
so Document visibility is not decided by local files, Backend objects, cached
peer addresses, or network location.

FR-5: V2 implements multi-Shard startup/routing according to fixed hash slots
and validated placement config.

FR-6: The Shard leader uploads sealed Blocks to the Backend asynchronously and
records upload obligations and confirmations through committed metadata.

FR-7: Operators can evict eligible follower-local Block data files and restore
full Blocks from the Backend before serving reads that need evicted bytes.

FR-8: V2 implements restore-first cold reads: when all local `.blk` copies are
evicted, `ReadDocument` restores the full Block from the Backend, verifies it,
and then serves through the normal local read path.

FR-9: Production `scrapd` startup fails closed when required TLS, role policy,
peer identity policy, Transit configuration, or dangerous hook policy is
missing, invalid, or contradictory.

FR-10: Production writes encrypt new Document payload bytes before Block
persistence, reads decrypt through the normal path while preserving integrity
checks, and operators can durably rewrap envelope metadata through Raft.

FR-11: V2 includes a leader-owned background Content Scanner that scans sealed
Block bytes with ClamAV and YARA after ACK and never blocks the write path.

FR-12: Content Quarantine gates a single Document at metadata level:
`ReadDocument` denies bytes, while `HeadDocument` and `FindDocuments` expose
metadata with scan status for reconciliation.

FR-13: `scrapctl` supports production-oriented diagnostics and evidence
workflows for status, leaders, peers, upload pressure, faults, evidence bundles,
and eviction.

FR-14: V2 implements `scrapctl` OpenBao bootstrap commands for local and
prod-like operator workflows.

FR-15: S.C.R.A.P. emits OpenTelemetry metrics, logs, traces, and profiles
sufficient to prove runtime behavior, production safety, and evidence gates.

FR-16: V2 release-ready status requires linked, current, reviewable evidence
and operator documentation for every required release claim.

### NonFunctional Requirements

NFR-1: Missing security config, missing key material, corrupt metadata, corrupt
Block bytes, invalid Backend restore, unauthorized peer/admin/public access,
and unsafe production hooks fail closed.

NFR-2: Production paths do not buffer full Documents, Blocks, uploads, restores,
peer transfers, or scans in memory.

NFR-3: Raft is metadata authority; Pebble Projection, Confirmed Upload Catalog,
Backend objects, Local Block Lifecycle, audit, and OTel evidence are not storage
truth.

NFR-4: Logs, metrics, traces, audit, evidence, screenshots, fixtures, and public
tracker comments do not leak secrets, Document bytes, raw identifiers, Backend
keys, data keys, or wrapped-key ciphertext.

NFR-5: Each release claim has current evidence with command, commit/ref,
environment, expected result, actual result, artifact path, and redaction proof.

NFR-6: Storage format, wire protocol, dependency/runtime choices,
security/encryption/auth contracts, and cross-package ownership changes require
ADRs.

NFR-7: Required features need positive path, fail-closed path, restart/rebuild
or recovery path where relevant, and the narrowest local/deployed gate that
proves the claim.

### Additional Requirements

- No new starter template is selected. The existing Go/gRPC S.C.R.A.P. V2
  repository remains the foundation.
- The old Phase 4.5 PRD and architecture remain historical/phase-specific
  inputs. The master PRD, master architecture, ADR 0025, ADR 0026, ADR 0027,
  and this regenerated epics file are the current backlog inputs.
- Content Scanner work adds `internal/avscan` for scanner orchestration, scan
  engine adapters, scan scheduling, watermarks, and scanner evidence helpers.
- Content Scanner must remain separate from Deep Scrub; Content Quarantine must
  remain separate from Block Quarantine.
- The Shard leader scans sealed Blocks asynchronously after ACK. Scanner
  unavailability must not block writes.
- Scanner progress uses persisted projection state including
  `last_scanned_block_id` and `last_sig_version_scanned`.
- Signature updates reset scanning priority for rescan without changing
  Document identity or write visibility.
- Scanner hits propose a `QuarantineDocument` Raft command.
- Content Quarantine state is replicated metadata. Pebble Projection must add a
  dedicated prefix for quarantined Document identities.
- `ReadDocument` must check Content Quarantine before serving bytes and return
  `FAILED_PRECONDITION` with a bounded quarantine reason.
- `HeadDocument` and `FindDocuments` must continue to return metadata and add
  scan status for reconciliation.
- ADR 0025 amends ADR 0008 so V2 uses existing admin HTTP plus `scrapctl` for
  quarantine management, not a new gRPC AdminService.
- Admin HTTP and `scrapctl` must support list, inspect, confirm, release, and
  scanner/quarantine health or lag operations.
- Content Quarantine confirm/release must propose Raft metadata commands and
  converge through the Shard authority path.
- Quarantine admin operations must follow the Phase 4.5 production security
  boundary: production mTLS, role authorization, audit, per-surface rate
  limits, redaction, and no Document bytes in admin responses.
- Multi-Shard work adds a Shard routing boundary, likely `internal/routing`,
  for fixed hash-slot count, Transaction-to-slot hashing, slot-to-Shard
  mapping, validation, route lookup, routing telemetry, and admin diagnostics.
- `internal/cmd` must build the configured Shard set, wire per-Shard telemetry
  and transports, and fail production startup when the Shard map is invalid or
  incomplete.
- Public gRPC handlers must not hardcode Shard IDs. They should call a
  Store-compatible router or narrow interface that delegates to the owning
  Shard.
- `internal/peer` must receive the authorized Shard set from validated
  placement membership and deny out-of-scope Shard RPCs before side effects.
- `internal/admin` and `scrapctl` must expose Shard-aware status without
  conflating Cell, Member, Shard, or peer identity.
- Upload Outbox, Confirmed Upload Catalog, eviction, restore, scrub, repair,
  Content Scanner, and Content Quarantine remain Shard-local authority flows.
- Phase 5 extends eviction so every local `.blk` copy of an uploaded sealed
  Block may be intentionally evicted when policy allows.
- Restore-first cold reads must restore the full Block data file to local
  storage before serving bytes through the normal local Block reader.
- Restore is allowed only from committed Confirmed Upload Catalog metadata.
  Backend list or inventory output is not authority.
- Restored `.blk` bytes must be staged safely, verified against committed
  Backend metadata and retained local `.idx` metadata, and published atomically.
- Failed restore must not publish partial bytes. Successful restore records
  local lifecycle evidence without changing Document visibility.
- Restore remains a per-Block singleflight operation owned by the Shard, bounded
  by concurrency, timeout, context cancellation, and backpressure.
- Metadata-only reads must not restore Block bytes.
- Encryption behavior remains unchanged for restore-first cold reads: Backend
  stores ciphertext Blocks, restore downloads ciphertext, Frame CRC verifies
  ciphertext storage integrity, normal read decrypts through the envelope path,
  and plaintext SHA-256 verifies before return.
- Direct Backend ciphertext streaming, Backend range streaming, and per-Frame
  remote reads are out of V2 unless re-chartered by a later accepted ADR or PRD.
- `scrapctl openbao bootstrap` must initialize local/prod-like OpenBao when
  requested, unseal with provided or generated material, mount Transit, create
  or verify the S.C.R.A.P. Transit key, and emit redacted evidence.
- `scrapctl openbao bootstrap` must use the official OpenBao Go API client.
- OpenBao bootstrap must be idempotent where OpenBao API semantics allow it:
  compatible existing mount/key state is success, incompatible state fails
  closed with an actionable reason.
- OpenBao bootstrap evidence must exclude root tokens, unseal keys, private
  keys, client cert material, Transit tokens, raw wrapped keys, and raw
  dependency logs.
- S.C.R.A.P. does not own production OpenBao deployment, secret custody, storage
  backend setup, high-availability OpenBao topology, or long-term OpenBao
  lifecycle for V2.
- V2 release scope requires a release evidence matrix that maps every FR, ADR
  gate, GitHub issue, verification command, evidence artifact, and closure
  status.
- Operator runbooks are required for startup/security readiness,
  mTLS/certificate rotation, OpenBao Transit dependency, Backend upload
  pressure, restore failures, eviction campaigns, Block Quarantine repair,
  Content Quarantine response, multi-Shard routing health, and evidence bundle
  collection.
- Alert/query references are required for public/peer/admin availability, write
  ACK latency, read failures, restore failures, Backend upload lag, upload
  pressure, scrub/quarantine, scanner lag/outage, Transit outage, audit sink
  failure, rate-limit denials, Shard leader/peer health, and evidence leak-scan
  status.
- `docs/prd-closure-policy.md` must prevent final V2 closure with open decision
  gates, missing evidence artifacts, stale local-only evidence, or unlinked
  issue/PR proof.
- Issue #429 remains the real S3/IAM production rehearsal gate and should be
  linked into the final release evidence matrix after feature scope is complete.
- Evidence for Content Scanner/Quarantine must prove scheduling, scanner outage
  visibility, detection-to-Raft, quarantined read denial, metadata scan status,
  confirm/release convergence, rescan behavior, and redaction.
- Evidence for multi-Shard must prove at least two Shards, deterministic
  routing, wrong-Shard peer denial, per-Shard admin status, and Backend
  upload/restore behavior under non-zero Shard IDs.
- Evidence for Phase 5 cold reads must prove all-local-copy eviction,
  restore-on-read, concurrent read singleflight, Backend transient failure,
  Backend missing/corrupt failure, encryption interaction, and no raw identifier
  or Backend key leaks.
- Evidence for OpenBao bootstrap must prove fresh bootstrap, idempotency,
  incompatible-state failure, redaction, and use by production security
  rehearsal where applicable.
- Final V2 evidence must include Tier 2 prod-like Cilium, Tier 3 evidence
  bundle, `make production-rehearsal-security`, and real S3/IAM
  `make production-rehearsal`.

### UX Design Requirements

No UX Design document was found in the planning artifact search. No UI-specific
UX Design requirements were extracted for this workflow step. Operator CLI and
admin workflow requirements are captured under Additional Requirements.

### FR Coverage Map

FR-1: Epic 1 - Billing ETL can write, read, head, and find immutable Documents
by `(transaction_id, document_name)`.

FR-2: Epic 1 - Write ACK is proven to come only after required local and peer
durability, committed metadata, and visibility.

FR-3: Epic 1 - Read paths return verified bytes or typed failures; partial or
unverified bytes are never returned.

FR-4: Epic 2 - Shard authority, Raft metadata, and peer byte replication are
preserved across Cell operation.

FR-5: Epic 2 - Multi-Shard startup and Transaction routing work through fixed
hash slots and validated placement config.

FR-6: Epic 3 - Sealed Blocks upload asynchronously through committed upload
obligations and confirmations.

FR-7: Epic 3 - Operators can evict eligible local Block data and restore full
Blocks before reads that need evicted bytes.

FR-8: Epic 3 - Cold reads restore and verify full Blocks before serving through
the normal local read path.

FR-9: Epic 4 - Production startup and security-sensitive surfaces fail closed
when required security configuration is missing, invalid, or contradictory.

FR-10: Epic 4 - Production writes use OpenBao-backed envelope encryption and
operators can durably rewrap envelope metadata through Raft.

FR-11: Epic 5 - Leader-owned Content Scanner scans sealed Block bytes after ACK
without blocking writes.

FR-12: Epic 5 - Content Quarantine denies unsafe reads while preserving
metadata reconciliation and admin confirm/release operations.

FR-13: Epic 6 - `scrapctl` supports release-relevant diagnostics and evidence
workflows.

FR-14: Epic 4 - `scrapctl openbao bootstrap` supports local/prod-like OpenBao
bootstrap workflows.

FR-15: Epic 6 - OpenTelemetry metrics, logs, traces, and profiles prove runtime
behavior, production safety, and evidence gates.

FR-16: Epic 6 - V2 release-ready status is backed by linked, current,
reviewable evidence and operator documentation.

## Epic List

### Epic 1: Billing ETL Can Trust Immutable Document Writes and Reads

Billing service engineers can write, read, head, and find immutable Documents by
`(transaction_id, document_name)` and trust that ACK, idempotency, visibility,
and all-or-error reads preserve the core S.C.R.A.P. contract.

**FRs covered:** FR-1, FR-2, FR-3.

**Evidence contract:** Owns ACK, idempotency, read/head/find, fail-closed
metadata corruption, restart/rebuild, cancellation, and redaction evidence.
Acceptance must include routing-boundary coverage even when a story uses a
single-Shard fixture.

### Epic 2: Operators Can Run a Shard-Aware Cell

Platform operators can start and diagnose a Cell with multiple Shards,
deterministic Transaction routing, Shard-scoped peer authorization, and
Shard-aware admin and `scrapctl` diagnostics without confusing Cell, Member,
Shard, or peer identity.

**FRs covered:** FR-4, FR-5.

**Evidence contract:** Owns deterministic routing, at least two Shards,
wrong-Shard peer denial before side effects, Shard-aware diagnostics, invalid
placement startup failure, non-zero Shard Backend upload/restore behavior, and
redaction evidence.

### Epic 3: Operators Can Prove Backend Durability and Restore-First Cold Reads

Storage operators can prove sealed Blocks upload through committed metadata,
eligible local copies can be evicted under policy, and cold `ReadDocument`
requests restore and verify full Blocks before serving bytes through the normal
local read path.

**FRs covered:** FR-6, FR-7, FR-8.

**Evidence contract:** Owns upload confirmation, upload pressure, eviction,
all-local-copy eviction, restore-on-read, concurrent restore singleflight,
Backend transient failure, missing/corrupt Backend object failure, encryption
interaction, cancellation, and redaction evidence.

### Epic 4: Operators Can Run Fail-Closed Security and OpenBao Workflows

Security and platform operators can run production security mode with
fail-closed startup, mTLS, authorization, audit, rate limits, OpenBao-backed
envelope encryption, durable rewrap, and `scrapctl openbao bootstrap` for
local/prod-like workflows.

**FRs covered:** FR-9, FR-10, FR-14.

**Evidence contract:** Owns production fail-closed security, peer/admin/public
surface authorization, audit/rate-limit evidence, encrypted write/read, Transit
outage failure, durable rewrap, bootstrap idempotency, incompatible-state
failure, and secret/key-material redaction evidence.

### Epic 5: Security Operators Can Contain Unsafe Content Without Mutating Documents

Security operators can scan sealed Block bytes after ACK, quarantine suspicious
Documents through Raft-owned metadata, deny unsafe reads, preserve
`HeadDocument` and `FindDocuments` reconciliation metadata, and confirm or
release quarantine through admin HTTP plus `scrapctl`.

**FRs covered:** FR-11, FR-12.

**Evidence contract:** Owns scan scheduling, scanner outage visibility,
detection-to-Raft, persisted watermarks, rescan behavior, quarantined read
denial, metadata scan status, admin confirm/release convergence, quarantine
race handling, and redaction evidence. `internal/avscan` scans and reports;
Shard/Raft owns quarantine authority.

### Epic 6: Release Owners Can Prove V2 Readiness

Release owners can reconcile feature evidence into a V2 release decision using
`scrapctl`, OpenTelemetry evidence, runbooks, alert/query references, a release
evidence matrix, closure policy updates, and final real S3/IAM production
rehearsal after feature scope is complete.

Epic 6 aggregates, audits, bundles, and gates evidence. It must not introduce
new product behavior that belongs in Epics 1 through 5.

**FRs covered:** FR-13, FR-15, FR-16.

**Evidence contract:** Owns final evidence aggregation, traceability, runbook
validation, alert/query references, evidence leak scanning, Tier 2 prod-like
Cilium, Tier 3 evidence bundle, `make production-rehearsal-security`, real
S3/IAM `make production-rehearsal`, and final PASS/CONCERNS/FAIL release gate.

### Cross-Epic Release Rules

- V2 is not releasable until all six epics and the final release evidence
  matrix are complete.
- Feature-specific telemetry, tests, redaction checks, `scrapctl` commands, and
  runbook hooks belong with the feature epic that creates the risk.
- Epic 6 aggregates, reconciles, audits, and gates evidence. It does not defer
  feature evidence that belongs in Epics 1 through 5.
- Security and redaction are cross-epic acceptance criteria wherever public,
  peer, admin, `scrapctl`, logs, metrics, traces, audit, screenshots, or
  evidence artifacts are introduced or changed.
- Any missing P0 evidence is FAIL. Any high-risk evidence gap with an owner and
  mitigation is CONCERNS. Silent waivers are not allowed.
- Any confirmed or plausible data-integrity bug discovered after story closure
  reopens the owning feature or evidence epic and blocks final release PASS
  until a fix, regression test, verification command, and release artifact are
  linked.
- Stories should touch one primary boundary when practical: proto contract,
  `internal/cmd` composition, `internal/server` routing, store/router,
  `internal/peer`, `internal/shard`, `internal/index`, `internal/backend`,
  `internal/admin`, `scrapctl`, or docs/evidence.
- Generated proto files are acceptance artifacts only; never hand-edit them.
- Every story must include AC IDs, failure-path tests, evidence command, and
  changed-boundary list.

## Epic 1: Billing ETL Can Trust Immutable Document Writes and Reads

Billing service engineers can write, read, head, and find immutable Documents by
`(transaction_id, document_name)` and trust that ACK, idempotency, visibility,
and all-or-error reads preserve the core S.C.R.A.P. contract.

### Story 1.1: Durable Document Write ACK

**Requirements:** FR-2.

As a billing service engineer,
I want Document writes ACKed only after required durability and visibility,
So that upstream billing workflows can trust accepted writes.

**Acceptance Criteria:**

**Given** a valid Document write
**When** the write completes
**Then** ACK is returned only after required local durability, peer durability,
committed metadata, and visibility are satisfied.
**And** the evidence identifies AC-1.1.1, the verification command, result,
changed-boundary list, and redaction proof.

**Given** local or peer durability fails before commit
**When** the write is attempted
**Then** no ACK is returned and no partial success is exposed.
**And** AC-1.1.2 evidence proves no Document bytes or raw identifiers appear in
logs, metrics, traces, or artifacts.

**Given** tests exercise the write ACK path through the routing boundary
**When** a single-Shard fixture is used
**Then** the fixture still routes through the Store-compatible boundary rather
than hardcoding Shard ID assumptions.
**And** AC-1.1.3 documents the changed boundary and future multi-Shard
compatibility claim.

**Given** the process crashes after durable local/peer write work but before
the client observes ACK
**When** the client retries the same write
**Then** recovery and replay do not create divergent Document state.
**And** AC-1.1.4 evidence proves retry behavior remains idempotent or fails
with a deterministic typed outcome.

### Story 1.2: Immutable Replay and Conflict Handling

**Requirements:** FR-1.

As a billing service engineer,
I want duplicate writes to distinguish exact replay from conflicting payloads,
So that immutable Document identity is preserved.

**Acceptance Criteria:**

**Given** an exact replay for `(transaction_id, document_name)`
**When** the write is submitted
**Then** the response is idempotent and does not create a second Document.
**And** AC-1.2.1 evidence includes the command and persisted-state check.

**Given** a conflicting payload or metadata for the same identity
**When** the write is submitted
**Then** S.C.R.A.P. rejects it with a typed failure.
**And** AC-1.2.2 evidence proves no overwrite, mutation, or duplicate visible
Document is created.

**Given** logs, metrics, traces, or test artifacts are emitted
**When** replay and conflict paths run
**Then** raw identifiers and Document bytes are redacted.
**And** AC-1.2.3 evidence records the leak-scan command and result.

**Given** replay observes a partial or corrupt committed-log/projection entry
for the same Document identity
**When** duplicate or conflicting write handling runs
**Then** the response is deterministic and fails closed instead of inventing a
second visible Document.
**And** AC-1.2.4 evidence records the corrupt/replay fixture and expected typed
failure.

### Story 1.3: Verified Read and Metadata Inspection

**Requirements:** FR-3.

As a billing service engineer,
I want `ReadDocument` and `HeadDocument` to return verified data or typed
failures,
So that billing consumers never process partial or corrupt bytes.

**Acceptance Criteria:**

**Given** a committed visible Document
**When** `ReadDocument` is called
**Then** returned bytes pass Block/Frame and Document verification.
**And** AC-1.3.1 evidence includes the verification command and changed-boundary
list.

**Given** visible metadata or Block bytes are corrupt
**When** read/head paths are called
**Then** the operation fails closed with the expected typed error.
**And** AC-1.3.2 evidence proves no least-bad, partial, or unverified bytes are
returned.

**Given** request cancellation occurs
**When** read work is in progress
**Then** the operation stops without leaking goroutines or returning partial
bytes.
**And** AC-1.3.3 evidence includes cancellation and cleanup proof.

**Given** internal metadata carries route or Shard context
**When** read/head verification executes in a single-Shard fixture
**Then** the contract preserves that context without adding `tenant_id` to
storage identity.
**And** AC-1.3.4 evidence records the storage/index/replay boundary assumptions
for Epic 2 routing.

### Story 1.4: Transaction-Scoped Document Discovery

**Requirements:** FR-1.

As a billing service engineer,
I want to find Documents for a Transaction without storage internals leaking,
So that reconciliation can use public metadata safely.

**Acceptance Criteria:**

**Given** multiple Documents exist for a Transaction
**When** `FindDocuments` is called
**Then** the response is scoped to that Transaction and excludes storage
implementation details.
**And** AC-1.4.1 evidence proves route scope without exposing Backend object
keys or raw local file paths.

**Given** no Documents exist
**When** `FindDocuments` is called
**Then** the response is typed, stable, and does not imply Backend inventory
state.
**And** AC-1.4.2 evidence proves Backend list, HEAD, or inventory output is not
used as public API authority.

**Given** evidence is generated
**When** find paths run
**Then** telemetry stays low-cardinality and redacted.
**And** AC-1.4.3 evidence records the leak-scan command and result.

**Given** this story implements Transaction-scoped discovery semantics
**When** public cross-Shard routing is needed
**Then** routing remains owned by Story 2.3 rather than hidden in discovery
logic.
**And** AC-1.4.4 evidence records whether the story touched index/query code,
public routing code, or both.

### Story 1.5: Core Gateway Restart and Rebuild Evidence

**Requirements:** FR-1, FR-2, FR-3.

As a release owner,
I want core write/read/head/find behavior proven across restart and Projection
rebuild,
So that Epic 1 is not closed from happy-path tests alone.

**Acceptance Criteria:**

**Given** committed Documents exist
**When** a Member restarts or Projection state is rebuilt
**Then** visible Documents remain readable or fail closed according to authority
state.
**And** AC-1.5.1 evidence links restart/rebuild command output.

**Given** Projection state is stale or missing
**When** rebuild completes
**Then** public behavior follows Raft and verified Block bytes, not stale
Projection assumptions.
**And** AC-1.5.2 evidence proves Pebble Projection is not treated as storage
truth.

**Given** Epic 1 evidence is collected
**When** closure is evaluated
**Then** ACK, replay/conflict, read/head/find, restart/rebuild, cancellation,
and redaction evidence are linked.
**And** AC-1.5.3 records PASS, CONCERNS, or FAIL using the V2 release gate
language.

**Given** replay/rebuild covers records with Shard context
**When** Projection state is rebuilt
**Then** storage identity remains `(transaction_id, document_name)` while Shard
authority and route context remain recoverable.
**And** AC-1.5.4 evidence records the Shard-context fixture used by Epic 2.

### Story 1.6: Fail Closed on Missing Document SHA-256 Verification

**Requirements:** FR-3, NFR-1, NFR-7, NFR-8.

As a release owner,
I want visible Document reads to fail closed when committed metadata lacks a
valid SHA-256 digest,
So that S.C.R.A.P. never serves unverified bytes.

**Acceptance Criteria:**

**Given** a Block reader entry with an all-zero SHA-256 digest
**When** read verification runs
**Then** the read fails closed instead of skipping Document digest verification.
**And** AC-1.6.1 evidence includes the targeted `internal/block` regression test.

**Given** valid historical fixtures
**When** read verification runs
**Then** the implementation either proves all production metadata has non-zero
SHA-256 or maps zero digest entries to a typed corruption failure.
**And** AC-1.6.2 evidence records the compatibility decision and affected
boundary.

**Given** shard-level read verification is exercised
**When** a zero-digest metadata fixture is visible
**Then** S.C.R.A.P. returns a typed failure and no partial or unverified bytes.
**And** AC-1.6.3 evidence records the shard-level read verification command.

**Given** release evidence is updated
**When** final closure is evaluated
**Then** the affected FR-3 row records PASS, CONCERNS, or FAIL with the fix,
test, command, and artifact linked.
**And** AC-1.6.4 evidence proves the data-integrity blocker is no longer open.

## Epic 2: Operators Can Run a Shard-Aware Cell

Platform operators can start and diagnose a Cell with multiple Shards,
deterministic Transaction routing, Shard-scoped peer authorization, and
Shard-aware admin and `scrapctl` diagnostics without confusing Cell, Member,
Shard, or peer identity.

### Story 2.1: Shard Routing Boundary and Placement Validation

**Requirements:** FR-5.

As a platform operator,
I want Transaction routing defined by validated Shard placement,
So that every Transaction maps deterministically to one owning Shard.

**Acceptance Criteria:**

**Given** a valid slot-to-Shard map
**When** startup validates routing config
**Then** all slots are covered exactly once.
**And** AC-2.1.1 evidence includes the validation command, changed-boundary list,
and route-map summary without raw Transaction identifiers.

**Given** duplicate, missing, or unknown Shard ownership
**When** production startup runs
**Then** startup fails closed before serving traffic.
**And** AC-2.1.2 evidence proves no public, peer, or admin listener accepts
traffic after invalid placement is detected.

**Given** routing telemetry is emitted
**When** route lookup runs
**Then** labels are low-cardinality and do not expose raw Transaction
identifiers.
**And** AC-2.1.3 evidence records telemetry and redaction checks.

### Story 2.2: Multi-Shard Cell Startup Composition

**Requirements:** FR-4, FR-5.

As a platform operator,
I want `scrapd` to compose multiple Shards from validated config,
So that one Cell can run the required V2 multi-Shard topology.

**Acceptance Criteria:**

**Given** valid multi-Shard placement
**When** `scrapd` starts
**Then** `internal/cmd` constructs and wires the configured Shard set.
**And** AC-2.2.1 evidence identifies the composition boundary and startup
verification command.

**Given** local Member placement is invalid
**When** startup runs
**Then** startup fails closed with an actionable, non-sensitive error.
**And** AC-2.2.2 evidence proves the error does not leak sensitive peer
addresses, cert material, or local filesystem paths.

**Given** at least two Shards are configured
**When** evidence is collected
**Then** per-Shard lifecycle state is visible without confusing Cell, Member, or
Shard identity.
**And** AC-2.2.3 evidence includes redacted per-Shard status output.

### Story 2.3: Public API Routes by Transaction

**Requirements:** FR-5.

As a billing service engineer,
I want public Document API calls routed to the owning Shard,
So that write/read/head/find behavior works without hardcoded Shard ID
assumptions.

**Acceptance Criteria:**

**Given** a Transaction maps to Shard A
**When** write/read/head/find calls arrive
**Then** server handlers route through the Store-compatible routing boundary to
Shard A.
**And** AC-2.3.1 evidence proves handlers do not hardcode Shard ID constants.

**Given** a different Transaction maps to Shard B
**When** the same calls arrive
**Then** they route to Shard B without handler-level Shard constants.
**And** AC-2.3.2 evidence covers at least two Shards and both read and write
paths.

**Given** route lookup fails
**When** a public request is handled
**Then** the request fails safely and does not fall back to local files or
Backend inventory.
**And** AC-2.3.3 evidence records the typed failure and redaction proof.

**Given** placement validation or Shard-set composition has not completed
successfully
**When** public API routing would otherwise start
**Then** public routing fails closed instead of serving through a default Shard.
**And** AC-2.3.4 evidence proves Stories 2.1 and 2.2 are prerequisites for
production routing.

### Story 2.4: Peer RPC Shard-Scope Authorization

**Requirements:** FR-4, FR-5.

As a platform operator,
I want peer RPCs authorized by Shard scope,
So that one peer cannot mutate or read Shard state it is not authorized for.

**Acceptance Criteria:**

**Given** a peer is authorized for Shard A
**When** it calls a Shard A peer RPC
**Then** the request may proceed to the normal handler path.
**And** AC-2.4.1 evidence proves authorization is derived from validated
placement membership.

**Given** the same peer calls a Shard B peer RPC
**When** it is not authorized for Shard B
**Then** the request is denied before side effects.
**And** AC-2.4.2 evidence proves no Raft, replication, rebuild, or Block
transfer side effect occurs.

**Given** denial evidence is emitted
**When** wrong-Shard access is tested
**Then** audit, log, and metric output is redacted and bounded.
**And** AC-2.4.3 evidence records the denied-operation command and leak-scan
result.

**Given** peer membership is stale or the caller lacks required auth context
**When** a Shard-carrying peer RPC arrives
**Then** the request fails closed before Raft, replication, rebuild, or Block
transfer side effects.
**And** AC-2.4.4 evidence covers wrong Shard, stale membership, and missing auth
context cases.

### Story 2.5: Shard-Aware Admin and `scrapctl` Diagnostics

**Requirements:** FR-5.

As a platform operator,
I want admin HTTP and `scrapctl` to show Shard-aware status,
So that I can diagnose routing, leadership, peers, and health per Shard.

**Acceptance Criteria:**

**Given** a multi-Shard Cell is running
**When** admin status is requested
**Then** output identifies per-Shard health, leader, peer, and route state.
**And** AC-2.5.1 evidence proves status is read-only and does not mutate Shard
state.

**Given** `scrapctl` queries the Cell
**When** it renders diagnostics
**Then** it preserves Cell, Member, and Shard terminology exactly.
**And** AC-2.5.2 evidence includes CLI output examples and changed-boundary
list.

**Given** diagnostic evidence is generated
**When** outputs are captured
**Then** no raw identifiers, sensitive peer addresses, or secret material leak.
**And** AC-2.5.3 evidence records redaction checks for admin and CLI outputs.

**Given** admin or `scrapctl` diagnostics run under production profile
**When** required auth, TLS, or role policy is missing
**Then** diagnostics fail closed instead of downgrading to a development
fallback.
**And** AC-2.5.4 evidence records the denied diagnostic path and redaction
proof.

### Story 2.6: Multi-Shard Evidence Closure

**Requirements:** FR-4, FR-5.

As a release owner,
I want multi-Shard behavior proven through failure and restart cases,
So that Epic 2 cannot close from a happy-path startup demo.

**Acceptance Criteria:**

**Given** a two-or-more-Shard Cell
**When** restart/rebuild evidence is collected
**Then** routing and Shard authority remain deterministic.
**And** AC-2.6.1 evidence links restart/rebuild command output.

**Given** non-zero Shard IDs are used
**When** Backend upload/restore evidence is sampled
**Then** object identity and diagnostics remain Shard-scoped.
**And** AC-2.6.2 evidence proves Backend keys are not used as public routing
authority.

**Given** Epic 2 closure is evaluated
**When** evidence is reviewed
**Then** deterministic routing, invalid startup failure, wrong-Shard denial,
diagnostics, restart/rebuild, and redaction evidence are linked.
**And** AC-2.6.3 records PASS, CONCERNS, or FAIL using the V2 release gate
language.

### Story 2.7: Bound Peer `ReplicateDocument` Input Before Side Effects

**Requirements:** FR-4, FR-5, NFR-2, NFR-3, NFR-8.

As a platform operator,
I want peer Document replication to enforce the same input bounds as public
writes before allocation-heavy work or side effects,
So that a buggy or compromised peer cannot pressure memory, disk, or replica
state outside the Document contract.

**Acceptance Criteria:**

**Given** a peer replication init has invalid transaction ID, Document name, or
content type
**When** `ReplicateDocument` receives it
**Then** the request is rejected before Block writer or replication sink side
effects.
**And** AC-2.7.1 evidence records the validation test and changed-boundary list.

**Given** a peer stream sends a chunk larger than `MaxClientChunkBytes`
**When** the chunk is received
**Then** replication fails with a bounded typed error before buffering the full
Document.
**And** AC-2.7.2 evidence records the peer transport test.

**Given** total replicated bytes exceed `MaxDocumentBytes`
**When** the stream continues
**Then** replication fails without publishing accepted state.
**And** AC-2.7.3 evidence proves no committed metadata or visible Document is
created.

**Given** the `replicationSink` path is configured
**When** oversized input arrives
**Then** input is bounded before `bytes.Buffer` can grow without limit.
**And** AC-2.7.4 evidence covers the sink path.

**Given** the fix is reviewed
**When** package boundaries are checked
**Then** `internal/peer` remains a transport boundary connected to Shard
behavior through narrow interfaces.
**And** AC-2.7.5 evidence records `go test ./internal/peer/... ./internal/cmd/...`.

### Story 2.8: Reject Malformed `ForwardRaftStream` Messages

**Requirements:** FR-4, FR-5, NFR-3, NFR-8.

As a platform operator,
I want malformed streamed Raft messages to fail visibly,
So that peer transport bugs cannot silently drop authority messages.

**Acceptance Criteria:**

**Given** malformed protobuf bytes arrive on `ForwardRaftStream`
**When** the peer server handles the message
**Then** the stream returns an observable error instead of `nil`.
**And** AC-2.8.1 evidence records the malformed-stream regression test.

**Given** a malformed message is received
**When** handling fails
**Then** no Raft route side effect occurs.
**And** AC-2.8.2 evidence records the no-route assertion.

**Given** malformed input is audited
**When** evidence is reviewed
**Then** audit and log output remains bounded and redacted.
**And** AC-2.8.3 evidence records the redaction review result.

**Given** unary `ForwardRaft` and streaming `ForwardRaftStream` receive malformed
messages
**When** errors are mapped
**Then** both paths have consistent observable rejection semantics.
**And** AC-2.8.4 evidence records `go test ./internal/peer/...`.

## Epic 3: Operators Can Prove Backend Durability and Restore-First Cold Reads

Storage operators can prove sealed Blocks upload through committed metadata,
eligible local copies can be evicted under policy, and cold `ReadDocument`
requests restore and verify full Blocks before serving bytes through the normal
local read path.

### Story 3.1: Committed Backend Upload Confirmation

**Requirements:** FR-6.

As a storage operator,
I want sealed Blocks uploaded and confirmed through committed metadata,
So that Backend durability is observable without entering the write ACK path.

**Acceptance Criteria:**

**Given** a sealed Block pending upload
**When** the Shard leader uploads it
**Then** upload obligations and confirmations are recorded through committed
metadata.
**And** AC-3.1.1 evidence identifies the command, changed-boundary list, and
Shard-local authority path.

**Given** Backend upload is delayed or fails transiently
**When** writes continue within local durability runway
**Then** write ACK behavior remains independent of Backend success.
**And** AC-3.1.2 evidence proves Backend upload is not in the ACK path.

**Given** upload evidence is emitted
**When** artifacts are captured
**Then** Backend keys and raw identifiers are redacted.
**And** AC-3.1.3 evidence records the leak-scan command and result.

**Given** Backend upload succeeds but committed upload-confirmation metadata
does not commit
**When** recovery or evidence reads upload state
**Then** S.C.R.A.P. does not report a false committed upload confirmation.
**And** AC-3.1.4 evidence records the split-success fixture and authority
decision.

### Story 3.2: Upload Pressure and Safe Admission Evidence

**Requirements:** FR-6.

As a storage operator,
I want upload lag to create safe, observable pressure before local durability
runway is unsafe,
So that the Cell degrades before durability is compromised.

**Acceptance Criteria:**

**Given** upload lag grows past configured thresholds
**When** admission decisions are evaluated
**Then** pressure state becomes visible before unsafe local storage runway.
**And** AC-3.2.1 evidence links pressure state to committed upload obligations.

**Given** pressure clears
**When** uploads catch up
**Then** admission behavior recovers without manual metadata edits.
**And** AC-3.2.2 evidence proves recovery follows committed state, not local
operator mutation.

**Given** pressure telemetry is produced
**When** evidence is collected
**Then** labels are bounded and redacted.
**And** AC-3.2.3 evidence records telemetry and redaction checks.

**Given** admission is rejected under upload pressure
**When** a write attempt is stopped
**Then** no partial Block, Frame, or visibility metadata is left as accepted
state.
**And** AC-3.2.4 evidence records cleanup and recovery behavior.

### Story 3.3: Policy-Gated Local Block Eviction

**Requirements:** FR-7.

As a storage operator,
I want eligible local `.blk` copies evicted only under committed policy gates,
So that local disk can be reclaimed without losing authority or metadata.

**Acceptance Criteria:**

**Given** a sealed Block has committed upload confirmation
**When** eviction dry-run executes
**Then** eligibility is reported without mutating local state.
**And** AC-3.3.1 evidence identifies the dry-run command and changed boundary.

**Given** eviction apply executes for an eligible copy
**When** local lifecycle markers update
**Then** `.idx` metadata remains available for metadata-only reads.
**And** AC-3.3.2 evidence proves Local Block Lifecycle remains per-Member
filesystem evidence only.

**Given** a Block is ineligible
**When** eviction is requested
**Then** the request fails closed with actionable, non-sensitive output.
**And** AC-3.3.3 evidence records the failure command and redaction proof.

### Story 3.4: Restore-First Cold Read Path

**Requirements:** FR-8.

As a billing service engineer,
I want cold reads to restore and verify the full Block before serving bytes,
So that reads preserve all-or-error behavior after all local `.blk` copies are
evicted.

**Acceptance Criteria:**

**Given** all local `.blk` copies for a confirmed uploaded Block are evicted
**When** `ReadDocument` is called
**Then** the Shard restores the full Block from Backend to staged local storage
before serving bytes.
**And** AC-3.4.1 evidence proves restore is allowed only from committed
Confirmed Upload Catalog metadata.

**Given** restore succeeds
**When** bytes are published
**Then** the restored Block is verified against retained `.idx`, committed
metadata, Frame CRCs, and Document SHA-256 before return.
**And** AC-3.4.2 evidence proves no partial or unverified bytes are returned.

**Given** restore is in progress
**When** concurrent reads need the same Block
**Then** they coalesce behind one per-Block restore instead of duplicate Backend
downloads.
**And** AC-3.4.3 evidence records bounded concurrency, timeout, cancellation,
and backpressure behavior.

**Given** restore-first read runs under production profile
**When** restore prerequisites or security gates are not satisfied
**Then** the read fails closed instead of using a local/debug fallback.
**And** AC-3.4.4 evidence records the production-profile failure result.

### Story 3.5: Restore Failure and Corruption Semantics

**Requirements:** FR-8.

As a storage operator,
I want Backend restore failures mapped to precise typed outcomes,
So that operators can distinguish transient dependency failures from data loss.

**Acceptance Criteria:**

**Given** Backend is transiently unavailable
**When** restore-first read runs
**Then** the public operation maps to `UNAVAILABLE` and returns no partial
bytes.
**And** AC-3.5.1 evidence records the dependency-failure command and result.

**Given** a confirmed Backend object is missing or corrupt
**When** restore verification runs
**Then** the public operation maps to `DATA_LOSS` and publishes no partial local
Block.
**And** AC-3.5.2 evidence proves no staged bytes are atomically published after
verification failure.

**Given** client cancellation occurs during restore
**When** the client stops waiting
**Then** no partial bytes are returned and restore lifecycle remains bounded
and observable.
**And** AC-3.5.3 evidence records cancellation and cleanup proof.

**Given** restore times out, Backend returns not-found, payload checksum
mismatches, or retry budget is exhausted
**When** restore-first read runs
**Then** the outcome maps to the documented typed failure and publishes no
partial local Block.
**And** AC-3.5.4 evidence records timeout, 404, corrupt payload, checksum
mismatch, and retry behavior.

### Story 3.6: Encryption-Compatible Restore Evidence

**Requirements:** FR-8.

As a security operator,
I want restore-first reads to preserve the encrypted Block contract,
So that cold reads do not bypass OpenBao, checksum, or plaintext verification
behavior.

**Acceptance Criteria:**

**Given** Backend stores ciphertext Blocks
**When** restore downloads a Block
**Then** Frame CRC verifies stored ciphertext and normal read decrypts through
the envelope path.
**And** AC-3.6.1 evidence proves direct Backend ciphertext streaming remains
out of V2 scope.

**Given** Transit is unavailable or key material is invalid
**When** restored bytes are read
**Then** the read fails closed without plaintext leakage.
**And** AC-3.6.2 evidence records the fail-closed command and redaction proof.

**Given** rewrap metadata changed after upload
**When** a restored Document is read
**Then** envelope metadata converges through Raft and plaintext SHA-256 verifies
before return.
**And** AC-3.6.3 evidence proves key material and wrapped-key ciphertext are not
leaked.

**Given** production OpenBao integration is outside this story's primary changed
boundary
**When** Epic 3 evaluates encryption-compatible restore
**Then** the story uses existing encryption fixtures or a test envelope adapter
without claiming final production OpenBao proof.
**And** AC-3.6.4 evidence states which adapter or fixture was used and marks
final production OpenBao interaction as release evidence owned by Epic 4.

**Given** the key service is unavailable or a wrong key version is selected
**When** restored ciphertext is read
**Then** the read fails closed without plaintext leakage.
**And** AC-3.6.5 evidence records unavailable-key-service and wrong-key-version
cases.

### Story 3.7: Backend Durability and Cold-Read Closure Evidence

**Requirements:** FR-6, FR-7, FR-8.

As a release owner,
I want Backend upload, eviction, restore, and failure evidence linked,
So that Epic 3 cannot close from a happy-path restore demo.

**Acceptance Criteria:**

**Given** Epic 3 evidence is collected
**When** closure is evaluated
**Then** upload confirmation, pressure, eviction, all-local-copy restore,
concurrent restore, failure mapping, encryption interaction, cancellation, and
redaction evidence are linked.
**And** AC-3.7.1 records the artifact paths and owning stories.

**Given** Backend inventory, list, or HEAD output exists
**When** evidence is reviewed
**Then** no hot read/write path treats it as authority.
**And** AC-3.7.2 evidence proves Backend access follows committed metadata and
explicit restore verification.

**Given** Epic 3 closure is evaluated
**When** any P0 cold-read evidence is missing
**Then** closure is FAIL, not deferred to Epic 6.
**And** AC-3.7.3 records PASS, CONCERNS, or FAIL using the V2 release gate
language.

### Story 3.8: Make Scrub Coordination Concurrency Deterministic

**Requirements:** FR-3, FR-6, FR-8, NFR-1, NFR-7, NFR-8.

As a storage operator,
I want scrub coordination to behave deterministically under duplicate and
overlapping requests,
So that integrity verification and repair workflows cannot hang or lose results.

**Acceptance Criteria:**

**Given** a duplicate `scrubID`
**When** a second consistency check is proposed
**Then** behavior is deterministic and the first waiter cannot hang
indefinitely.
**And** AC-3.8.1 evidence records the duplicate-ID regression test.

**Given** overlapping scrubs with different IDs
**When** results apply
**Then** each result remains retrievable by ID for the defined retention window
or is explicitly rejected with a documented policy.
**And** AC-3.8.2 evidence records cache behavior by scrub ID.

**Given** `applyConsistencyCheck` notifies a waiter
**When** the send occurs
**Then** the coordinator does not hold the mutex across a potentially blocking
send.
**And** AC-3.8.3 evidence records a deterministic lock/send regression test.

**Given** concurrency tests run
**When** cancellation, timeout, cleanup, and race-sensitive paths are exercised
**Then** tests use channels, contexts, or bounded polling, not sleeps.
**And** AC-3.8.4 evidence records the synchronization strategy.

**Given** the scrub coordinator fix is complete
**When** verification runs
**Then** `go test ./internal/shard/...` and `go test -race ./internal/shard/...`
pass.
**And** AC-3.8.5 evidence links both commands.

## Epic 4: Operators Can Run Fail-Closed Security and OpenBao Workflows

Security and platform operators can run production security mode with
fail-closed startup, mTLS, authorization, audit, rate limits, OpenBao-backed
envelope encryption, durable rewrap, and `scrapctl openbao bootstrap` for
local/prod-like workflows.

### Story 4.1: Production Security Startup Gate

**Requirements:** FR-9.

As a platform operator,
I want production `scrapd` startup to fail closed when required security config
is missing or invalid,
So that unsafe Cells never serve production traffic.

**Acceptance Criteria:**

**Given** production mode lacks required TLS, role policy, peer identity policy,
Transit config, or dangerous hook policy
**When** startup runs
**Then** `scrapd` fails closed before serving public, peer, or admin traffic.
**And** AC-4.1.1 evidence proves no listener accepts production traffic after
startup gate failure.

**Given** production mode has valid security config
**When** startup runs
**Then** public, peer, admin, telemetry, and `scrapctl` paths are wired with
explicit security posture.
**And** AC-4.1.2 evidence identifies the security config boundary and command.

**Given** startup errors are emitted
**When** evidence is captured
**Then** messages are actionable and do not leak secrets, cert material, private
paths, tokens, or dependency logs.
**And** AC-4.1.3 evidence records the leak-scan command and result.

**Given** any required production security setting is absent
**When** startup evaluates defaults
**Then** it does not fall back to development mode, plaintext mode, disabled
auth, or local-only overrides.
**And** AC-4.1.4 evidence records one negative case for each required setting.

### Story 4.2: Surface Authorization, Audit, and Rate Limits

**Requirements:** FR-9.

As a security operator,
I want public, peer, admin, and dangerous operations authorized, audited, and
rate-limited,
So that production surfaces fail closed before side effects.

**Acceptance Criteria:**

**Given** a caller lacks the required role
**When** it attempts public, peer, admin, or dangerous operations
**Then** authorization denies before side effects.
**And** AC-4.2.1 evidence proves the denied request does not mutate Raft,
Backend, Local Block Lifecycle, or audit authority state.

**Given** public gRPC, peer RPC, admin HTTP, and `scrapctl`-initiated admin paths
are tested separately
**When** each path lacks required auth context
**Then** each path returns the expected denial before side effects.
**And** AC-4.2.1a evidence records one denial artifact per surface.

**Given** an authorized caller performs a dangerous operation
**When** the operation completes or fails
**Then** bounded audit evidence is emitted.
**And** AC-4.2.2 evidence proves audit fields are low-cardinality and redacted.

**Given** rate limits are exceeded
**When** requests continue
**Then** the surface returns typed denials without leaking sensitive request
metadata.
**And** AC-4.2.3 evidence records public, peer, admin, and dangerous-operation
rate-limit behavior where applicable.

### Story 4.3: OpenBao-Backed Encrypted Write and Read

**Requirements:** FR-10.

As a security engineer,
I want production writes encrypted before Block persistence and reads decrypted
through the normal path,
So that plaintext is never stored as the production default.

**Acceptance Criteria:**

**Given** production encryption is configured
**When** a Document is written
**Then** payload bytes are encrypted before Block persistence and Frame CRC
covers stored ciphertext.
**And** AC-4.3.1 evidence proves plaintext is not written to Block storage.

**Given** an encrypted Document is read
**When** Transit is available
**Then** the normal read path decrypts and verifies plaintext SHA-256 before
return.
**And** AC-4.3.2 evidence identifies the changed boundary and verification
command.

**Given** Transit or key material is unavailable
**When** write/read paths run
**Then** operations fail closed without plaintext fallback.
**And** AC-4.3.3 evidence proves no key material, wrapped-key ciphertext, or
plaintext leaks into logs, metrics, traces, or artifacts.

**Given** production outage drills are required
**When** Transit outage or key-denial behavior is exercised beyond the
write/read crypto path
**Then** operational rehearsal evidence is owned by Story 4.7 rather than hidden
inside this story.
**And** AC-4.3.4 evidence records the split between crypto-path tests and
production rehearsal gates.

### Story 4.4: Durable Envelope Rewrap Workflow

**Requirements:** FR-10.

As a security engineer,
I want envelope metadata rewrapped durably through Raft,
So that key rotation converges without rewriting Block payload bytes.

**Acceptance Criteria:**

**Given** a rewrap request is authorized
**When** rewrap runs
**Then** envelope metadata changes converge through committed Raft state.
**And** AC-4.4.1 evidence proves rewrap state is replicated authority, not local
operator state.

**Given** rewrap is interrupted or retried
**When** the workflow resumes
**Then** it is idempotent and does not leak key material.
**And** AC-4.4.2 evidence records retry behavior and redaction checks.

**Given** rewrap completes
**When** encrypted reads run across Members
**Then** plaintext verification still succeeds without rewriting Block payload
bytes.
**And** AC-4.4.3 evidence proves Block bytes remain unchanged while envelope
metadata converges.

**Given** rewrap is interrupted after old and new envelope metadata both exist
in flight
**When** the workflow resumes or replay runs
**Then** it does not orphan old envelopes or expose ambiguous decrypt behavior.
**And** AC-4.4.4 evidence records the interrupted-rewrap fixture and resumed
state.

### Story 4.5: `scrapctl openbao bootstrap` Fresh Setup

**Requirements:** FR-14.

As a platform operator,
I want `scrapctl openbao bootstrap` to initialize local/prod-like OpenBao
Transit setup,
So that rehearsals do not depend on undocumented scripts.

**Acceptance Criteria:**

**Given** a fresh supported local/prod-like OpenBao target
**When** `scrapctl openbao bootstrap` runs
**Then** it initializes or unseals as configured, mounts Transit, creates or
verifies the S.C.R.A.P. Transit key, and emits redacted evidence.
**And** AC-4.5.1 evidence identifies the CLI command, environment, and artifact
path.

**Given** OpenBao returns sensitive values
**When** output, logs, reports, or tracker-ready evidence are produced
**Then** root tokens, unseal keys, Transit tokens, private keys, client cert
material, wrapped keys, and raw dependency logs are excluded.
**And** AC-4.5.2 evidence records stdout, stderr, report, log, and artifact
redaction checks.

**Given** the command uses OpenBao APIs
**When** dependencies are reviewed
**Then** it uses the official OpenBao Go API client rather than shelling out to
undocumented scripts.
**And** AC-4.5.3 evidence records the dependency and changed-boundary list.

### Story 4.6: `scrapctl openbao bootstrap` Idempotency and Incompatible State

**Requirements:** FR-14.

As a platform operator,
I want OpenBao bootstrap to be idempotent for compatible state and fail closed
for incompatible state,
So that rehearsal setup can be repeated safely.

**Acceptance Criteria:**

**Given** Transit and the S.C.R.A.P. key already exist with compatible settings
**When** bootstrap reruns
**Then** the command succeeds without unsafe mutation.
**And** AC-4.6.1 evidence proves the existing compatible state is preserved.

**Given** existing OpenBao state is incompatible
**When** bootstrap runs
**Then** it fails closed with an actionable, redacted reason.
**And** AC-4.6.2 evidence proves incompatible state is not mutated into an
unsafe configuration.

**Given** bootstrap evidence is reviewed
**When** closure is evaluated
**Then** fresh setup, idempotency, incompatible-state failure, and redaction
evidence are linked.
**And** AC-4.6.3 records PASS, CONCERNS, or FAIL for the bootstrap slice.

### Story 4.7: Production Security Rehearsal Evidence Closure

**Requirements:** FR-9, FR-10, FR-14.

As a release owner,
I want production security, encryption, rewrap, and OpenBao bootstrap evidence
linked,
So that Epic 4 cannot close from local happy-path security tests.

**Acceptance Criteria:**

**Given** Epic 4 evidence is collected
**When** closure is evaluated
**Then** startup fail-closed, authz, audit, rate limits, encrypted write/read,
Transit outage, rewrap, bootstrap, idempotency, incompatible state, and
redaction evidence are linked.
**And** AC-4.7.1 records the artifact paths and owning stories.

**Given** Transit is unavailable, auth is denied, or key policy is wrong during
production rehearsal
**When** security rehearsal runs
**Then** the rehearsal records a fail-closed outcome without plaintext fallback
or secret leakage.
**And** AC-4.7.1a evidence records the outage/drill artifact path.

**Given** `make production-rehearsal-security` runs
**When** artifacts are captured
**Then** results include command, commit/ref, environment, expected result,
actual result, artifact path, and redaction proof.
**And** AC-4.7.2 evidence proves LocalStack or local overrides are clearly
marked when used.

**Given** any P0 security or secret-redaction evidence is missing
**When** closure is evaluated
**Then** closure is FAIL, not deferred to Epic 6.
**And** AC-4.7.3 records PASS, CONCERNS, or FAIL using the V2 release gate
language.

## Epic 5: Security Operators Can Contain Unsafe Content Without Mutating Documents

Security operators can scan sealed Block bytes after ACK, quarantine suspicious
Documents through Raft-owned metadata, deny unsafe reads, preserve
`HeadDocument` and `FindDocuments` reconciliation metadata, and confirm or
release quarantine through admin HTTP plus `scrapctl`.

### Story 5.1: Content Scanner Engine Boundary and Scheduling

**Requirements:** FR-11.

As a security operator,
I want sealed Blocks scanned asynchronously after ACK,
So that unsafe content detection does not block billing writes.

**Acceptance Criteria:**

**Given** a sealed Block becomes eligible for scan
**When** the Shard leader schedules scanner work
**Then** scanning runs after ACK and never participates in write durability or
visibility.
**And** AC-5.1.1 evidence proves scanner work is separate from write ACK and
Deep Scrub concerns.

**Given** the scanner engine is unavailable
**When** writes continue
**Then** writes are not blocked and scanner outage/lag becomes
operator-visible.
**And** AC-5.1.2 evidence records the outage command, operator signal, and
write-path result.

**Given** scan telemetry is emitted
**When** evidence is collected
**Then** labels are bounded and no Document bytes, raw identifiers, signatures,
or rule payloads leak.
**And** AC-5.1.3 evidence records telemetry and redaction checks.

**Given** the scanner crashes, receives a poison Document fixture, or schedules
the same Block twice
**When** scanner work resumes
**Then** writes remain unblocked and scanner state remains bounded,
observable, and retry-safe.
**And** AC-5.1.4 evidence records crash, poison, and duplicate-scheduling
fixtures.

### Story 5.2: Scanner Watermarks and Rescan Priority

**Requirements:** FR-11.

As a security operator,
I want scan progress and signature versions persisted,
So that rescans are deterministic and restart-safe.

**Acceptance Criteria:**

**Given** scan work completes for a sealed Block
**When** progress is persisted
**Then** scanner watermarks record `last_scanned_block_id` and
`last_sig_version_scanned`.
**And** AC-5.2.1 evidence identifies the persisted Projection keys and changed
boundary.

**Given** scanner or Member restart occurs
**When** scanning resumes
**Then** work resumes from persisted progress without treating watermarks as
Document visibility authority.
**And** AC-5.2.2 evidence proves watermarks are progress evidence only.

**Given** signatures update
**When** rescan priority is computed
**Then** previously clean Documents can be rescanned without changing Document
identity.
**And** AC-5.2.3 evidence records the rescan trigger and redaction proof.

**Given** persisted scanner watermark state appears to roll backward or conflict
with signature-version state
**When** scanning resumes
**Then** duplicate scan work is safe and Document visibility is unchanged.
**And** AC-5.2.4 evidence records watermark rollback and duplicate scheduling
behavior.

### Story 5.3: QuarantineDocument Raft Command and Projection State

**Requirements:** FR-11, FR-12.

As a security operator,
I want scanner detections to converge through Raft-owned quarantine metadata,
So that unsafe content state is replicated authority, not local scanner state.

**Acceptance Criteria:**

**Given** scanner detection identifies a suspicious Document
**When** the detection is accepted
**Then** a `QuarantineDocument` Raft command is proposed without Document bytes
or raw scanner payloads.
**And** AC-5.3.1 evidence identifies the proto/Raft boundary and command.

**Given** the command commits
**When** Projection state is updated
**Then** a dedicated Content Quarantine prefix materializes the quarantined
Document identity.
**And** AC-5.3.2 evidence proves Projection state is rebuildable from Raft.

**Given** `internal/avscan` reports a hit
**When** quarantine state changes
**Then** Shard/Raft owns the state transition, not the scanner package.
**And** AC-5.3.3 evidence records the changed-boundary list and authority path.

**Given** committed quarantine metadata is replayed after restart
**When** Projection state rebuilds
**Then** Content Quarantine state reconciles from Raft without consulting
transient scanner memory.
**And** AC-5.3.4 evidence records replay/rebuild behavior.

### Story 5.4: Quarantined Read Denial and Metadata Reconciliation

**Requirements:** FR-12.

As a billing service engineer,
I want unsafe Documents denied on read while metadata stays available,
So that reconciliation can continue without serving quarantined bytes.

**Acceptance Criteria:**

**Given** a Document is in Content Quarantine
**When** `ReadDocument` is called
**Then** the operation returns `FAILED_PRECONDITION` with a bounded quarantine
reason and no bytes.
**And** AC-5.4.1 evidence proves no Document bytes are returned.

**Given** the same Document is queried through `HeadDocument` or `FindDocuments`
**When** metadata is returned
**Then** scan status is visible for reconciliation.
**And** AC-5.4.2 evidence proves metadata responses remain bounded and redacted.

**Given** quarantine state races with read visibility
**When** the read path evaluates state
**Then** it fails closed and never returns unsafe bytes.
**And** AC-5.4.3 evidence records race handling and fail-closed behavior.

**Given** quarantine metadata is replayed while reads are active
**When** metadata and Projection state reconcile
**Then** reads continue to fail closed until committed authority permits bytes.
**And** AC-5.4.4 evidence records replay/read-race behavior.

### Story 5.5: Admin HTTP Quarantine Operations

**Requirements:** FR-12.

As a security operator,
I want admin HTTP operations to list, inspect, confirm, and release quarantined
Documents,
So that quarantine response follows the existing V2 admin surface.

**Acceptance Criteria:**

**Given** quarantined Documents exist
**When** an authorized admin lists or inspects them
**Then** bounded metadata is returned without Document bytes.
**And** AC-5.5.1 evidence records authz, rate-limit, audit, and redaction proof.

**Given** an authorized operator confirms quarantine
**When** the request is accepted
**Then** the lifecycle change converges through Raft authority.
**And** AC-5.5.2 evidence proves confirm does not mutate local scanner state
directly.

**Given** an authorized operator releases quarantine
**When** the request is accepted
**Then** read eligibility changes only after committed metadata converges.
**And** AC-5.5.3 evidence records release, audit, and post-release read behavior.

### Story 5.6: `scrapctl` Quarantine Operator Workflow

**Requirements:** FR-12.

As a security operator,
I want `scrapctl` commands for quarantine response,
So that operators can inspect, confirm, release, and collect evidence without
raw API calls.

**Acceptance Criteria:**

**Given** quarantine state exists
**When** `scrapctl` lists or inspects quarantine
**Then** output uses exact glossary terms and redacts raw identifiers by
default.
**And** AC-5.6.1 evidence includes CLI output and redaction proof.

**Given** an operator confirms or releases quarantine
**When** `scrapctl` invokes admin HTTP
**Then** the command reports the committed outcome or a typed failure.
**And** AC-5.6.2 evidence proves CLI operations route through admin HTTP and
Raft-owned authority.

**Given** evidence output is requested
**When** `scrapctl` renders quarantine evidence
**Then** it includes command, result, artifact path, and redaction proof.
**And** AC-5.6.3 evidence records stdout, stderr, and report leak checks.

### Story 5.7: Content Safety Closure Evidence

**Requirements:** FR-11, FR-12.

As a release owner,
I want scanner, quarantine, admin, and `scrapctl` evidence linked,
So that Epic 5 cannot close from scanner happy-path tests alone.

**Acceptance Criteria:**

**Given** Epic 5 evidence is collected
**When** closure is evaluated
**Then** scheduling, scanner outage, watermarks, rescan, detection-to-Raft, read
denial, metadata scan status, admin confirm/release, `scrapctl`, race handling,
and redaction evidence are linked.
**And** AC-5.7.1 records artifact paths and owning stories.

**Given** scanner signatures, YARA rule text, or dependency logs exist
**When** artifacts are reviewed
**Then** sensitive content and raw payloads are excluded.
**And** AC-5.7.2 evidence records the leak-scan command and result.

**Given** any P0 unsafe-read or quarantine-authority evidence is missing
**When** closure is evaluated
**Then** closure is FAIL, not deferred to Epic 6.
**And** AC-5.7.3 records PASS, CONCERNS, or FAIL using the V2 release gate
language.

## Epic 6: Release Owners Can Prove V2 Readiness

Release owners can reconcile feature evidence into a V2 release decision using
`scrapctl`, OpenTelemetry evidence, runbooks, alert/query references, a release
evidence matrix, closure policy updates, and final real S3/IAM production
rehearsal after feature scope is complete.

### Story 6.1: V2 Release Evidence Matrix

**Requirements:** FR-16.

As a release owner,
I want a release evidence matrix mapping FRs, ADRs, issues, commands,
artifacts, and closure status,
So that V2 readiness can be audited from current linked evidence.

**Acceptance Criteria:**

**Given** all V2 FRs and accepted ADR gates
**When** the matrix is generated
**Then** every FR, ADR 0025-0027 gate, story, issue, command, artifact path,
and closure status is represented.
**And** AC-6.1.1 evidence records the generated matrix path and source inputs.

**Given** matrix columns are defined
**When** the matrix is reviewed
**Then** it includes FR/ADR/story, evidence command, artifact path, environment,
owner, timestamp, commit/ref, pass/fail status, and redaction check columns.
**And** AC-6.1.1a evidence records the matrix schema.

**Given** an evidence row references an artifact
**When** the artifact is reviewed
**Then** it includes command, commit/ref, environment, expected result, actual
result, timestamp, and redaction proof.
**And** AC-6.1.2 evidence proves stale or local-only evidence is marked
explicitly.

**Given** a requirement lacks current evidence
**When** closure is evaluated
**Then** the matrix marks FAIL or CONCERNS with owner and mitigation, never
silent pass.
**And** AC-6.1.3 records the release-gate decision for each gap.

**Given** matrix generation needs data from feature epics
**When** the data is missing
**Then** this story records the gap and does not create substitute feature
evidence.
**And** AC-6.1.4 evidence proves Epic 6 stayed aggregation-only.

### Story 6.2: Operator Runbooks for V2 Failure Domains

**Requirements:** FR-16.

As a platform operator,
I want runbooks for the major V2 failure domains,
So that production incidents can be handled without reconstructing behavior
from source code.

**Acceptance Criteria:**

**Given** V2 runbooks are created
**When** docs are reviewed
**Then** they cover startup/security readiness, mTLS/cert rotation, OpenBao
Transit dependency, Backend upload pressure, restore failures, eviction
campaigns, Block Quarantine repair, Content Quarantine response, multi-Shard
routing health, and evidence collection.
**And** AC-6.2.1 evidence links each runbook to the owning feature epic or
release gate.

**Given** each runbook is structured
**When** it is reviewed
**Then** it includes normal path, failure path, rollback or escalation,
expected outputs, and evidence collection sections.
**And** AC-6.2.1a evidence records the runbook section checklist.

**Given** a runbook references commands
**When** examples are reviewed
**Then** commands match implemented `scrapctl`, admin, or make targets and avoid
raw secrets.
**And** AC-6.2.2 evidence records command validation and redaction checks.

**Given** a runbook contains incident steps
**When** reviewed for safety
**Then** it does not instruct operators to use Backend inventory, local files,
or telemetry as storage authority.
**And** AC-6.2.3 evidence records the authority-boundary review result.

**Given** cold reads, Content Quarantine, OpenBao fail-closed behavior, and
Backend restore are documented
**When** runbook workflows are reviewed
**Then** each workflow is independently runnable from documented operator
commands and expected outputs.
**And** AC-6.2.4 evidence records workflow validation results.

### Story 6.3: Alert and Query References

**Requirements:** FR-15, FR-16.

As a platform operator,
I want alert/query references tied to release risks,
So that S.C.R.A.P. health can be monitored through low-cardinality, redacted
signals.

**Acceptance Criteria:**

**Given** alert/query references are created
**When** docs are reviewed
**Then** they cover public/peer/admin availability, write ACK latency, read
failures, restore failures, Backend upload lag, upload pressure,
scrub/quarantine, scanner lag/outage, Transit outage, audit sink failure,
rate-limit denials, Shard leader/peer health, and evidence leak-scan status.
**And** AC-6.3.1 evidence links each reference to an operational question.

**Given** an alert/query reference is added
**When** it is reviewed
**Then** it states what happened, how to confirm it, and what the operator does
next.
**And** AC-6.3.1a evidence records that triad for each high-risk reference.

**Given** a query uses telemetry attributes
**When** reviewed
**Then** attributes are bounded and do not include raw Document identifiers,
Backend keys, tokens, trace IDs, or request IDs.
**And** AC-6.3.2 evidence records telemetry privacy validation.

**Given** alerts reference runbooks
**When** reviewed
**Then** each high-risk alert links to the relevant operator response path.
**And** AC-6.3.3 evidence records missing links as FAIL or CONCERNS.

**Given** alert/query references require feature telemetry not yet implemented
**When** the reference is reviewed
**Then** the gap is marked FAIL or CONCERNS rather than filled by invented
metrics.
**And** AC-6.3.4 evidence proves Epic 6 stayed aggregation-only.

### Story 6.4: `scrapctl` Release Evidence Bundle

**Requirements:** FR-13, FR-15, FR-16.

As a release owner,
I want `scrapctl` to collect release-relevant diagnostics and evidence,
So that V2 closure can be reviewed from reproducible operator output.

**Acceptance Criteria:**

**Given** a prod-like Cell is running
**When** `scrapctl` evidence collection runs
**Then** it gathers status, leaders, peers, upload pressure, faults,
eviction/restore status, Shard health, scanner/quarantine status, OpenBao
readiness, and redaction proof where implemented by feature epics.
**And** AC-6.4.1 evidence records command, environment, artifact path, and
changed-boundary list.

**Given** the evidence bundle is written
**When** its manifest is inspected
**Then** it records artifact naming, checksums, provenance, command lines,
environment, commit/ref, and redaction/privacy status.
**And** AC-6.4.1a evidence records the manifest schema and example.

**Given** evidence output is written
**When** artifacts are inspected
**Then** raw identifiers, Backend keys, tokens, private keys, Document bytes,
data keys, and wrapped-key ciphertext are absent.
**And** AC-6.4.2 evidence records stdout, stderr, report, and artifact
leak-scan results.

**Given** a feature-specific evidence command is missing
**When** the bundle runs
**Then** the result marks the missing evidence as FAIL or CONCERNS instead of
omitting it.
**And** AC-6.4.3 evidence proves missing evidence cannot silently pass release
closure.

**Given** the bundle needs a feature-specific command
**When** that command is not implemented by the owning feature epic
**Then** the bundle records the missing command and does not add replacement
product behavior.
**And** AC-6.4.4 evidence proves Epic 6 stayed aggregation-only.

### Story 6.5: Tier 2 and Tier 3 Release Evidence Gates

**Requirements:** FR-15, FR-16.

As a release owner,
I want Tier 2 prod-like and Tier 3 telemetry/evidence gates linked into closure,
So that deployed behavior and telemetry privacy are proven before V2 release.

**Acceptance Criteria:**

**Given** Tier 2 prod-like Cilium evidence is collected
**When** artifacts are reviewed
**Then** deployed gateway behavior, security posture, and relevant feature
evidence are linked.
**And** AC-6.5.1 evidence records command, commit/ref, environment, expected
result, actual result, and artifact path.

**Given** Tier 3 evidence bundle is collected
**When** artifacts are reviewed
**Then** logs, metrics, traces, profiles, and leak-scan results are present and
redacted.
**And** AC-6.5.2 evidence proves telemetry privacy constraints are enforced.

**Given** either gate is stale, missing, or inconsistent with current commit/ref
**When** closure is evaluated
**Then** V2 readiness is FAIL or CONCERNS.
**And** AC-6.5.3 records the owner and mitigation for each gap.

**Given** Tier 2 or Tier 3 evidence is a screenshot, stale artifact, local-only
run, or unlinked output
**When** release closure reviews the gate
**Then** the gate fails or records CONCERNS using hard criteria, not advisory
language.
**And** AC-6.5.4 evidence records pass/fail criteria and artifact retention
rules.

### Story 6.6: Real S3/IAM Production Rehearsal Closure

**Requirements:** FR-16.

As a release owner,
I want real non-local S3/IAM rehearsal evidence captured after feature scope is
complete,
So that Backend production claims do not rely only on LocalStack or local
filesystem evidence.

**Acceptance Criteria:**

**Given** all required feature epics are complete
**When** real S3/IAM `make production-rehearsal` runs
**Then** evidence uses non-local S3/IAM credentials and environment.
**And** AC-6.6.1 evidence records command, commit/ref, environment, expected
result, actual result, artifact path, and redaction proof.

**Given** LocalStack or localhost endpoints appear in evidence
**When** final closure is evaluated
**Then** they are marked interim only and cannot close the real S3/IAM gate.
**And** AC-6.6.2 evidence proves final Backend claims are not closed by local
emulation.

**Given** issue #429 is linked
**When** closure is reviewed
**Then** the evidence artifact, command, environment, redaction proof, and
result are traceable from the matrix.
**And** AC-6.6.3 records the issue linkage and final gate status.

**Given** real S3/IAM evidence is vague, screenshot-only, localhost-only,
LocalStack-only, or missing IAM provenance
**When** the final Backend claim is reviewed
**Then** the S3/IAM gate is FAIL or CONCERNS.
**And** AC-6.6.4 evidence records hard pass/fail criteria for issue #429.

### Story 6.7: V2 Closure Policy and Final Gate Decision

**Requirements:** FR-16.

As a release owner,
I want closure policy to enforce V2's no-intermediate-release rule,
So that V2 is not called release-ready until every required feature and
evidence gate is complete.

**Acceptance Criteria:**

**Given** V2 closure policy is updated
**When** final readiness is evaluated
**Then** closed issues, merged PRs, or closed phase milestones are not enough
without current linked evidence.
**And** AC-6.7.1 evidence records the policy diff and review result.

**Given** a proposed waiver would bypass required P0 evidence, security
evidence, real S3/IAM evidence, or redaction proof
**When** closure policy is applied
**Then** the waiver is rejected as non-waivable for V2 release readiness.
**And** AC-6.7.1a evidence records the non-waivable blocker list.

**Given** any required FR, ADR gate, story, runbook, alert/query reference, Tier
gate, security rehearsal, or real S3/IAM evidence is missing
**When** closure is evaluated
**Then** the final decision is FAIL or CONCERNS, not PASS.
**And** AC-6.7.2 evidence records owner, mitigation, and next action for each
gap.

**Given** final release review completes
**When** all evidence is current and redacted
**Then** the matrix records PASS with linked artifacts and remaining non-goals
explicitly out of scope.
**And** AC-6.7.3 records the final release gate decision and non-goal review.

**Given** Epic-level evidence is rolled into the final matrix
**When** the final gate is reviewed
**Then** every PASS traces back to a feature epic, artifact, command, owner,
timestamp, and commit/ref.
**And** AC-6.7.4 evidence records the rollup from Epic 1 through Epic 6.

### Story 6.8: Reconcile Release Evidence and Fail Closed on Contradictions

**Requirements:** FR-16, NFR-5, NFR-8.

As a release owner,
I want release artifacts and gate scripts to fail closed when evidence
contradicts itself,
So that SCRAP cannot report final PASS while required data-integrity or tier gate
evidence still reports FAIL.

**Acceptance Criteria:**

**Given** `closure-policy-final-gate-decision.md` says final PASS while
`release-tier-gates-evidence.md` or `release-evidence-matrix.md` says FAIL
**When** gate validation runs
**Then** final closure fails.
**And** AC-6.8.1 evidence records a fixture test for the contradiction.

**Given** Tier 2 or Tier 3 evidence has current PASS runs
**When** Story 6.5 artifacts are updated
**Then** all release artifacts cite the same commit/ref, run links, artifact
names, retention, and redaction proof.
**And** AC-6.8.2 evidence records the reconciled release artifact paths.

**Given** a release artifact references stale or local-only evidence
**When** final closure is evaluated
**Then** the result is FAIL or CONCERNS, never PASS.
**And** AC-6.8.3 evidence records weak-proof rejection coverage.

**Given** gate script fixture tests run
**When** `check-e2e-gates.sh` and the release tier/closure validator path are
exercised
**Then** contradictory PASS/FAIL state is rejected.
**And** AC-6.8.4 evidence records `go test ./scripts/...` and `make gates-check`.

### Story 6.9: Add Vertical Data-Integrity Evidence Across Shard, Raft, Backend, Encryption, and Scrub

**Requirements:** FR-3, FR-4, FR-6, FR-8, FR-10, FR-16, NFR-7, NFR-8.

As a release owner,
I want one vertical data-integrity test path covering Shard authority, Raft
metadata, Backend storage, encryption, and scrub verification,
So that final release closure is not based only on isolated adapter tests.

**Acceptance Criteria:**

**Given** an encrypted Document is written through a Shard-backed path
**When** metadata commits and Backend upload or restore behavior is exercised
**Then** reads return verified bytes or a typed failure.
**And** AC-6.9.1 evidence records the integration command and covered boundaries.

**Given** corruption is introduced in the Block, Backend object, or committed
metadata fixture
**When** read, scrub, or restore runs
**Then** S.C.R.A.P. fails closed without returning partial or unverified bytes.
**And** AC-6.9.2 evidence records the corruption fixtures and typed outcomes.

**Given** scrub or repair evidence is collected
**When** artifacts are reviewed
**Then** raw Document identifiers, Backend keys, key material, and Document bytes
are absent.
**And** AC-6.9.3 evidence records the redaction proof.

**Given** final release evidence is reviewed
**When** this vertical test has local-only, Tier 2, or Tier 3 limits
**Then** the artifact states which boundaries are covered locally, which require
Tier 2/Tier 3, and which are explicitly deferred with owner and mitigation.
**And** AC-6.9.4 evidence records `go test ./test/integration/...`,
`make tier2-e2e-up`, and `make tier3-evidence-up STRESS_SCENARIO=throughput`
where applicable.
