# Write Path Implementation Spike

Status: active prototype

This spike is disposable. Its job is to produce evidence about the storage
gateway write/read lifecycle before production code is created. Keep the code
easy to delete or absorb deliberately.

## Question

Can the proposed S.C.R.A.P. hot path stream documents through gRPC, prepare
bytes durably in a local block file, commit document visibility in Pebble, and
serve immediate post-finalize reads without whole-document buffering?

## Scope

The spike should include:

- real `grpc-go` streaming;
- real local file IO with explicit sync boundaries;
- real Pebble metadata commits;
- SHA-256 checksums for written and read bytes;
- a minimal `.blk` and `.openlog` shape;
- fake, single-node Raft, and three-node in-process Raft commit barriers;
- a synthetic ETL workload with 2 to 7 documents per transaction;
- immediate `HeadDocument` and `ReadDocument` checks after finalize;
- a concise latency and runtime report.

The spike may use a temporary wire codec instead of the final protobuf schema.
That makes transport numbers non-authoritative, but still validates streaming,
durability boundaries, memory shape, and read-after-finalize behavior.

## Non-Goals

The spike does not decide or implement:

- the production protobuf schema;
- production Raft log persistence, restart, membership, snapshots, or ReadIndex;
- peer byte replication;
- OpenBao integration;
- backend upload;
- encryption, compression, restore, scrub, or repair;
- duplicate active-write and idempotency race semantics beyond the unique
  synthetic workload;
- Kubernetes topology;
- final package layout.

## Definition Of Done

The spike is useful when it can run with one command and produce:

- number of transactions and documents attempted;
- bytes written and bytes read;
- write ACK latency p50, p95, p99;
- `HeadDocument` latency p50, p95, p99;
- read first-byte and full-read latency p50, p95, p99;
- heap, allocation, GC, and goroutine summary;
- failed invariant count, if any;
- path to the scratch data directory for inspection.

Hard correctness invariants for this spike:

- no acknowledged document is missing from Pebble metadata;
- no acknowledged document returns corrupt bytes on immediate read;
- uncommitted prepared bytes are not visible through `HeadDocument` or
  `ReadDocument`;
- the process does not buffer whole document payloads in memory by design.

## Initial Slices

1. Add the disposable runner and local storage skeleton.
2. Stream synthetic documents through gRPC into a local block file.
3. Commit visibility metadata to Pebble after block and openlog sync.
4. Read committed byte ranges back through gRPC and verify checksums.
5. Capture the first report and decide whether to promote, rewrite, or discard
   the validated pieces.

## Run

```bash
make spike-write-path
```

Useful variants:

```bash
go run ./cmd/scrap-spike -transactions 500 -concurrency 16
go run ./cmd/scrap-spike -dir /tmp/scrap-spike -transactions 1000
make spike-write-path-raft
make spike-write-path-raft-cluster
```

## Run Evidence

### Run 1: happy-path write/read spike

Date: 2026-05-22

Command:

```bash
make spike-write-path
```

Result:

```text
transactions: 25
documents_planned: 112
documents_completed: 112
concurrency: 4
chunk_size: 1048576
raft_barrier: false
bytes_written: 320022481
bytes_read: 320022481
invariant_errors: 0

write_ack: n=112 p50=7.13405ms p95=53.911162ms p99=239.181412ms
head_document: n=112 p50=189.533µs p95=701.592µs p99=978.664µs
read_first_byte: n=112 p50=831.498µs p95=5.70274ms p99=14.858788ms
read_full: n=112 p50=864.059µs p95=13.975753ms p99=46.485733ms

heap_alloc: 14717536
total_alloc: 3948168656
gc_cycles: 245
gc_pause_total_ns: 39595021
goroutines: 39
```

Interpretation:

- The first runnable spike validated the basic gRPC streaming write/read path.
- The first tests now cover commit visibility, failed-stream invisibility,
  close/reopen recovery, and truncated/corrupt local block fail-closed behavior.
- The report is not a production performance result; it ran on a local developer
  environment without pinned disk, kernel, runtime, or container settings.

## Findings

### Frame checksum planning

The spike keeps the current design contract: `ReadDocument` is all-or-error
before byte streaming. It does not return a clean prefix and then fail later if
a later frame is corrupt.

That contract is accepted for v1. The expected workload makes the tradeoff
reasonable: 128 MiB documents are edge cases, while most documents are expected
to be below roughly 200 KiB.

The first checksum implementation verified whole documents before streaming.
That protected full reads but left ranged reads too weak. The current spike now
records per-frame segment checksums in committed metadata and verifies every
frame segment touched by the requested range before sending byte chunks.

This preserves the invariant that no corrupt bytes are returned and lets a clean
range inside one frame succeed even if a later frame is corrupt. It does not
optimize first-byte latency for full-document reads yet, because all-or-error
full reads still need to verify every touched frame before streaming.

Changing that behavior would be a product/API decision, not an implementation
detail: it would weaken all-or-error reads into "verified bytes may stream until
a later frame fails."

### Run 2: frame-checksum read planning

Date: 2026-05-22

Command:

```bash
go run ./cmd/scrap-spike -transactions 100 -concurrency 8
```

Result:

```text
transactions: 100
documents_planned: 449
documents_completed: 449
concurrency: 8
chunk_size: 1048576
bytes_written: 976786475
bytes_read: 976786475
invariant_errors: 0

write_ack: n=449 p50=11.241334ms p95=71.943525ms p99=284.311217ms
head_document: n=449 p50=232.368µs p95=807.575µs p99=1.157417ms
read_first_byte: n=449 p50=1.047264ms p95=6.674803ms p99=14.750475ms
read_full: n=449 p50=1.082634ms p95=15.170812ms p99=42.18777ms

heap_alloc: 8869360
total_alloc: 12254228792
gc_cycles: 526
gc_pause_total_ns: 113986877
goroutines: 39
```

Interpretation:

- Frame-checksum planning kept the default and larger synthetic workloads clean.
- First-byte latency is still bounded by all-or-error frame verification for the
  requested range.
- The next useful evidence should come from peer-prepare simulation or explicit
  crash-point injection, not from increasing local happy-path volume alone.

### Crash-point injection

The spike now has narrow write fault hooks for repeatable crash-boundary tests.
Those hooks are prototype-only; they exist to make durability claims executable.

Current tested boundaries:

- failure after local block sync leaves the document invisible after reopen;
- failure after `.openlog` sync but before metadata commit leaves the document
  invisible after reopen;
- failure after metadata commit still returns success and the document survives
  reopen.

Without an explicit Raft barrier, Pebble metadata commit is the spike's fake
consensus visibility barrier. That keeps the production rule concrete: before
the barrier, prepared bytes are durable but invisible; after the barrier, the
document is ACKed and must remain readable or repairable.

### Peer prepare boundary

The spike now has a minimal fake peer-prepare hook. It does not model placement,
membership changes, network partitions, or Raft. It only validates the write-path
ordering around ACK safety:

- required peer prepares happen after local block/openlog durability and before
  metadata visibility;
- a successful peer prepare sees the exact document bytes and committed physical
  ref candidate;
- peer prepare failure prevents the document from becoming visible, including
  after close/reopen.

This keeps the v1 durability promise testable in miniature: asynchronous backend
upload is acceptable only after local and required peer durability have succeeded.

### Raft metadata commit barrier

The spike now supports a minimal `go.etcd.io/raft/v3` metadata commit barrier.
It is intentionally a single-node in-process Raft harness, not production shard
consensus.

Current tested behavior:

- document visibility can be gated behind a real Raft proposal/commit/apply
  cycle instead of a direct function call;
- the store only writes visible Pebble metadata after the Raft barrier applies
  the command;
- closing the Raft barrier before write prevents document visibility, including
  after close/reopen;
- an ACKed document committed through the barrier remains readable after
  close/reopen because Pebble remains the spike's visible metadata projection.

Limitations:

- Raft log persistence, restart from Raft storage, leader change, followers,
  network partitions, snapshots, membership changes, and ReadIndex are still
  outside this slice.
- The next Raft slice should move from a single-node barrier to a deterministic
  in-process multi-node harness with dropped/reordered messages and leader
  changes.

### Run 3: single-node Raft barrier

Date: 2026-05-22

Command:

```bash
make spike-write-path-raft
```

Result:

```text
transactions: 25
documents_planned: 112
documents_completed: 112
concurrency: 4
chunk_size: 1048576
raft_barrier: true
raft_barrier_mode: single-node
raft_leader: 1
raft_applied_commands: 112
bytes_written: 320022481
bytes_read: 320022481
invariant_errors: 0

write_ack: n=112 p50=6.989369ms p95=54.949683ms p99=237.995249ms
head_document: n=112 p50=209.914µs p95=725.381µs p99=1.103282ms
read_first_byte: n=112 p50=955.127µs p95=6.718251ms p99=15.190404ms
read_full: n=112 p50=1.00124ms p95=14.817152ms p99=50.556805ms

heap_alloc: 18027552
total_alloc: 3945480944
gc_cycles: 241
gc_pause_total_ns: 41992800
goroutines: 41
```

Interpretation:

- Every completed document passed through the Raft barrier once.
- The default workload still has zero invariant errors with Raft gating enabled.
- This is not quorum evidence yet; it proves only the local Ready/Advance
  integration and the store ordering around the metadata visibility barrier.

### Three-node Raft cluster harness

The spike now has a deterministic three-node in-process `raft.RawNode` harness.
It is still disposable code, but it moves beyond the single-node barrier by
testing the behaviors that matter to the metadata visibility contract:

- document ACK waits until the metadata command applies on a quorum;
- with both followers isolated, prepared bytes remain invisible until quorum is
  restored;
- with one leader/follower link dropped, the document can still commit through
  the remaining quorum and the dropped follower catches up after the link heals;
- after isolating the old leader and campaigning a new one, writes continue
  through the new leader and remain immediately readable after ACK.

This is stronger evidence for the commit ordering rule:

1. local block bytes sync;
2. `.openlog` prepare sync;
3. required peer prepare;
4. Raft metadata command applied on quorum;
5. Pebble visibility commit and client ACK.

The Raft command now carries the full `DocumentRecord`, including physical block
reference, logical checksum, length, and frame checksums. Tests assert that the
applied Raft record matches the metadata that becomes visible through Pebble.
That matters because consensus metadata, not Pebble itself, is the intended
authority; Pebble is only the spike's local visible projection.

Limitations that still matter:

- the Raft log is in memory only;
- node restart from persisted Raft state is not modeled;
- message loss is deterministic drop/isolate behavior, not real transport;
- cluster membership and snapshots are not modeled;
- each document waits for quorum-applied metadata before ACK, which is stricter
  than the minimum Raft commit point and acceptable for this prototype evidence.

### Run 4: three-node Raft cluster barrier

Date: 2026-05-22

Command:

```bash
make spike-write-path-raft-cluster
```

Result:

```text
transactions: 25
documents_planned: 112
documents_completed: 112
concurrency: 4
chunk_size: 1048576
raft_barrier: true
raft_barrier_mode: three-node-cluster
raft_leader: 1
raft_applied_commands: 112
bytes_written: 320022481
bytes_read: 320022481
invariant_errors: 0

write_ack: n=112 p50=6.408025ms p95=57.441061ms p99=237.804679ms
head_document: n=112 p50=237.282µs p95=756.497µs p99=977.719µs
read_first_byte: n=112 p50=841.312µs p95=6.410452ms p99=13.77898ms
read_full: n=112 p50=864.558µs p95=17.746044ms p99=49.049889ms

heap_alloc: 15760944
total_alloc: 3943131072
gc_cycles: 243
gc_pause_total_ns: 39824259
goroutines: 40
```

Interpretation:

- The default workload completed cleanly with visible metadata gated by a
  three-node quorum-applied Raft command.
- The run is still not production performance evidence; the cluster is
  in-process, memory-backed, and deterministic.
