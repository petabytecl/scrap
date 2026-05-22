# Write Path Implementation Spike

Status: completed evidence

Completed: 2026-05-22

This spike is disposable. Its job was to produce evidence about the storage
gateway write/read lifecycle before production code is created. Keep the code
easy to delete or absorb deliberately; do not promote spike internals into
production code without a separate implementation roadmap slice and review.

## Evidence Summary

The spike completed the intended evidence loop:

- streaming writes and reads through `grpc-go`;
- local block append with explicit sync boundaries;
- `.openlog` prepare records before visibility;
- Pebble as a visible metadata projection;
- whole-document and frame-level checksum verification;
- failed-stream, crash-boundary, corruption, and reopen tests;
- fake peer-prepare ordering;
- single-node, durable single-node, and three-node in-process Raft metadata
  commit barriers;
- ReadIndex freshness gating in the three-node Raft harness;
- repeatable synthetic ETL runs with zero invariant errors.

The most reusable conclusion is not the prototype package layout. The reusable
conclusion is the ordering contract:

1. local block bytes sync;
2. `.openlog` prepare sync;
3. required peer prepare;
4. Raft metadata command applied on quorum;
5. derived metadata projection commit;
6. client ACK and immediate read visibility.

## Reusable Conclusions

- The proposed write/read lifecycle is viable without whole-document buffering
  by design.
- Consensus metadata should be the authority for document visibility and
  physical refs; Pebble-like local stores are derived projections that must be
  rebuildable.
- `ReadDocument` should stay all-or-error for v1. The expected workload makes
  the verification-before-streaming tradeoff acceptable: 128 MiB documents are
  edge cases, while most documents are expected to be below roughly 200 KiB.
- Frame-level checksums are necessary for safe ranged reads. A whole-document
  checksum alone is too coarse once ranged reads are part of the API.
- Local and required peer durability must complete before metadata visibility;
  asynchronous backend upload is not part of the client ACK safety boundary.
- Three-node Raft and ReadIndex evidence supports the intended visibility and
  fresh-read direction, but only as a deterministic prototype harness.

## Remaining Gaps

- Production Raft persistence, snapshots, membership changes, transport,
  restart, and compaction are not designed or implemented.
- Production block, index, envelope, and metadata encodings are not decided by
  the spike.
- Backend upload, restore, scrub, repair, OpenBao, encryption, compression,
  and capacity shaping remain unimplemented in production form.
- Peer byte replication is only a fake prepare hook, not a production data
  transfer protocol.
- The public/admin API schemas, validation layers, and server shells are
  pre-production scaffolding and do not prove the storage service is ready.
- The spike workload is synthetic and local. It is not a production benchmark
  and does not replace crash-injection, upgrade, soak, security, or capacity
  gates.

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
- fake, single-node Raft, durable single-node Raft, and three-node in-process
  Raft commit barriers;
- a synthetic ETL workload with 2 to 7 documents per transaction;
- immediate `HeadDocument` and `ReadDocument` checks after finalize;
- a concise latency and runtime report.

The spike may use a temporary wire codec instead of the final protobuf schema.
That makes transport numbers non-authoritative, but still validates streaming,
durability boundaries, memory shape, and read-after-finalize behavior.

## Non-Goals

The spike does not decide or implement:

- the production protobuf schema;
- production Raft log persistence, membership, snapshots, or ReadIndex;
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
make spike-write-path-raft-durable
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

write_ack: n=112 p50=6.536024ms p95=66.167901ms p99=244.91516ms
head_document: n=112 p50=228.064µs p95=852.351µs p99=1.153359ms
read_first_byte: n=112 p50=847.78µs p95=7.428147ms p99=14.196865ms
read_full: n=112 p50=884.634µs p95=20.262093ms p99=58.064268ms

heap_alloc: 4245768
total_alloc: 3944113904
gc_cycles: 235
gc_pause_total_ns: 41742752
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

write_ack: n=112 p50=7.073075ms p95=64.744472ms p99=264.173843ms
head_document: n=112 p50=215.137µs p95=631.897µs p99=862.845µs
read_first_byte: n=112 p50=939.565µs p95=7.424631ms p99=16.065204ms
read_full: n=112 p50=972.409µs p95=15.948132ms p99=51.902272ms

heap_alloc: 35784360
total_alloc: 3955276728
gc_cycles: 243
gc_pause_total_ns: 45649849
goroutines: 41
```

Interpretation:

- Every completed document passed through the Raft barrier once.
- The default workload still has zero invariant errors with Raft gating enabled.
- This is not quorum evidence yet; it proves only the local Ready/Advance
  integration and the store ordering around the metadata visibility barrier.

### Durable single-node Raft replay

The spike now has a prototype durable single-node Raft log. It writes Raft
`HardState`, entries, and snapshots to a JSONL file with per-record CRC32C and
fsyncs the file before applying committed document records.

Current tested behavior:

- a committed document record is replayed after closing and reopening the Raft
  barrier;
- the reopened barrier can continue accepting new writes without reusing command
  IDs;
- if Pebble metadata is deleted, the visible metadata projection can be rebuilt
  from the replayed Raft document records and the existing block bytes remain
  readable;
- a corrupted durable log record is rejected on restart instead of silently
  rebuilding from suspect consensus metadata.

This sharpens the source-of-truth boundary: Raft metadata is the intended
authority for document visibility and physical refs; Pebble is a derived local
projection that must be rebuildable.

Limitations:

- the durable log format is prototype JSONL with per-record CRC, not a
  production WAL;
- torn-write salvage, compaction, snapshots, and multi-node restart are still
  not modeled;
- the durable replay slice is single-node only, so it does not replace the
  three-node quorum harness.

### Run 4: durable single-node Raft barrier

Date: 2026-05-22

Command:

```bash
make spike-write-path-raft-durable
```

Result:

```text
transactions: 25
documents_planned: 112
documents_completed: 112
concurrency: 4
chunk_size: 1048576
raft_barrier: true
raft_barrier_mode: single-node-durable
raft_leader: 1
raft_applied_commands: 112
bytes_written: 320022481
bytes_read: 320022481
invariant_errors: 0

write_ack: n=112 p50=7.46095ms p95=59.284165ms p99=257.463001ms
head_document: n=112 p50=206.803µs p95=752.481µs p99=912.763µs
read_first_byte: n=112 p50=959.17µs p95=6.364686ms p99=17.091983ms
read_full: n=112 p50=975.63µs p95=16.017114ms p99=50.920083ms

heap_alloc: 21254424
total_alloc: 3946560440
gc_cycles: 231
gc_pause_total_ns: 46286286
goroutines: 41
```

Interpretation:

- The default workload completed cleanly while each Raft Ready batch was written
  to the prototype durable log and fsynced.
- The restart/rebuild behavior is covered by tests rather than this one-shot
  runner.

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
  through the new leader and remain immediately readable after ACK;
- Raft ReadIndex succeeds with a healthy quorum, fails closed while the leader
  is isolated from both followers, and succeeds again after quorum is restored;
- in three-node cluster mode, `HeadDocument` and `ReadDocument` pass the
  ReadIndex freshness barrier before serving metadata.

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
- ReadIndex is wired only for the three-node cluster spike mode; follower read
  routing and redirect behavior are not modeled yet;
- each document waits for quorum-applied metadata before ACK, which is stricter
  than the minimum Raft commit point and acceptable for this prototype evidence.

### Run 5: three-node Raft cluster barrier

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

write_ack: n=112 p50=6.907994ms p95=53.428583ms p99=238.905015ms
head_document: n=112 p50=196.989µs p95=565.552µs p99=821.031µs
read_first_byte: n=112 p50=766.366µs p95=6.233301ms p99=13.92309ms
read_full: n=112 p50=798.648µs p95=17.561067ms p99=50.02302ms

heap_alloc: 19958448
total_alloc: 3948474384
gc_cycles: 245
gc_pause_total_ns: 37525859
goroutines: 40
```

Interpretation:

- The default workload completed cleanly with visible metadata gated by a
  three-node quorum-applied Raft command.
- The run is still not production performance evidence; the cluster is
  in-process, memory-backed, and deterministic.

### Run 6: final default write-path check

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
bytes_written: 320022481
bytes_read: 320022481
invariant_errors: 0

write_ack: n=112 p50=6.315967ms p95=56.283759ms p99=246.565202ms
head_document: n=112 p50=246.295µs p95=615.075µs p99=769.398µs
read_first_byte: n=112 p50=916.933µs p95=3.415393ms p99=3.92507ms
read_full: n=112 p50=947.545µs p95=11.846028ms p99=39.762683ms

heap_alloc: 19145056
total_alloc: 3920412760
gc_cycles: 232
gc_pause_total_ns: 38051776
goroutines: 39
```

Interpretation:

- The final default run completed all planned documents with zero invariant
  errors after the frame-checksum, crash-boundary, peer-prepare, and Raft
  evidence slices had been added.
- This closes the spike as evidence. The next work should be roadmap and ADR
  planning, not automatic production implementation.
