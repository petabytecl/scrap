# S.C.R.A.P. — V2 Context

> Session-restoration document for AI-assisted development.
> Read this at the start of every session. Intentionally short.
> This is V2's starting point, not its spec.

---

## What This Is

S.C.R.A.P. is a transaction-scoped document storage gateway for billing ETL workflows.

Billing services write and read immutable documents (XML, PDF, etc.) through a gRPC API.
S.C.R.A.P. hides whether bytes come from local hot storage, peer replicas, or a backend
object store (S3, GCS, Azure Blob, etc.). The corpus may reach billions of relatively small
files. Documents are addressed by `(tenant_id, transaction_id, document_name)`.

This is not an S3-compatible API. It is a purpose-built gateway with strong consistency
guarantees for the billing ETL use case.

## Language

**Document**:
An immutable file (XML, PDF, etc.) stored in S.C.R.A.P., addressed by
`(transaction_id, document_name)`. Once ACK'd, a Document can never be
modified, overwritten, or deleted by the API. Size: ~16 KiB (p50) to 128 MiB (max).
_Avoid_: File, object, blob, artifact

**Transaction**:
A group of 2–7 related **Documents** from a single billing workflow step. All Documents
in a Transaction share a **Shard**. Identified by a globally unique `transaction_id`.
_Avoid_: Batch, bundle, upload session

**Block**:
An append-only file containing multiple **Documents** as sequential **Frames**. Sealed
when it reaches a size threshold (default 64 MiB). The unit of backend upload and local
eviction. One Block belongs to exactly one **Shard**.
_Avoid_: Volume, chunk, segment

**Frame**:
A contiguous chunk of **Document** bytes within a **Block**. The unit of checksum
verification (CRC-32C). Max 64 KiB. Small Documents fit in a single Frame; larger
Documents span multiple Frames.
_Avoid_: Chunk, slice, piece

**Shard**:
An independent Raft group managing a subset of **Transactions**. Has its own Raft log,
**Pebble Projection**, and **Block** files. Transactions are assigned to Shards via
fixed hash slots.
_Avoid_: Partition, range, region

**Cell**:
A complete S.C.R.A.P. deployment identified by a permanent `cell_id`. One Cell per
Kubernetes cluster. Contains multiple **Members** forming one or more **Shard** groups.
_Avoid_: Cluster (ambiguous with K8s cluster), deployment, instance

**Member**:
A storage node within a **Cell**. Identified by three levels: `cell_id` (permanent),
`member_slot_id` (K8s pod hostname), `member_id` (durable identity on the PVC).
A Member hosts replicas of multiple **Shards**.
_Avoid_: Node (ambiguous with K8s node), replica, pod

**Backend**:
The cloud object store (S3, GCS, Azure Blob) providing cold durability for sealed,
uploaded **Blocks**. Not in the ACK path — **Documents** are ACK'd from local replicas.
One upload per sealed Block from the **Shard** leader.
_Avoid_: Cold storage, archive, remote storage

**Pebble Projection**:
A derived key-value store (CockroachDB's Pebble) indexing **Document** metadata for
fast lookups. Fully rebuildable from Raft log replay + **Block** bytes. Not a source
of truth for visibility or durability.
_Avoid_: Index, cache, store

**Openlog**:
A per-**Shard** crash-recovery mechanism. Records in-flight write prepare records
before bytes are committed to Raft. On recovery, entries are compared against committed
Raft state to identify completed vs. partial writes.
_Avoid_: WAL (ambiguous with Raft WAL), prepare log, journal

### Example dialogue

> **Dev:** "A billing service just wrote 5 invoices for the same order."
> **Expert:** "Those 5 Documents form a Transaction. They'll land in the same Shard,
> packed into the current open Block."
>
> **Dev:** "What if I need to find all the invoices?"
> **Expert:** "FindDocuments on the Transaction — it's a prefix scan in the Pebble
> Projection. You'll get back all 5 Document names."
>
> **Dev:** "And if the pod restarts during a write?"
> **Expert:** "The Member recovers using its Openlog — it checks each prepare record
> against committed Raft state. Partial writes are discarded from the Block."
>
> **Dev:** "How long are Documents kept?"
> **Expert:** "7 years. They live in local Blocks initially, then the Shard leader
> uploads sealed Blocks to the Backend. After upload, the Block can be evicted locally."

## What V2 Is

V2 is a full restart from scratch. V1 produced extensive documentation and a spike,
but the spike gradually blurred into production code without a conscious decision, and
the spec was never finished before implementation began.

**V2 philosophy:** Re-derive everything from first principles. Use V1 reasoning as a
reference — not as a constraint. The design may land in the same place on many decisions,
but nothing is assumed correct until re-derived and questioned.

**The one thing that is locked:** the tech substrate, validated by the V1 spike:

- Language: Go
- Consensus: `go.etcd.io/raft/v3`
- Metadata KV: Pebble
- API: gRPC + protobuf (buf)
- Envelope encryption: OpenBao Transit
- Observability: Prometheus (`client_golang`, service-local registry, `/metrics`)

Everything else — replica count, block format, sharding model, cell federation, write ACK
contract, scope, phasing — is an open question for V2 to re-derive.

## V2 Process Rules

These rules prevent the V1 failure mode (spike → production blur):

1. **Spec before code.** No production package starts until its design is derived,
   questioned, and documented. A spike is evidence, never a foundation.
2. **Hard spike boundary.** Spike code lives in `cmd/scrap-spike` / `internal/spike`.
   It cannot graduate to production packages without an explicit documented decision.
3. **Re-derive, don't assume.** Before accepting a V1 decision, ask: does this still
   make sense? The burden of proof is on the V2 design.
4. **Short docs over long docs.** If a design document takes more than 30 minutes to
   read, it has failed. Distill decisions into ADRs.
5. **ADRs are the output.** Every hard-to-reverse decision must produce a dated ADR
   before implementation of that decision begins.

## The Workload (Stable Facts)

These anchor every design decision:

- Each `transaction_id` creates 2–7 immutable documents
- p50 document size: small (16–128 KiB range)
- p99 document size: medium (128 KiB–4 MiB range)
- Max document size: 128 MiB
- Documents are immutable after creation — no versions, no overwrites
- Metadata lookup target: single-digit milliseconds p95
- First-byte target (hot, local): single-digit milliseconds p95
- File loss is catastrophic
- Continued availability during multi-node loss is required
- After ACK, the gateway is the source of truth

## Member Identity Model (V1 Multi-Member Contract)

V1 landed on a 3-level identity model because conflating any two of these created
silent-divergence bugs. V2 should accept this as the starting point.

| Level | Source | Lifetime | Purpose |
| --- | --- | --- | --- |
| `cell_id` | Operator-assigned config | Permanent for one deployment | Stable identity of one authoritative SCRAP cell; safe in keys, logs, metrics |
| `member_slot_id` | K8s pod hostname (`scrapd-0`) | Lifetime of the StatefulSet slot | Stable peer DNS + operator messages ("pod scrapd-0 is missing") |
| `member_id` | Durable identity record on the member's PVC | Lifetime of the data volume | The actual storage member identity in cluster metadata |

Rules that came out of V1:

- A normal pod restart presents the **same `member_id` from the same PVC**.
- If a PVC is lost, the replacement pod may reuse the `member_slot_id`, but it
  must **not silently reuse the old `member_id`**. Operator must run the
  lost-member workflow: SCRAP catches up metadata, verifies bytes, performs
  membership change under placement rules.
- Peer handshake verifies all 3 identities. A peer address alone is not authority
  to join a shard or serve bytes. Conflicting identity records → non-serving
  member + admin warning.
- Local non-production mode may default `cell_id=local, member_id=local`. This
  mode must be visible in admin output and must NOT satisfy production write
  ACK gates.

Peer discovery in K8s: one StatefulSet, one PVC per member, one headless Service,
separate public/admin/peer listener ports, NetworkPolicies restricting peer +
admin traffic. DNS form:
`<member_slot_id>.<headless_service>.<namespace>.svc.<cluster_domain>:<peer_port>`.

## V1 Spike Evidence (Empirically Validated)

The V1 spike ran 6 evidence rounds with **zero invariant errors** across all runs.
This is tested evidence, not design intent. V2 should accept or explicitly challenge it.

### The Write State Machine

A write moves through 7 distinct states. Each state is a real semantic boundary —
collapsing any two together breaks an invariant the V1 spike or later production
work paid to learn.

```
1. Accepted for processing
     — validation, authz, idempotency key, shard routing OK
     — NO durability promise yet
2. Local bytes durable
     — receiving/leader member: bytes written + verified + fsynced locally
     — document still invisible
3. Peer bytes durable
     — required peer members fsynced + checksum-validated + returned receipts
     — document still invisible
4. Metadata committed
     — shard consensus durably committed metadata command
     — (records visibility, physical refs, envelopes, upload/repair obligations)
5. Visible
     — reads may use committed metadata + any checksum-valid local/peer replica
     — strong read-after-write scoped to authoritative cell
     — *** client ACK returns here ***
6. Backend upload recorded
     — durable metadata outbox holds the upload obligation
     — retry-safe, asynchronous
7. Backend uploaded and verified
     — backend objects + envelopes available as cold durability / restore source
```

Backend upload (states 6–7) is NOT in the ACK path. Upload lag can create admission
pressure when it threatens the local durability window, but lag alone does not
invalidate already-ACKed writes.

Implementation ordering (spike-validated, preserves the state machine above):

```
1. local block bytes → fsync         (→ state 2)
2. .openlog prepare record → fsync   (→ state 2 sealed)
3. required peer prepare             (→ state 3)
4. Raft metadata command on quorum   (→ state 4)
5. Pebble projection commit          (derived; still state 4)
6. client ACK + read visibility      (→ state 5)
```

### Key Spike Conclusions

- The write path is viable **without whole-document heap buffering** by design.
- **Consensus metadata is the authority** for visibility and physical refs. Pebble is a
  derived projection that must be rebuildable from Raft state alone.
- **`ReadDocument` must be all-or-error.** Verify all touched frame checksums before
  streaming any bytes. Returning a clean prefix and failing later weakens the API into
  a partial-success contract. Acceptable tradeoff because p50 doc size is ~16–128 KiB.
- **Frame-level checksums are necessary** for safe ranged reads. A whole-document
  checksum alone is too coarse — one clean frame doesn't prove another frame is clean.
- **Corruption behavior:** quarantine the bad source, retry from a verified alternate
  source, schedule repair, never return least-bad bytes.
- If Pebble projection is deleted, it can be fully rebuilt from replayed Raft state +
  existing block bytes. This was tested in the spike (durable single-node Raft).

### Schema Evolution (V1 Long-Lived Contracts)

7-year retention forced V1 to commit to explicit compatibility rules across
three serialized layers. Any V2 design that wants to ship before establishing
equivalent rules should expect to repay this debt later.

**Compatibility guarantee:** a document written with schema version N must be
readable by software version N+5 or later.

| Layer | V1 Policy |
| --- | --- |
| Proto wire format | Fields added, never removed or renumbered. No field type changes without a major version increment. 2-release deprecation notice before any zeroing. |
| Pebble key schema | 1-byte version prefix on every key: `<version:1byte><existing-key-bytes>`. `PebbleKeySchemaV1 = 0x01`. Legacy unversioned keys detected on open. |
| Raft log format | Reads accept all versions V1..current. Writes use current. Snapshot restore re-encodes to current. |

### Spike Reference Numbers (Developer Laptop, Not Production Targets)

From Run 2 (100 transactions, 449 documents, concurrency=8, local NVMe):

```
write_ack:        p50=11ms   p95=72ms   p99=284ms
head_document:    p50=232µs  p95=808µs  p99=1.16ms
read_first_byte:  p50=1.05ms p95=6.67ms p99=14.75ms
read_full:        p50=1.08ms p95=15.2ms p99=42.2ms
heap_alloc:       8.9 MiB (stable, no buffering growth)
gc_cycles:        526 over ~1 GiB of writes
```

These are orientation numbers only. Authoritative performance gates require pinned
hardware with known disk class, kernel, GOMEMLIMIT, and container settings.

## V1 API Shape (Starting Reference for V2 Discussion)

V1 landed on this RPC surface. V2 should re-derive it, but this is the baseline:

```
DocumentService:
  WriteDocument(stream WriteDocumentRequest) → WriteDocumentResponse
  HeadDocument(HeadDocumentRequest) → HeadDocumentResponse
  ReadDocument(ReadDocumentRequest) → stream ReadDocumentResponse
  FindDocuments(FindDocumentsRequest) → FindDocumentsResponse   // transaction-scoped

TransactionService:
  CompleteTransaction(CompleteTransactionRequest) → CompleteTransactionResponse
  GetTransaction(GetTransactionRequest) → GetTransactionResponse
```

Document identity: `(tenant_id, transaction_id, document_name)` — all opaque strings
validated but not parsed, trimmed, or case-folded by the gateway.

`WriteDocument` is client-streaming: first message = init metadata, rest = byte chunks.
`ReadDocument` is server-streaming: first message = metadata + source, rest = byte chunks.
`FindDocuments` is transaction-scoped only (not cross-tenant search).
`CompleteTransaction` is a lifecycle marker — it does not affect document visibility.

## V1 ADR Reasoning Summary

The _reasoning_ behind V1 decisions, for use when re-deriving V2. These are the _why_,
not locked answers.

| Topic                                   | V1 Reasoning                                                                                                                                                                                                                                                       |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Custom gRPC, not S3**                 | S3 shape hides streaming writes, strong tx-scoped reads, typed cold-read responses, and write admission semantics behind object-store conventions.                                                                                                                 |
| **Immutable blocks**                    | Storing docs as ranges inside sealed blocks avoids billions of tiny backend objects, controls object size and key fanout, enables range reads via block indexes. Block/index/envelope formats become long-lived compatibility contracts as a result.               |
| **Transaction-keyed sharding**          | All docs in a transaction in one shard group — keeps `FindDocuments`, lifecycle, repair, and upload outbox local to one authority. Raft log carries metadata commands only, not bytes (bytes in consensus = too expensive for throughput, recovery, snapshotting). |
| **Fail closed on corruption**           | Partial-byte streaming weakens the API into a partial-success contract. The workload (p50 ~16–128 KiB) makes all-or-error verification affordable.                                                                                                                 |
| **Linux local filesystem**              | Production ACK depends on fsync/fdatasync semantics. NFS, FUSE, mmap-heavy designs, and async IO libraries have different ordering/flush/rename semantics and can make crash-recovery tests prove the wrong thing.                                                 |
| **Single-voter Raft is non-production** | Single-voter Raft is crash-consistent but not independently tamper-evident. Billing docs may be subject to audit or subpoena. A privileged actor with filesystem access can rewrite single-voter local state without another voter disagreeing.                    |
| **Group commit for write throughput**   | Per-write fsync dominates throughput under concurrency. Group commit batches durability syncs without weakening the ACK contract — a caller returns only after its bytes/command are included in a completed durable sync.                                         |
| **Backend capacity profiles**           | S.C.R.A.P. ACKs before backend upload. Local replicated durability must prove the accepted workload can be drained before disk runway becomes unsafe — provider defaults and alerts are not enough.                                                                |

## V1 Hard-Won Lessons (Bug-Derived Constraints)

These are invariants V1 violated _after_ the spike, during the production
refactor. Each one is a concrete metadata-authority failure mode that V2 should
prevent structurally — they are not derivable from reading clean code.

**1. Separate logical errors from infrastructure errors.**
A client-induced error (`ErrConflict`, `ErrTransactionClosed`, etc.) must reply
to that single caller and have **no global side effect**. Only infrastructure
errors (I/O, log write, fsync) may fail-closed the component. V1 collapsed both
into one `recordFailure()` path and a single conflicting client request bricked
metadata writes shard-wide.

**2. Conflict checks belong on the apply side, not pre-batch.**
Two concurrent commits for the same identity within one batch both observed
the pre-batch store state, both passed the pre-batch conflict check, and the
second poisoned the authority on apply. Conflict checks must either run after
apply (with compensating rollback) or against the running apply-side view of
the batch.

**3. Every worker goroutine on a critical path must `recover()`.**
A panic inside a proposal worker without `recover()` exits the goroutine
without replying to in-flight proposals → all callers block forever on
`<-proposal.done`. Worker must reply error to all in-flight proposals before
exiting or re-panicking.

**4. Poisoned state must be observable + recoverable.**
A transient `EIO` / `ENOSPC` / timeout permanently caching a `failureErr` with
no health metric and no in-process reopen path is a production hazard.
Required: (a) health endpoint exposes the poisoned state; (b) `Reset()` /
reopen path clears the error after the underlying resource recovers, OR the
restart-required requirement is surfaced in the health API.

**5. `appliedIndex` and `durableLogIndex` are different watermarks.**
If `appliedIndex < log.LastIndex()` (apply failure in flight), snapshotting at
`appliedIndex` and compacting the log strands entries between them →
unbootable shard on restart. Snapshot/compact must gate on a clean apply
state, or only advance to a known-safe point. Expose **two** named accessors:
the log-durability watermark and the apply-progress cursor; never overload
one name to mean both.

**6. Post-apply enrichment must not mask already-committed writes.**
If apply succeeds but the post-apply read (e.g. `GetTransaction` after
`CompleteTransaction`) fails, the caller sees an error and concludes the
write didn't happen — but it did. Either retry the enrichment internally
before replying, or reply success-with-sentinel and surface enrichment
failures as a separate signal.

**7. Pebble is a derived projection — Raft is the authority.**
Reaffirmed: if Pebble is poisoned, deleted, or behind, the rebuild path is
"replay Raft + verified block bytes." Any V2 design that lets Pebble become
load-bearing for visibility breaks this invariant.

## Other V1 Decisions Worth Remembering

Compact appendix of decisions/contracts from V1 not otherwise captured above.
Each is the *what*; the *why* is in the V1 ADR reasoning table.

- **Backend error taxonomy (provider-neutral)**: `throttled`, `transient`,
  `auth`, `not-found`, `conflict`, `corrupt`, `permanent`. Every backend
  adapter classifies its errors into this set; retry/admission logic keys
  off the class, not the provider's native error.
- **Backend adapter capacity profiles**: every adapter exposes a finite
  capacity profile (production = real budget; non-production filesystem =
  explicitly finite). ACK admission gates on this profile, not provider
  defaults.
- **OpenBao Transit boundary**: deterministic fake Transit supports
  data-key / unwrap / rewrap / outage / missing-key behavior in tests.
  Rewrap is a durable, idempotent operation with audit evidence. Encrypted
  envelope reads return a typed `crypto-unavailable` response when key
  material is missing.
- **Single-voter Raft forensic gap (ADR 0016)**: documented, not closed.
  Option A (acknowledge + compensating controls: non-root container,
  read-only root FS, K8s RBAC, restricted volume attach, audited admin ops).
  Option B (post-commit HMAC-SHA256 to external append-only log) reserved
  for production. Production forensic integrity blocked until Option B
  evidence exists OR multi-voter Raft (Option C) is shipped.
- **Group-commit constraints (ADR 0017)**: sync errors fail **all** affected
  waiters and force the component fail-closed. Close/seal/repair must
  flush-or-fail pending batches before continuing. Queue depth, batch size,
  sync latency, retry/drop behavior all observable and bounded.
- **Placement is production-fail-closed**: a production shard must not
  silently lower its replica/failure-domain requirements because too few
  K8s nodes are eligible. Unsatisfiable placement → shard reports
  placement-unhealthy; admission continues only while the selected
  durability policy can still be met.
- **Lease reads disabled**: V1 did not enable Raft lease reads. Reasoning:
  timing/fencing assumptions need explicit evidence first. ReadIndex protocol
  is the read freshness mechanism.

## V2 First Slice — Resolved Design Decisions

Resolved through structured design session (2026-05-25). See `docs/adr/` for
hard-to-reverse decisions with full rationale.

### Scope

Single Shard, multi-voter Raft, full write-through-ACK + read path.
Deferred: backend upload, cell federation, multi-tier write ACK, encryption (OpenBao).
API: WriteDocument, HeadDocument, ReadDocument, FindDocuments (4 RPCs).

### Safety Invariants

1. No acknowledged write may be lost
2. No read may return corrupt bytes (all-or-error)
3. No read may return an unacknowledged Document
4. No silent data divergence between replicas
5. Metadata is the authority for Document existence

### Replication

Voter-count-agnostic code. Deploy with 3 voters (dev) / 5 voters (prod).
Byte replication: leader fan-out via separate gRPC peer service (not through Raft).
Bytes on quorum Members before Raft metadata commit. See ADR 0001.

### Write Path

1. Client streams bytes to leader
2. Leader writes to local Block + fsync
3. Leader fans out bytes to all N-1 peers in parallel; waits for quorum-1 ACKs
4. Leader proposes metadata to Raft; waits for quorum commit (~330 bytes)
5. Leader applies to Pebble Projection
6. Leader ACKs client → Document visible

### Read Path

Leader-only reads for first slice. ReadIndex from followers is a known extension.
Client routing: smart Go client library with redirect-on-leader-change, no gateway.
Document resolution: Pebble maps Transaction → Block ID; .idx file resolves per-Document
metadata (offset, size, checksum). See ADR 0004.

### Pebble Projection Model

Lean per-Transaction keys (Option D): Pebble stores `transaction_id` →
`{block_ids, doc_count, completed}` (~27 bytes). A Transaction may span multiple
Blocks when a seal triggers between Documents, so `block_ids` is a list.
Per-Document resolution via .idx files.
Metadata tiering: Pebble holds a configurable hot window (default 6 months, ~60 GiB/shard
at 50M tx/day). Eviction triggers: (1) entries older than the hot window (time-based),
(2) operator-initiated on-demand eviction, (3) automatic eviction under disk pressure
(when local metadata exceeds a configurable threshold). Evicted entries go into monthly
catalog archives
uploaded to the Backend (~1.5-2 GiB each, compressed). Per-month bloom filters (~10 GiB
total, mmap'd locally) enable deterministic cold lookup without scanning. Bloom filters
are derived from catalogs (rebuildable, not backed up). Cold reads: bloom filter check
(~100µs) → download + cache right monthly catalog (~3s once, then instant) → lookup
transaction → block_id → .idx → bytes. Catalog granularity is a deployment config
(default monthly). See ADR 0004.

### Identity Model

Document identity: `(transaction_id, document_name)`. Transaction IDs are globally
unique (enforced by billing services). `tenant_id` is an optional API field reserved
for future routing, federation, and quota features — not stored in the data path.
Shard routing: `hash(transaction_id) % 1024` → slot → Shard.

### Block Format

Shared Blocks (multiple Documents per Block). 64 MiB seal threshold. 64 KiB max
Frame size. 18-byte self-describing frame headers: magic(2) + version(1) +
flags(1) + doc_seq(4) + frame_seq(2) + payload_len(4) + crc32c(4).
Mirror layout: all replicas have identical Block files. See ADR 0003.
Checksums: CRC-32C per Frame, SHA-256 per Document. See ADR 0002.

### Sharding (future, not in first slice)

Fixed 1024 hash slots. `hash(transaction_id) % 1024` → slot → Shard.
Shard count = deployment config (~3× node count). Placement: node labels (rack, AZ),
fail-closed when placement rules unsatisfiable.

### Data Lifecycle

Phase 1 (first slice): Write ACK'd → local copies on all voters.
Phase 2 (future): Backend upload → leader uploads sealed Blocks.
Phase 3 (future): Partial eviction → followers evict uploaded Blocks.
Phase 4 (future): Cold-only → all local copies evicted, Backend-only reads.

### Raft Operations

Log truncation: two-mode heuristic (free truncation when all replicas healthy;
size-capped at ~4 MiB when a replica is offline, then force snapshot). Always gate
truncation on snapshot-in-progress status.

Atomic batch apply: applied-index + Pebble state update in a single
`Batch.Commit()`. Prevents V1 lesson #5 (appliedIndex vs durableLogIndex mismatch).

### Recovery

Raft snapshot = Pebble Projection only (small). Block files transfer separately
via peer service. New Members catch up via snapshot + block transfer + log replay.
Repair throttling: concurrent block-file repairs limited per node, bandwidth budget
reserves ≥75% of I/O for client reads. Prevents recovery storms.

### Background Scrubbing

Two-tier scrubbing (first slice):
- Light scrub (daily): leader proposes consistency-check via Raft, all voters
  compute Pebble state checksum at same applied index, leader compares. Mismatch
  triggers Pebble wipe + Raft replay on the divergent replica.
- Deep scrub (weekly): sequentially read all local Block bytes, re-verify all Frame
  CRC-32C and Document SHA-256 against Raft metadata. Corrupt → quarantine + fetch
  from peer.
- Auto-repair cap: >5 corrupt Frames per Block → treat as bad disk, quarantine
  entire Block, fetch from peer.
- I/O budget: deep scrub limited to 25% of disk read bandwidth (configurable),
  pauses during high client load.

### Filesystem Layout

```
/data/
├── identity/member.json
├── shards/{shard_id}/
│   ├── raft/{wal/, snap/}
│   ├── pebble/
│   ├── blocks/{block_id}.blk, {block_id}.idx
│   └── openlog/{write_id}.prep
└── tmp/
```

### Open Questions (deferred beyond first slice)

- Cell federation model
- Multi-tier write ACK (priority classes)
- Backend capacity profiles and admission pressure
- Encryption (OpenBao Transit integration)
- Group commit optimization
- Shard rebalancing and slot transfer protocol
