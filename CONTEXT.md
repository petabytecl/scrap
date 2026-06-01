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
files. V2 Documents are addressed by `(transaction_id, document_name)`; `tenant_id`
may appear on API requests for future routing but is not part of storage identity.

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
`member_hostname` (K8s pod hostname), `member_id` (durable identity on the PVC).
A Member hosts replicas of multiple **Shards**.
_Avoid_: Node (ambiguous with K8s node), replica, pod

**Backend**:
The cloud object store (S3, GCS, Azure Blob) providing cold durability for sealed,
uploaded **Blocks**. Not in the ACK path — **Documents** are ACK'd from local replicas.
One upload per sealed Block from the **Shard** leader.
_Avoid_: Cold storage, archive, remote storage

**Pebble Projection**:
A derived key-value store (CockroachDB's Pebble) indexing **Document** metadata for
fast lookups. In the Phase 1 single-node spike-store, Pebble is the local visibility
authority because Raft does not exist yet. In Phase 2+, Pebble returns to being a
fully rebuildable projection from Raft log replay + **Block** bytes, not the source
of truth for visibility or durability.
_Avoid_: Index, cache, store

**Projection Resolution**:
The read-side process that turns a visible **Pebble Projection** Transaction entry
into **Document** metadata by walking its **Block** IDs and reading each Block's
`.idx` file. In Phase 1, it is the single place that owns fail-closed behavior for
visible metadata corruption and write-order listing for `FindDocuments`.
_Avoid_: Index lookup, metadata lookup, document resolver

**Openlog**:
A per-**Shard** crash-recovery mechanism. Records in-flight write prepare records
before bytes are committed to Raft. On recovery, entries are compared against committed
Raft state to identify completed vs. partial writes.
_Avoid_: WAL (ambiguous with Raft WAL), prepare log, journal

**Upload Outbox**:
A per-**Shard** durable record of sealed **Blocks** pending upload to the
**Backend**. Tracked via Raft commands (`SealBlock` on seal, `ConfirmUpload` after
verified upload). A new leader scans the outbox and resumes uploads. The outbox
drives admission pressure: when pending upload bytes exceed the configured budget,
the **Shard** rejects new writes to prevent local disk exhaustion.
_Avoid_: Upload queue (implies in-memory), upload log (ambiguous with Raft log)

**Confirmed Upload Catalog**:
A derived per-**Shard** record of sealed **Blocks** whose upload to the
**Backend** was confirmed by committed Raft state. Used by Phase 4 to decide
whether a local `.blk` copy can be considered for eviction and to find Backend
metadata for restore. It is not eviction state and not Backend inventory.
_Avoid_: Upload Outbox (pending uploads), Eviction Marker, Backend listing

**Block Quarantine**:
A filesystem-level isolation of a corrupt **Block**. The `.blk` and `.idx` files are
renamed to `.blk.quarantine` / `.idx.quarantine`. Triggered by **Deep Scrub** when
Frame CRC or Document SHA-256 verification fails. The Block is replaced from a peer
via `TransferBlock`. All **Documents** in a Block-quarantined Block become unreadable
until repair completes.
_Avoid_: Content Quarantine (different mechanism)

**Content Quarantine**:
A metadata-level gate on a single **Document** flagged by the **Content Scanner** as
potentially malicious. A dedicated Pebble key prefix records quarantined Document
identities, replicated via a `QuarantineDocument` Raft command. `ReadDocument` returns
`FAILED_PRECONDITION`; `HeadDocument` and `FindDocuments` return metadata with a
`scan_status` field. Block bytes are untouched. An operator can confirm (permanent
quarantine) or release (false positive) via the **Admin Service**.
_Avoid_: Block Quarantine (different mechanism), flag, hold

**Content Scanner**:
A background process on the **Shard** leader that scans sealed **Block** bytes for
malware using ClamAV signatures and YARA rules. Runs asynchronously after Document
ACK — never in the write path. Tracks progress via a block-ID watermark and a
signature-version watermark. Shares I/O budget with **Deep Scrub** to avoid starving
client reads.
_Avoid_: AV scanner, virus scanner, malware scanner

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
- Observability: OpenTelemetry producer contract (OTLP telemetry, collector-routed
  metrics/logs/traces/profiles, self-hosted evidence stack for stress runs)

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

| Level             | Source                                      | Lifetime                         | Purpose                                                                      |
| ----------------- | ------------------------------------------- | -------------------------------- | ---------------------------------------------------------------------------- |
| `cell_id`         | Operator-assigned config                    | Permanent for one deployment     | Stable identity of one authoritative SCRAP cell; safe in keys, logs, metrics |
| `member_hostname` | K8s pod hostname (`scrapd-0`)               | Lifetime of the StatefulSet slot | Stable peer DNS + operator messages ("pod scrapd-0 is missing")              |
| `member_id`       | Durable identity record on the member's PVC | Lifetime of the data volume      | The actual storage member identity in cluster metadata                       |

Rules that came out of V1:

- A normal pod restart presents the **same `member_id` from the same PVC**.
- If a PVC is lost, the replacement pod may reuse the `member_hostname`, but it
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
`<member_hostname>.<headless_service>.<namespace>.svc.<cluster_domain>:<peer_port>`.

Implementation note: older V2 code and telemetry may still use
`member_slot_id` until a compatibility pass renames those identifiers.

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
5. Projection commit                  (derived; still state 4)
6. client ACK + read visibility      (→ state 5)
```

### Key Spike Conclusions

- The write path is viable **without whole-document heap buffering** by design.
- **Consensus metadata is the authority** for visibility and physical refs. The projection
  is derived and must be rebuildable from Raft state alone.
- **`ReadDocument` must be all-or-error.** Verify all touched frame checksums before
  streaming any bytes. Returning a clean prefix and failing later weakens the API into
  a partial-success contract. Acceptable tradeoff because p50 doc size is ~16–128 KiB.
- **Frame-level checksums are necessary** for safe ranged reads. A whole-document
  checksum alone is too coarse — one clean frame doesn't prove another frame is clean.
- **Corruption behavior:** quarantine the bad source, retry from a verified alternate
  source, schedule repair, never return least-bad bytes.
- If the projection is deleted, it can be fully rebuilt from replayed Raft state +
  existing block bytes. This was tested in the spike (durable single-node Raft).

### Schema Evolution (V1 Long-Lived Contracts)

7-year retention forced V1 to commit to explicit compatibility rules across
three serialized layers. Any V2 design that wants to ship before establishing
equivalent rules should expect to repay this debt later.

**Compatibility guarantee:** a document written with schema version N must be
readable by software version N+5 or later.

| Layer             | V1 Policy                                                                                                                                            |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Proto wire format | Fields added, never removed or renumbered. No field type changes without a major version increment. 2-release deprecation notice before any zeroing. |
| Pebble key schema | 1-byte version prefix on every key: `<version:1byte><existing-key-bytes>`. `PebbleKeySchemaV1 = 0x01`. Legacy unversioned keys detected on open.     |
| Raft log format   | Reads accept all versions V1..current. Writes use current. Snapshot restore re-encodes to current.                                                   |

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

## V2 API Contract (Resolved Phase 1 Boundary)

V2 keeps the DocumentService surface and deliberately excludes TransactionService from
the Phase 1 spike-store milestone:

```
DocumentService:
  WriteDocument(stream WriteDocumentRequest) → WriteDocumentResponse
  HeadDocument(HeadDocumentRequest) → HeadDocumentResponse
  ReadDocument(ReadDocumentRequest) → stream ReadDocumentResponse
  FindDocuments(FindDocumentsRequest) → FindDocumentsResponse   // transaction-scoped
```

Document identity: `(transaction_id, document_name)`.

`tenant_id` is an optional request field reserved for future routing, federation,
and quota features. It is validated when present, but it is not part of storage
identity, not persisted as authoritative metadata, and not echoed as authoritative
response state in Phase 1.

`idempotency_key` is optional and validated, but is not authoritative in Phase 1.
Duplicate `(transaction_id, document_name)` always returns `ALREADY_EXISTS`,
regardless of idempotency key or content. Full idempotent retry semantics are deferred.

`WriteDocumentRequest` uses an explicit `oneof`:

- `init`: transaction/document metadata
- `chunk_data`: raw bytes

`ReadDocumentResponse` uses an explicit `oneof`:

- `meta`: verified Document metadata
- `chunk_data`: raw bytes

The API exposes SHA-256 as lowercase 64-character hex. Store, Block, and Index code
store SHA-256 as raw 32-byte digests.

Boundary validation preserves exact text. The gateway must not trim, normalize, or
case-fold identifiers.

| Field             | Rule                    |
| ----------------- | ----------------------- |
| `transaction_id`  | required, max 256 bytes |
| `document_name`   | required, max 512 bytes |
| `content_type`    | required, max 255 bytes |
| `tenant_id`       | optional, max 256 bytes |
| `idempotency_key` | optional, max 256 bytes |

NUL and other control characters are rejected in text fields. Zero-byte Documents are
invalid. Max client chunk size is 1 MiB. Max Document size is 128 MiB.

Store and server errors are typed and mapped centrally to gRPC status codes:
`ALREADY_EXISTS`, `NOT_FOUND`, `INVALID_ARGUMENT`, `RESOURCE_EXHAUSTED`, `DATA_LOSS`,
and `UNAVAILABLE`. Substring-based error mapping is forbidden.

`FindDocuments` is transaction-scoped only. Results are returned in write order:
ascending Block ID, then append order within each `.idx`.

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
Each is the _what_; the _why_ is in the V1 ADR reasoning table.

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
- **Single-voter Raft forensic gap (V1 ADR 0016)**: documented, not closed.
  Option A (acknowledge + compensating controls: non-root container,
  read-only root FS, K8s RBAC, restricted volume attach, audited admin ops).
  Option B (post-commit HMAC-SHA256 to external append-only log) reserved
  for production. Production forensic integrity blocked until Option B
  evidence exists OR multi-voter Raft (Option C) is shipped.
- **Group-commit constraints (V1 ADR 0017)**: sync errors fail **all** affected
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

## V2 Phasing — Resolved Design Decisions

Resolved through structured design sessions (2026-05-25 and 2026-05-26). See
`docs/adr/` for hard-to-reverse decisions with full rationale.

### Scope

Phase 1 is the single-node spike-store milestone: protobuf API, Store behavior,
gRPC error/streaming behavior, Block/Frame/.idx formats, projection value encoding,
local read/write path, sealing, resource limits, and integrity tests. Those parts
are contract-grade. `internal/spike` is replaceable Phase 2 scaffolding.

Phase 1 deliberately excludes Raft, byte replication, quorum ACK semantics, backend
upload, TLS/auth, full idempotent retry, and repair/quarantine workflow.

Phase 2 is the first V2 safety milestone: single Shard, multi-voter Raft, full
write-through-ACK + read path. Deferred beyond that: backend upload, cell federation,
multi-tier write ACK, encryption (OpenBao).

Phase 2 splits into two sub-milestones:

- **Phase 2a** (core replicated path): Raft bootstrap + apply loop, peer byte
  replication, shard orchestrator (`store.Store` implementation), openlog, leader-only
  reads via ReadIndex, client routing (leader hint), block transfer for recovery,
  multi-member integration tests, Kind E2E harness.
- **Phase 2b** (scrubbing + hardening): light scrub (projection checksum comparison),
  deep scrub (block byte re-verification), ConsistencyCheck peer RPC, quarantine +
  auto-repair, I/O budget throttling. Scrubbing detects latent divergence but does
  not prevent it — the core path (Raft + frame checksums + apply-side idempotency)
  prevents divergence.

API: WriteDocument, HeadDocument, ReadDocument, FindDocuments (4 RPCs).

### Phase 1 Read/Write Semantics

`created_at` is assigned by the Store at Document commit time and persisted/returned
consistently.

`WriteDocumentResponse` returns only after Block bytes, `.idx` entry, and projection entry
are locally durable. New `.blk`/`.idx` files require directory fsync for their entries.

`ReadDocument` verifies the whole Document before sending any stream message. Corrupt
reads send zero metadata/chunk messages and return `DATA_LOSS`. The read path is
two-pass and bounded-memory: pass 1 verifies Frame headers, payload CRCs, sequence,
frame count, and Document SHA-256; pass 2 streams verified bytes.

### Safety Invariants

1. No acknowledged write may be lost
2. No read may return corrupt bytes (all-or-error)
3. No read may return an unacknowledged Document
4. No silent data divergence between replicas
5. Metadata is the authority for Document existence: the projection in Phase 1,
   Raft metadata in Phase 2+

### Replication

Phase 1 has no replication. Phase 2 uses voter-count-agnostic code. Deploy with
3 voters (dev) / 5 voters (prod). Byte replication: leader fan-out via separate
gRPC peer service (not through Raft). Bytes on quorum Members before Raft metadata
commit. See ADR 0001.

### Phase 2 Write Path

1. Client streams bytes to leader
2. Leader writes openlog `.prep` file + fsync (crash-recovery intent record)
3. Leader writes to local Block + fsync
4. Leader fans out bytes to all N-1 peers in parallel; waits for quorum-1 ACKs
5. Leader proposes metadata to Raft; waits for quorum commit (~330 bytes)
6. Leader applies to projection (apply-side conflict detection)
7. Leader deletes `.prep` file, ACKs client → Document visible

Leadership loss during write: the shard detects the term change, returns
`ErrNotLeader` to the client, and does NOT clean up the `.prep` file or Block bytes.
Openlog recovery handles all partial states uniformly — if no Raft commit exists for
the `.prep` entry, the Block is truncated at the recorded start offset and the `.prep`
is deleted.

### Raft Metadata Command Format

Raft log entries carry protobuf-encoded metadata commands (~330 bytes per document).
A `RaftCommand` message wraps a `oneof command` for extensibility — Phase 2 uses
`CommitDocument` (transaction/document identity, block ID, frame offset, frame count,
total bytes, SHA-256, created_at, content type, idempotency key). Future commands:
`CompleteTransaction`, tombstones, membership changes.

### Apply-Side Conflict Detection

Pre-proposal: optimistic check against the projection for fast rejection of obvious duplicates.
Apply loop (authoritative): check the projection again before applying. If the document
already exists, the committed entry is a deterministic no-op — don't update the
projection, reply `ALREADY_EXISTS` to the caller. First-writer-wins. All replicas execute
the same deterministic apply function, producing identical projection state. Replay-safe:
rebuilding the projection from Raft log replay produces the same result. Prevents V1 lesson #2 (conflict
checks on the apply side, not pre-batch).

### Peer Byte Replication Service

Separate gRPC service (`PeerService`) for inter-member byte transfer, distinct from the
client-facing `DocumentService`. Two RPCs:

- `ReplicateDocument` (client-streaming): hot-path write replication. Leader pushes
  document frames to a follower during a write. Mirrors `WriteDocument`'s init + chunk
  streaming shape. Follower writes to its local Block, verifies checksums, ACKs.
- `TransferBlock` (server-streaming): recovery path. Transfers a sealed Block + `.idx`
  to a new or lagging member. Used during snapshot catch-up and repair.

Phase 2b adds two peer RPCs: `ConsistencyCheck` (leader pulls projection checksum from
voters) and `RequestIndexRebuild` (leader instructs divergent voter to wipe + rebuild
projection from Raft state). Phase 2b also adds `RequestConsistencyCheck` to the
`RaftCommand` oneof (fields: `scrub_id` ULID, `requested_at_us`).

### Phase 2 Read Path

Leader-only reads for the Phase 2 safety milestone. ReadIndex from followers is a known extension.
Client routing: smart Go client library with redirect-on-leader-change, no gateway.
Non-leader members return `UNAVAILABLE` with a `LeaderHint` gRPC status detail containing
the current leader's address. The client extracts the hint and retries directly.
Document resolution: the projection maps Transaction → Block IDs; .idx file resolves
per-Document metadata (offset, size, checksum). See ADR 0004.

### Projection Model

Lean per-Transaction keys (Option D): the projection stores versioned `transaction_id` →
`{block_ids, doc_count, completed}` values. A Transaction may span multiple Blocks
when a seal triggers between Documents, so `block_ids` is a list.
Per-Document resolution via .idx files.

Phase 1 visibility authority is the projection. If Block/.idx bytes exist but the
projection did not commit, the Document is invisible. Infrastructure write, fsync, and
projection failures fail closed.

Metadata tiering: the projection holds a configurable hot window (default 6 months, ~60 GiB/shard
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
for future routing, federation, and quota features. It is accepted and validated,
but ignored for storage identity and not returned as authoritative metadata.
Shard routing: `hash(transaction_id) % 1024` → slot → Shard.

### Phase 1 Resource Controls

Default limits:

- Max concurrent writes: 64
- Max concurrent reads: 256
- Block seal size: 64 MiB
- Max client chunk: 1 MiB
- Max Document: 128 MiB

Excess read/write RPCs are rejected immediately with `RESOURCE_EXHAUSTED`.
Size flags accept raw bytes and IEC suffixes such as `64MiB`.

### Block Format

Shared Blocks (multiple Documents per Block). Default 64 MiB seal threshold.
Seal triggers between Documents, never mid-Document. A single Document larger than
the seal threshold completes in the current Block and then the Block seals.

Block IDs are `uint64`, rendered as fixed-width lowercase hex:
`000000000000002a.blk` and `000000000000002a.idx`. Startup scans valid Block
filenames, allocates `max(existing_block_id) + 1`, never fills gaps, and fails on
malformed `.blk` or `.idx` filenames. Restart always opens a new Block; existing
Blocks are treated as closed.

Block header: 40 bytes, little-endian:
`magic(4) + version(2) + header_len(2) + shard_id(8) + block_id(8) +
created_at_unix_micro(8) + reserved(4) + header_crc32c(4)`. `header_crc32c`
covers bytes 0-35. Readers validate header CRC and filename/header Block ID agreement.

Frame header: 32 bytes, little-endian:
`magic(2) + version(1) + flags(1) + header_len(2) + reserved(2) + doc_seq(4) +
frame_seq(4) + payload_len(4) + payload_crc32c(4) + reserved(4) +
header_crc32c(4)`. `header_crc32c` covers bytes 0-27. Payload CRC uses CRC-32C
Castagnoli.

`.idx` files have a CRC-protected `SIDX` header. Each entry is framed as
`entry_len + payload + entry_crc32c`. Entry payload includes version, reserved,
transaction ID, document name, content type, created_at, first frame offset,
frame count, total bytes, and raw SHA-256 digest. HeadDocument and FindDocuments
fail closed on `.idx` CRC/header/entry corruption.

Mirror layout: all replicas have identical Block files. See ADR 0003.
Checksums: CRC-32C per Frame, SHA-256 per Document. See ADR 0002.

### Sharding (future, not in Phase 1)

Fixed 1024 hash slots. `hash(transaction_id) % 1024` → slot → Shard.
Shard count = deployment config (~3× node count). Placement: node labels (rack, AZ),
fail-closed when placement rules unsatisfiable.

### Data Lifecycle

Phase 1: single-node spike-store milestone; no replicated durability guarantee.
Phase 2: write ACK'd → local copies on quorum voters.
Phase 3: Backend upload → leader uploads sealed Blocks (.blk + .idx as separate
objects) to the Backend, verifies via HEAD + size/ETag, proposes `ConfirmUpload`
via Raft. Upload Outbox tracks obligations. Three-level admission pressure
(WARN/PRESSURE/CRITICAL) prevents unbounded upload lag from filling local disk.
Phase 4 (future): Partial eviction → followers evict uploaded `.blk` data files
while retaining local `.idx` files for metadata reads (see ADR 0016).
Phase 5 (future): Cold-only → all local copies evicted, Backend-only reads.

### Raft Operations

Raft WAL and snapshots use the etcd WAL and snap libraries (`go.etcd.io/etcd/server/v3/wal`
and `go.etcd.io/etcd/server/v3/snap`) — embedded Go libraries, no external etcd server.
The `raft/` directory in the filesystem layout maps to these libraries' file structures.
Raft state is separate from the projection, preserving the "projection is rebuildable
from Raft" invariant.

Log truncation: two-mode heuristic (free truncation when all replicas healthy;
size-capped at ~4 MiB when a replica is offline, then force snapshot). Always gate
truncation on snapshot-in-progress status.

Atomic batch apply: applied-index + projection state update in a single
`Batch.Commit()`. Prevents V1 lesson #5 (appliedIndex vs durableLogIndex mismatch).

### Openlog Lifecycle

Each in-flight write creates a `{write_id}.prep` file in the openlog directory.
`write_id` is a ULID (time-ordered, lexicographically sortable). The `.prep` file is
a protobuf-encoded `OpenlogEntry` containing: transaction ID, document name, block ID,
start offset in the Block, content type, and idempotency key. Size: ~200–300 bytes.

Lifecycle: (1) write `.prep` + fsync file + fsync directory, (2) append document bytes
to Block, (3) fan out to peers, (4) propose to Raft, (5) on Raft commit: delete `.prep`.

Recovery on restart: scan `.prep` files in ULID order. For each: if Raft committed the
document, delete the `.prep` (completed write). If not, truncate the Block at the
recorded `start_offset` and delete the `.prep` (partial write). Processing in ULID order
ensures correct handling when multiple `.prep` files target the same Block.

### Recovery

Raft snapshot = projection state only (small). Block files transfer separately
via peer service. New Members catch up via snapshot + block transfer + log replay.
Repair throttling: concurrent block-file repairs limited per node, bandwidth budget
reserves ≥75% of I/O for client reads. Prevents recovery storms.

### Background Scrubbing

Two-tier scrubbing (Phase 2 safety milestone):

- Light scrub (daily): leader proposes `RequestConsistencyCheck` via Raft, all voters
  compute a streaming SHA-256 over all projection keys at the same applied index,
  leader pulls results via `ConsistencyCheck` peer RPC and compares. Mismatch triggers
  projection wipe + Raft replay on the divergent replica (leader sends
  `RequestIndexRebuild` peer RPC to the divergent voter).
- Deep scrub (weekly): each voter independently reads all sealed Block bytes
  oldest-first, re-verifies all Frame CRC-32C and Document SHA-256 against projection
  metadata. Corrupt → rename `.blk` → `.blk.quarantine`, fetch from peer via
  `TransferBlock`. Open (unsealed) Blocks are skipped.
- Auto-repair cap: >5 corrupt Frames per Block in a single scrub run → treat as bad
  disk, quarantine entire Block, fetch from peer. Emit `scrap_scrub_bad_disk_suspected`
  metric.
- I/O budget: deep scrub rate-limited via token bucket (default 125 MB/s, configurable).
  Pauses when client read p99 latency exceeds threshold (default 10ms, 30s cooldown).
- Failed repair: retry per deep scrub cycle, round-robin peers. No permanent give-up —
  quarantined blocks stay quarantined until a peer provides a good copy.
- Scheduling: ticker + jitter (10%). Leader runs light scrub, all voters run deep scrub
  independently. Deep scrub progress (last-scanned block_id) checkpointed in projection;
  resets on projection rebuild (intentional — re-scan after divergence).

Phase 2b package: `internal/scrub/` with `Light` and `Deep` types.
Dependencies are injected via interfaces that `scrub` itself defines (consistency
checker/proposer, block lister/verifier, quarantine manager, peer repairer/
rebuilder); the concrete projection, peer, and metrics implementations are supplied
by the shard orchestrator (`internal/shard/`), which owns and schedules the
scrubbers. Compile-time dependency direction: `shard → scrub → {block, ulid}`.
Access to the projection (`index`) and peers (`peer`) is inverted — `scrub` defines
the ports and `shard` injects the adapters — so `scrub` does **not** import `index`
or `peer`.

Phase 2b configuration (env vars, all with defaults):

| Env var                      | Default     | Unit      |
| ---------------------------- | ----------- | --------- |
| `SCRAP_SCRUB_ENABLED`        | `true`      | bool      |
| `SCRAP_LIGHT_SCRUB_INTERVAL` | `24h`       | duration  |
| `SCRAP_DEEP_SCRUB_INTERVAL`  | `168h`      | duration  |
| `SCRAP_DEEP_SCRUB_IO_RATE`   | `125000000` | bytes/sec |
| `SCRAP_SCRUB_PAUSE_LATENCY`  | `10ms`      | duration  |
| `SCRAP_SCRUB_PAUSE_COOLDOWN` | `30s`       | duration  |
| `SCRAP_SCRUB_CORRUPT_CAP`    | `5`         | count     |
| `SCRAP_SCRUB_JITTER`         | `0.1`       | fraction  |

### Backend Upload

Leader-only upload of sealed Blocks to the Backend. The leader watches the Upload
Outbox (materialized from `SealBlock` Raft commands without matching `ConfirmUpload`)
and uploads `.blk` + `.idx` as separate objects. Upload is verified via HEAD +
size/ETag check before proposing `ConfirmUpload` (see ADR 0010). Retry policy is
error-class-specific: `throttled` reduces upload concurrency, `auth` pauses all
uploads, `transient` retries with exponential backoff. Admission pressure prevents
unbounded upload lag from filling local disk.

Backend adapter: provider-neutral `Backend` interface with 5 operations (Put, Head,
Get with byte range, Delete, List). Error taxonomy: `throttled`, `transient`, `auth`,
`not-found`, `conflict`, `corrupt`, `permanent`. Each class drives a distinct
retry/admission decision.

Object key format: `{cell_id}/shards/{shard_id}/{block_id}.blk|.idx` (see ADR 0009).

Phase 3 package: `internal/backend/` with `Backend` interface and provider adapters.
Upload processor owned and scheduled by the shard orchestrator (`internal/shard/`).
Dependency direction: `shard → backend`.

Phase 3 configuration (env vars, all with defaults):

| Env var                       | Default      | Unit      |
| ----------------------------- | ------------ | --------- |
| `SCRAP_UPLOAD_ENABLED`        | `true`       | bool      |
| `SCRAP_UPLOAD_CONCURRENCY`    | `2`          | count     |
| `SCRAP_UPLOAD_BUDGET`         | `10737418240`| bytes     |
| `SCRAP_UPLOAD_WARN_PCT`       | `80`         | percent   |
| `SCRAP_UPLOAD_PRESSURE_PCT`   | `90`         | percent   |
| `SCRAP_UPLOAD_CRITICAL_PCT`   | `95`         | percent   |

Phase 4 configuration (env vars, all with defaults):

| Env var                                 | Default     | Unit     |
| --------------------------------------- | ----------- | -------- |
| `SCRAP_EVICTION_ENABLED`                | `false`     | bool     |
| `SCRAP_EVICTION_HOT_RESIDENCY_WINDOW`   | `24h`       | duration |
| `SCRAP_EVICTION_PLAN_TTL`               | `10m`       | duration |
| `SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS` | `10`        | count    |
| `SCRAP_EVICTION_RECOMMENDED_MAX_BYTES`  | `671088640` | bytes    |
| `SCRAP_EVICTION_MAX_VALIDATE_SAMPLES`   | `1`         | count    |

### Phase 3.6 Telemetry Evidence Plane

Phase 3.6 is a required evidence milestone before Phase 4. It replaces the
earlier Prometheus-only observability contract with an OpenTelemetry producer
contract for `scrapd`.

Required signals: low-cardinality metrics, structured logs, traces, and runtime
profiles. The local evidence stack is self-hosted Grafana with OTLP-capable
metrics, log, trace, and profile storage. A collector layer enriches Kubernetes
and S.C.R.A.P. resource attributes, batches exports, and tail-samples traces so
slow/error paths are retained under stress.

Logs remain structured `slog` JSON on stdout/stderr and are collected by the
telemetry pipeline; `scrapd` must not block request handling on log-export
backpressure. Go pprof is exposed only on the admin listener, disabled by
default, explicitly enabled for evidence environments, and network-restricted to
telemetry collectors and operators.

Telemetry must not use raw `transaction_id` or `document_name` as metric
attributes. Logs and traces carry stable hashed identifiers by default. Raw
identifiers are allowed only behind an explicit local debug override and must
not be enabled in production-like evidence runs.

Phase 4 cannot begin until throughput, mixed read/write/head, and upload-pressure
stress scenarios produce a timestamped evidence bundle containing run
configuration, load results, selected metric snapshots, log/trace/profile
exports or stable query references, and pass/fail checks. Alerting policy,
runbooks, and incident workflows are deferred beyond this milestone.

### Cluster Bootstrap

K8s-first auto-discovery. Members derive peer identity from StatefulSet primitives:

- `hostname()` → pod ordinal (e.g. `scrapd-2` → ordinal 2)
- Raft node ID = ordinal + 1 (Raft IDs are 1-based; ID 0 is reserved)
- Peer list = `scrapd-{0..N-1}.<headless_service>.<namespace>.svc:<peer_port>`
- Configuration via env vars: `SCRAP_REPLICAS`, `SCRAP_HEADLESS_SERVICE`,
  `SCRAP_CELL_ID`, `SCRAP_PEER_PORT`

Bootstrap: if WAL is empty (first boot) and ordinal is 0, bootstrap the Raft group
with all peers. If WAL is empty and ordinal > 0, wait for the bootstrap leader to add
this member. If WAL exists, resume from WAL state.

Fallback for local dev: `--peers` flag overrides K8s DNS discovery.

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

### Open Questions (deferred beyond current milestone)

- Cell federation model
- Multi-tier write ACK (priority classes)
- Encryption (OpenBao Transit integration)
- Group commit optimization
- Shard rebalancing and slot transfer protocol
