# Define write pipeline group-commit

Status: accepted

## Context

S.C.R.A.P. acknowledges writes before backend upload. ADR 0012 makes that ACK a
durability promise: acknowledged bytes must remain readable or repairable after
the write method returns.

The current local write path pays that durability cost once per operation:

- `internal/blockstore.Store.AppendValidated` serializes every append through
  the store mutex and calls `blockFile.Sync()` before returning a record.
- `internal/raftmeta.Authority` serializes metadata commands through the
  pending proposal lock and `Log.Append` calls `file.Sync()` for every command
  before applying it to the metastore projection.

That shape is simple and correct, but under concurrent write load the throughput
ceiling is dominated by per-write fsync latency. Issue #148 requires a
write-pipeline group-commit design that improves throughput without weakening
the durable ACK contract.

The original issue text referred to ADR-009 as the prerequisite. ADR 0009 is the
disaster-recovery scope decision, so this ADR records the group-commit decision.

## Decision

Implement group-commit as bounded durability batching, not as asynchronous ACKs.
A caller may return successfully only after the bytes or metadata command it
depends on are included in a completed durable sync.

The first implementation should keep ordered single-writer ownership for each
durable file and batch the sync waiters behind that writer. Do not start by
switching block appends to parallel `WriteAt` or mmap-style IO. Parallel
reservation and disjoint writes may be reconsidered only after the evidence
harness proves that serialized file writes, not fsync, are the remaining
bottleneck.

Use this initial batch policy for both block append syncs and metadata command
log syncs:

- flush at most 2 ms after the first ready waiter enters an empty batch;
- flush immediately when a batch reaches 128 operations;
- flush immediately when known encoded payload bytes reach 8 MiB, while allowing
  a single operation that is valid under its existing per-operation size limit to
  form a one-item batch;
- flush explicitly on `Close`, block seal, repair/range installation, snapshot,
  compaction, and any shutdown path that can otherwise strand waiters;
- keep queue depth bounded by an implementation constant until benchmark
  evidence justifies exposing a deployment setting.

These limits are starting points, not product SLOs. Issue #177 must capture
before/after evidence and should be used to tune the constants before #148 is
closed.

## Block Append Model

`blockstore.Store` should gain an append worker that owns the current block
file, current block offset, and durable sync boundary.

Append requests enter a bounded queue. The worker writes records in admission
order, computes the same hashes and frame records as today, and advances the
in-memory next offset only after the record bytes have been written to the file.
The worker then groups completed records into a sync batch. After `Sync`
returns successfully, every waiter in that batch can receive its `Record`.

The implementation must preserve these invariants:

- offsets are assigned by one owner and never overlap;
- no `Record` is returned before the corresponding stored bytes are durable;
- `SealCurrent`, `SealBlock`, `Close`, repair, and range installation flush or
  fail pending append batches before changing the file lifecycle;
- a write, checksum, validation, or sync error fails the affected waiters and
  moves the store into a fail-closed state;
- after fail-closed, subsequent appends return a stable error until the store is
  reopened or repaired.

Context cancellation is an admission and waiting concern, not permission to
leave ambiguous partial state. If a context is canceled before the request is
accepted, the request fails without writing. Once the worker starts writing a
request, it must complete the request to a deterministic terminal state:
durable success, pre-sync rollback to the last safe offset, or fail-closed after
an ambiguous sync/write failure.

Crash recovery may encounter bytes that were synced but never referenced by
metadata because the process stopped before the metadata commit. Those bytes are
not visible documents. Reads still require metadata records and checksum
verification.

## Metadata Command Model

`raftmeta.Authority` should gain an ordered commit loop for mutating commands.
Public methods continue to build command-specific requests and enforce
freshness before admission, but durable mutation work moves through the commit
loop.

The loop drains a bounded batch, validates each request in order against the
current metastore state, writes accepted commands to the command log, performs a
single durable sync for the accepted batch, and applies the synced entries to
the metastore in log-index order. Requests that fail validation before log
append fail individually and are not included in the durable batch.

`Log` should provide an append-batch primitive that writes encoded frames in
index order and syncs once. `Log.Append` may remain as a one-command wrapper for
tests and compatibility, but production metadata mutation should use the batch
path after this ADR is implemented.

The implementation must preserve these invariants:

- command IDs remain deterministic idempotency keys;
- conflict checks observe commands accepted earlier in the same batch;
- metastore visibility never occurs before the command log entries are durable;
- replay after crash applies durable commands in the same index order;
- append, sync, or apply errors fail affected waiters and move the authority
  into a fail-closed state;
- read freshness and write quorum checks are not weakened by batching.

If a process crashes after command-log sync but before applying all commands,
replay must apply the durable entries. If it crashes after apply but before the
caller receives the result, the deterministic command ID and existing conflict
logic must make retry idempotent.

## Observability

The implementation must expose bounded signals before #148 can close:

- block append queue depth;
- block sync batch size and sync latency;
- metadata command queue depth, preserving the existing `scrap_raft_queue_depth`
  signal or replacing it with equivalent semantics;
- metadata command batch size and command-log sync latency;
- batch failures grouped by component and failure stage;
- dropped, retried, or canceled metadata proposals where relevant.

The #177 evidence harness must report commit SHA, dirty-tree state, workload
shape, throughput, ACK latency p50/p95/p99, and the queue or backlog signals
available in the local run.

## Implementation Map

Close #148 through these slices:

- #177 adds the focused before/after write-pipeline evidence harness.
- #178 implements blockstore durable append batching.
- #179 implements raftmeta command-log batching.

#178 and #179 should stay separate. Each changes a different durability
boundary and needs focused regression tests before combined throughput evidence
is meaningful.

## Consequences

- The ACK contract remains synchronous at the caller boundary, but several
  concurrent callers can share one durable sync.
- The first implementation improves the fsync-bound bottleneck without also
  changing file-write parallelism, memory ownership, or on-disk formats.
- Fail-closed behavior may reject more writes after ambiguous IO errors, which
  is preferable to continuing with unknown durability state.
- The throughput target in #148 is evidence-dependent. The design makes it
  possible to reach the target, but #148 should close only after measured
  before/after evidence demonstrates the improvement.

## Rejected Options

- **Asynchronous success before sync:** rejected because it violates ADR 0012
  and would let callers observe success for bytes or metadata that may disappear
  after a crash.
- **Parallel block writes as the first change:** rejected for the initial slice
  because the current risk and bottleneck are fsync-bound. Parallel `WriteAt`
  changes offset ownership, memory pressure, and partial-write recovery at the
  same time as durability batching.
- **Unbounded linger for larger batches:** rejected because it can turn
  throughput optimization into unpredictable p99 latency and shutdown behavior.
- **Tunable public config before evidence:** rejected until #177 shows which
  constants matter. Premature config would expand the production support surface
  without a measured operating range.
