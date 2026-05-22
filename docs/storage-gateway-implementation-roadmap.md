# Storage Gateway Implementation Roadmap

Status: planning gate tracked

Last updated: 2026-05-22

This roadmap turns the accepted storage-gateway architecture into
implementation slices. It is the control document before more production code
is added.

The tracked v1 PRD is GitHub issue `#2`. Its child Feature issues are:

- `#8` Production architecture and engineering standards;
- `#9` Storage format and metadata boundary;
- `#10` Single-member local durability;
- `#11` Raft metadata shard;
- `#12` Peer byte replication and placement;
- `#13` Backend upload, restore, and envelope workflow;
- `#14` Control plane and operator workflows;
- `#15` Production readiness gate.

Human-owned deployment and compliance inputs for the first production profile
are captured in
[Production Capacity and Compliance Signoff Inputs](production-capacity-compliance-signoff.md).
That record tracks GitHub issue `#48` and must be completed before the
production readiness gate can be signed off.

The next technical gate before storage-format implementation is:

1. `#16` Document package architecture and dependency rules, captured in
   [Storage Gateway Package Architecture](storage-gateway-package-architecture.md);
2. `#17` Document durability-sensitive coding guidelines;
3. `#18` Audit library and substrate ADR coverage.

## Purpose

Build the S.C.R.A.P. storage gateway so billing ETL services can write and read
immutable documents through a strongly consistent service API while the gateway
hides local hot storage, peer replicas, backend object storage, and restore
state.

The spike evidence is captured in
[Write Path Implementation Spike](spikes/0001-write-path-spike.md). The spike
validated ordering and safety assumptions, not production code structure.

## Success Criteria

- A finalized write is acknowledged only after required local and peer
  durability plus quorum-applied authoritative metadata.
- `HeadDocument` and `ReadDocument` provide strong read-after-write behavior
  for acknowledged writes inside an authoritative cell.
- Reads never return corrupt bytes. If all verified sources fail, the API fails
  closed with a typed corruption or unavailable response.
- Backend upload, restore, scrub, repair, lifecycle, and DR jobs are durable,
  idempotent, observable workflows.
- Internal metadata, published metadata, block indexes, and envelope records are
  versioned compatibility contracts with explicit mixed-version behavior.
- Production builds pass generated-code, race, crash/recovery, security,
  capacity, and operator-readiness gates before accepting production traffic.

## Non-Goals

- Do not expose S3 compatibility in v1.
- Do not promise formal business RTO/RPO values in v1; report measured DR drill
  evidence instead.
- Do not implement always-on secondary backend replication in v1.
- Do not build a separate stateless ingress tier before the stateful member
  model proves insufficient.
- Do not promote spike package structure, JSONL Raft durability, fake peer
  prepare, or synthetic benchmarks into production by default.
- Do not weaken the all-or-error read contract to stream clean prefixes from
  partially corrupt documents without a new product/API decision.

## Pre-Production Code Disposition

Decision on 2026-05-22: no pre-existing untracked production implementation
code was present in the working tree. There is therefore nothing to stash or
delete.

Committed pre-production scaffolding remains in the repository as committed
history. Its existence does not change this roadmap: no further production
implementation should be added until the planning and ADR gates below are
complete.

## Milestones

### 0. Planning Gate

Goal: freeze the minimum set of hard-to-reverse decisions before the next
production slice.

Deliverables:

- this roadmap;
- ADRs for internal metadata boundaries, read corruption behavior, and
  production readiness gates;
- explicit mapping from spike conclusions to implementation gates;
- issue or task slices small enough to review independently.

Acceptance criteria:

- design notes no longer direct the next step to implementation by default;
- spike status is completed evidence with reusable conclusions and gaps;
- all new ADRs are accepted or explicitly deferred;
- no untracked production code remains undecided;
- GitHub issue `#2` and child issues `#8` through `#48` track the v1 PRD,
  feature slices, production architecture gates, implementation tasks, and
  production-readiness evidence.

Risk gate:

- Do not continue production storage implementation until this milestone is
  complete.

### 1. Storage Format And Metadata Boundary

Goal: define the long-lived internal records before writing durable production
bytes.

Deliverables:

- private versioned protobuf messages for authoritative shard metadata;
- private versioned protobuf messages for published metadata snapshots/tails;
- block, index, frame-checksum, and envelope record drafts;
- compatibility rules for old readers, old writers, old stored bytes, and
  rolling upgrades, including the example in
  [Storage Gateway Schema Evolution Example](storage-gateway-schema-evolution.md);
- generated-code checks in CI for all private schemas.

Acceptance criteria:

- authoritative metadata can represent document identity, logical checksum,
  physical refs, frame checksums, encryption envelope refs, restore state, and
  background work;
- published metadata excludes non-public internals and can be imported by a
  read-only cell without joining source consensus;
- tests reject unknown required versions and preserve unknown forward-compatible
  fields where protobuf semantics allow it;
- one documented migration example proves how a new metadata field rolls out.

Risk gate:

- Do not write production block/index files until the compatibility boundary is
  reviewed.

### 2. Single-Member Local Durability Slice

Goal: implement the smallest production-shaped local write/read loop without
claiming production ACK semantics.

Deliverables:

- production package boundaries for API, application workflow, metadata,
  blockstore, and local projection;
- local block append and sync;
- local prepare/openlog record and recovery;
- local metadata projection rebuild from authoritative records;
- checksum-verified `HeadDocument` and `ReadDocument` for local data.

Acceptance criteria:

- crash tests cover every boundary between write start, block sync, prepare
  sync, metadata apply, and ACK;
- uncommitted bytes stay invisible after restart;
- acknowledged local-only dev writes survive restart in non-production mode;
- full and ranged reads verify all touched frames before streaming bytes.

Risk gate:

- Local-only mode must be visibly non-production. It cannot satisfy the public
  production ACK contract.

### 3. Raft Metadata Shard

Goal: make consensus metadata the authority for document visibility and
background work.

Deliverables:

- `go.etcd.io/raft/v3` shard runtime with durable WAL, snapshots, restart, and
  compaction;
- deterministic command encoding for document commit, lifecycle, upload intent,
  restore state, repair state, and tombstone;
- ReadIndex freshness barrier for safe reads;
- leader routing, stale-leader fencing, and timeout behavior.

Acceptance criteria:

- committed records replay into a clean local projection after projection loss;
- stale leaders cannot acknowledge writes or serve fresh reads;
- ReadIndex fails closed without quorum;
- duplicate proposals and unknown client outcomes are idempotent by document
  identity and request id;
- mixed-version command handling follows the metadata compatibility plan.

Risk gate:

- Do not enable production writes until quorum metadata restart and stale-leader
  tests pass under fault injection.

### 4. Peer Byte Replication And Placement

Goal: make byte durability match the ACK contract instead of relying on a fake
prepare hook.

Deliverables:

- replica placement over distinct Kubernetes storage nodes;
- peer prepare protocol for block ranges and checksum evidence;
- peer catch-up, repair, and quarantine workflow;
- member drain and replacement workflow.

Acceptance criteria:

- a write ACK waits for required peer byte durability before metadata
  visibility;
- a lost local disk can be repaired from verified peer bytes or backend bytes;
- byte-serving readiness requires caught-up metadata and verified local bytes;
- placement rejects unsafe replica sets and reports why.

Risk gate:

- Do not allow production ACKs if placement cannot satisfy the configured
  durability profile.

### 5. Backend Upload, Restore, And Envelope Workflow

Goal: turn backend object storage into durable cold storage without putting it
inside the client ACK path.

Deliverables:

- backend adapter interface and explicit capacity profiles;
- block, index, and envelope upload outbox;
- OpenBao Transit envelope integration;
- restore-on-read and explicit prewarm workflows;
- backend verification and DR rebuild tooling.

Acceptance criteria:

- backend upload jobs are idempotent and retry-safe;
- encrypted backend objects can be restored only with required envelope and
  OpenBao key material;
- restore-pending and crypto-unavailable responses are typed and observable;
- capacity profiles fail closed when runway or backend budgets are unsafe;
- a clean cluster can rebuild metadata and verify bytes from primary backend
  artifacts in a drill.

Risk gate:

- Do not advertise backend durability until upload, verify, restore, and key
  retention drills pass.

### 6. Control Plane And Operator Workflows

Goal: make dangerous operational actions typed, durable, audited, and
idempotent.

Deliverables:

- admin API workflows for drain, repair, scrub, restore, tombstone, key
  rotation, backend verification, capacity override, and DR drill;
- shared durable operation model;
- CLI as an API client;
- authorization policy loading and audit events.

Acceptance criteria:

- every dangerous operation has a dry-run or plan phase where useful;
- retries do not duplicate destructive side effects;
- denied and successful critical actions are audited;
- bad hot-reload policy is rejected while the last valid policy remains active;
- operation status survives process restart.

Risk gate:

- Do not run production member replacement, repair override, tombstone, or
  capacity override through ad hoc scripts.

### 7. Production Readiness Gate

Goal: prove the service can be operated safely before production traffic.

Deliverables:

- crash/recovery matrix across local bytes, metadata WAL, projections, backend
  upload, restore, and OpenBao failures;
- race, fuzz, generated-code, and compatibility checks in CI;
- representative soak and capacity tests using expected document sizes and edge
  cases up to 128 MiB;
- security review of API authorization, encryption boundaries, and admin
  operations;
- runbooks for corruption, restore, drain, lost disk, lost member, OpenBao
  outage, backend outage, and DR rebuild.

Acceptance criteria:

- no known invariant failure remains in acknowledged-write, visible-read,
  corruption, replay, or restore paths;
- all release-blocking checks are automated or have a named manual evidence
  artifact;
- dashboards expose write admission, disk runway, backend lag, repair lag,
  restore lag, corruption incidents, Raft health, and OpenBao health;
- operators can execute at least one full DR rebuild drill from documented
  steps.

Risk gate:

- Do not accept production traffic until this milestone is signed off by the
  product and operations owners for the target deployment profile.

## Sequencing

1. Complete milestone 0 before more production implementation.
2. Build storage format and metadata boundaries before durable production
   writes.
3. Implement single-member local durability only as a testable substrate.
4. Add Raft authority before peer durability is allowed to publish visibility.
5. Add peer byte replication before any production ACK mode.
6. Add backend upload and restore before claiming cold durability or DR.
7. Add control-plane workflows before operators need the corresponding action.
8. Run production readiness gates before production traffic.

## Risk Register

- Metadata schema drift could make old data unreadable. Mitigation: versioned
  private protobuf messages, compatibility tests, and migration examples.
- Read corruption policy could leak partial bytes if implemented casually.
  Mitigation: verify all touched frames before streaming and fail closed.
- Raft authority could be undermined by stale leaders or local projection
  shortcuts. Mitigation: ReadIndex gates, fencing tests, and projection rebuild
  tests.
- Backend lag could exhaust local disks. Mitigation: explicit capacity profiles,
  guard bands, admission control, and backend circuit breakers.
- OpenBao key loss could make encrypted data unrecoverable. Mitigation:
  documented key retention, snapshots, audit, and restore drills.
- Spike code could bias production architecture. Mitigation: treat the spike as
  evidence only and re-implement production slices behind reviewed boundaries.
