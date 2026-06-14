# External storage systems research for Phase 4

Date: 2026-05-31

## Purpose

This note compares five external repositories against S.C.R.A.P. and records
which ideas are worth carrying into Phase 4 planning. It is research input, not
an accepted architecture decision. Hard decisions still belong in ADRs.

Compared repositories:

- <https://github.com/MattJackson/basement>
- <https://github.com/amaan-mohib/storage-gateway>
- <https://github.com/cloud37/s3-encryption-gateway>
- <https://github.com/xiidea/storage-gateway>
- <https://github.com/NasitSony/VeriStore>

S.C.R.A.P. remains a purpose-built gRPC Document gateway. It is not an
S3-compatible API and should not copy an S3 protocol surface just because several
reference systems use one.

## Summary

S.C.R.A.P. is already deeper than these systems on the storage-authority axis:
immutable Documents, Block and Frame layout, all-or-error reads, checksum-first
integrity, Raft metadata authority, Upload Outbox, scrub and repair, and
production-like evidence gates are already documented locally.

The useful research signal is mostly operational:

- security boundaries and fail-closed behavior;
- backend retry, admission, and observability policy;
- deployment/configuration invariants;
- operator/admin surfaces for dangerous actions;
- explicit failure matrices before local eviction removes hot copies.

## Ranked Findings

### 1. cloud37/s3-encryption-gateway

Best reference for production gateway hardening.

Useful ideas:

- Envelope encryption and KeyManager/KMS separation, including key-version and
  migration thinking.
- Fail-closed multipart state: no silent plaintext fallback when the configured
  security dependency is missing.
- Backend retry policy that distinguishes idempotent and non-idempotent
  operations and exposes retry exhaustion.
- Dedicated observability: metrics, traces, audit events, pprof guarded behind
  admin controls, dashboards, alerts, and runbooks.
- Helm schema invariants that catch invalid production configurations before
  deploy.

Limits:

- Its core product goal is transparent S3 compatibility. That is explicitly not
  S.C.R.A.P.'s API direction.
- Its external state-store choices are appropriate for multipart proxy state,
  not for S.C.R.A.P. Shard authority, which belongs in Raft.

### 2. MattJackson/basement

Best reference for operator/control-plane shape.

Useful ideas:

- Capability-gated backend features instead of driver-name conditionals.
- Audit-first admin actions and service-account/M2M separation.
- Federation, backup, restore, and failover modeled as operator workflows.
- First-run and deployment docs that turn operational assumptions into a visible
  product surface.
- A layered test strategy: unit tests, doc consistency checks, live smoke tests,
  and post-deploy gates.

Limits:

- basement is primarily an admin/control-plane plus gateway product, not a
  quorum-ACK storage authority.
- Its eventual replication/federation model should not replace S.C.R.A.P.'s
  Raft authority for acknowledged Documents.

### 3. NasitSony/VeriStore

Best correctness comparator.

Useful ideas:

- Explicit durability boundaries around WAL fsync and group commit.
- Recovery that stops at the last valid CRC-checked record.
- Metadata-last object visibility as the commit point.
- Small demos for crash, replay, leader election, follower catch-up, and prefix
  listing.

Limits:

- It is educational and smaller in scope.
- S.C.R.A.P. already has richer contracts around bytes outside Raft, Block
  repair, projection rebuilds, and evidence gates.

### 4. xiidea/storage-gateway

Useful future reference for tenant/store/provider registry work.

Useful ideas:

- Tenant, Store, and Bucket Mapping as explicit control-plane concepts.
- Gateway credentials verified before upstream access.
- Backend credentials encrypted at rest and rotated with cache invalidation.
- Proxy versus redirect mode as an explicit read-path choice.
- Redis-backed registry caching with targeted invalidation.

Limits:

- It is S3-compatible and backed by Postgres/Redis control-plane state. That is
  not the S.C.R.A.P. Shard authority model.
- Redirect reads are not compatible with S.C.R.A.P.'s all-or-error verification
  contract unless a future API explicitly defines such a mode.

### 5. amaan-mohib/storage-gateway

Lowest direct relevance.

Useful ideas:

- Separating request admission from background processing can inform Content
  Scanner scheduling.
- Queue task names make async workflows visible.

Limits:

- Worker paths buffer whole objects with `io.ReadAll`, which conflicts with
  S.C.R.A.P.'s no-whole-Document-buffering design.
- The repo has limited tests and little durable design documentation.

## Non-Goals For S.C.R.A.P.

- Do not become S3-compatible.
- Do not introduce Postgres, Redis, or Valkey as Shard authority.
- Do not route storage correctness through a UI/admin control plane.
- Do not let background processors buffer full Documents in memory.
- Do not weaken all-or-error reads for redirect or range convenience.
- Do not make Backend upload part of the write ACK path.

## Improvement Candidates

### 1. Production security boundary ADR

Define client, admin, and peer auth separately. Cover mTLS or workload identity
for peer RPCs, admin authorization, rate limits, audit events, and explicit
non-production escape hatches.

Why: Phase 4 makes local copies disappear on some Members. Operator and peer
surfaces become more sensitive because repair, restore, and eviction controls
can now change read availability.

### 2. OpenBao envelope encryption contract

Before implementation, define envelope format, key-version behavior, Transit
outage behavior, rewrap semantics, audit evidence, and fail-closed reads when key
material is unavailable.

Why: Cold reads and long retention make encryption metadata a long-lived
compatibility contract.

### 3. Backend retry and admission policy

Promote backend retry behavior from adapter detail into documented policy:
retry classes, idempotency rules, per-operation budgets, upload/restore
interaction, pressure metrics, and give-up audit events.

Why: Phase 4 turns the Backend into a read-availability dependency for evicted
local copies.

### 4. Deployment/config invariant validation

Add machine-checkable validation for prod-like and evidence Cells: peer/admin
port exposure, NetworkPolicy, Cilium assumptions, metrics labels, storage limits,
and unsafe test hooks.

Why: The Phase 4 safety story depends on correct peer, admin, Backend, and
telemetry wiring.

### 5. Operator admin surface model

Design Admin Service commands for Block Quarantine, Content Quarantine, upload
pressure, repair, eviction, restore, evidence, health, and dangerous fault
commands.

Why: Phase 4 needs operator-visible controls for why a Block is evicted,
restoring, quarantined, or unavailable.

### 6. Crash/failure matrix for the write-state machine

Turn the seven write states into a failure-injection matrix: crash before/after
local fsync, openlog fsync, peer receipt, Raft commit, projection commit, ACK,
seal, upload confirm, eviction mark, and local unlink.

Why: Partial eviction adds new crash windows after upload confirmation.

### 7. Retention and legal-hold model

Define operator-facing retention and hold semantics even while the public API has
no delete.

Why: Billing Documents have long retention and audit implications. Eviction must
not be confused with deletion.

### 8. Content Scanner work queue design

Make scanner work bounded, durable, resumable, and outside the ACK path. Define
watermarks, I/O budget sharing with Deep Scrub, and audit behavior.

Why: Async processing patterns are useful only if they preserve the storage
contract and avoid unbounded memory.

### 9. Tenant routing and quota registry

Keep `tenant_id` non-authoritative for now, but prepare an ADR when it becomes
routing or quota input.

Why: Phase 4 can proceed without this. Pulling it forward would add authority
complexity before the storage lifecycle is stable.

## Recommended Phase 4 Entry Criteria

Phase 4 implementation should begin only after:

- the Phase 3.6 evidence gates remain current and reviewable;
- the Phase 4 partial-eviction ADR is accepted;
- the restore path from Backend is tested before any local unlink path ships;
- operator visibility exists for eviction eligibility, restore attempts, and
  read unavailability;
- Backend retry/admission behavior is explicit enough that an evicted Block does
  not turn transient Backend trouble into silent corruption or ambiguous errors.
