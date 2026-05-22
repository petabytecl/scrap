# Storage Gateway Design Notes

## Reader And Goal

This note is for an internal engineer resuming the storage gateway architecture discussion.

After reading it, they should be able to continue the design review from the current frontier: block format, metadata storage, shard replication, backend upload policy, and failure-domain planning.

Design status: exploratory. The settled points below are the current working direction, not a final ADR.

## Current Summary

S.C.R.A.P. is a transaction-scoped document storage gateway for billing ETL workflows.

The gateway stores immutable documents addressed by:

```text
tenant_id
transaction_id
document_name
```

Each transaction produces roughly 2 to 7 immutable documents. Documents may be ephemeral workflow artifacts or permanent definitive records retained for years. A document is invisible while it is being streamed. Once finalize succeeds, it is complete, checksummed, replicated, indexed, and immediately readable.

The service fleet talks to S.C.R.A.P. through a custom gRPC API. Object backends such as S3, GCS, Azure Blob, or filesystem storage are implementation details behind the gateway.

The physical storage abstraction is a unified immutable block format. Every document is stored as a byte range inside a block:

```text
document -> block_id + stored_offset + stored_length + checksums
```

Blocks are stored locally for the hot path and uploaded to the backend for durable long-term storage.

## Problem

The billing ETL platform has many microservices that need frequent access to documents such as XML and PDF files. The corpus may reach billions of relatively small files. During a workflow, services may read and write documents multiple times, but each document itself is immutable after creation.

The goal is to build a fast gateway that:

- serves metadata and bytes with low latency;
- supports streaming reads and writes;
- keeps hot documents close to the ETL fleet;
- asynchronously uploads permanent data to external object storage;
- reduces repeated object-backend reads and writes;
- supports cold storage management;
- avoids excessive backend object-count and prefix hot-spotting.

## Workload Facts

- Each `transaction_id` creates roughly 2 to 7 documents.
- Document names are transaction-local path-like names such as `bill.xml`, `accountants/payroll.xml`, or `sales/bill_1.pdf`.
- All documents are immutable. There are no document versions in the current model.
- p50 document size is small.
- p99 document size is medium.
- Maximum document size is 128 MiB.
- Metadata lookup target is single-digit milliseconds at p95.
- Small-document first byte target is single-digit milliseconds at p95 from the local hot tier.
- Small-document full read target is low tens of milliseconds at p95, depending on network conditions.

## Non-Negotiable Constraints

- File loss is catastrophic.
- Continued availability is required during multi-node loss.
- After a successful write acknowledgment, the gateway is the source of truth.
- For the service fleet, the gateway is the permanent storage interface.
- Backend upload may be asynchronous, but post-ack durability cannot be best-effort.
- `HeadDocument`, `ReadDocument`, and transaction-scoped `FindDocuments` must be strongly consistent after finalize.
- Global/admin listing, analytics scans, and lifecycle reports may be eventual or snapshot-based.

## API Shape

The primary API is custom gRPC. S3-compatible client behavior is not a v1 goal. Backend portability is a goal.

Core v1 API:

```text
WriteDocument(stream WriteDocumentRequest) -> WriteDocumentResult
HeadDocument(transaction_id, document_name) -> DocumentMetadata
ReadDocument(transaction_id, document_name, range?) -> stream ReadDocumentResponse
FindDocuments(transaction_id, filters) -> DocumentMetadata[]
CompleteTransaction(transaction_id) -> TransactionState
```

`WriteDocument` is a single client-streaming RPC in v1. The first message carries metadata; later messages carry bytes.

The write metadata includes:

```text
tenant_id
transaction_id
document_name
document_class = permanent | ephemeral
priority_class = critical_ingest | normal | bulk
content_type
expected_length?
expected_sha256?
client_idempotency_key?
workflow metadata/tags?
```

`tenant_id` and `priority_class` are supplied by internal callers, trusted for normal operation, and recorded immutably for audit. Optional guardrails can reject unknown tenants, unknown priorities, or emergency-downgrade a noisy service.

`ReadDocument` returns metadata in the first response message, followed by byte chunks:

```text
response #1:
  metadata
  selected range
  storage_source = local | peer | backend

response #2..N:
  bytes
```

Default API chunk size is 1 MiB. Allowed API chunk size range is 64 KiB to 4 MiB. API chunking is separate from block layout and crypto frame size.

Idempotency:

```text
critical_ingest: client_idempotency_key required
normal / bulk: client_idempotency_key optional but recommended
```

The idempotency key is scoped to the document write identity:

```text
tenant_id
transaction_id
document_name
client_idempotency_key
```

`client_idempotency_key` is caller supplied. `write_attempt_id` is internal. Finalized document idempotency metadata is retained with the document. Incomplete write attempt records are retained for 24 hours before cleanup.

## Document Identity

The public document identity is:

```text
(tenant_id, transaction_id, document_name)
```

`tenant_id` and `transaction_id` are caller-supplied opaque strings. S.C.R.A.P. validates them but does not parse, trim, case-fold, or Unicode-normalize them.

Internally, transaction identity is scoped by tenant:

```text
transaction_key = (tenant_id, transaction_id)
```

Upstream may guarantee globally unique `transaction_id` values, but S.C.R.A.P. should not depend on that for correctness.

`document_name` is a relative, transaction-local virtual path. It is not a POSIX path and does not create real directories.

Recommended validation:

- normalize separators to `/`;
- reject absolute paths;
- reject `..`;
- reject empty segments;
- reject duplicate slashes;
- reject leading or trailing slash;
- reject control characters;
- enforce maximum length.

If a service writes the same `(tenant_id, transaction_id, document_name)` twice:

- if the first write is finalized and bytes differ, return `409 Conflict`;
- if the content hash and compatible metadata match, treat it as an idempotent retry;
- if another write is in progress, reject or block with a short write lease;
- every write has an internal attempt/session ID.

## Internal Identifiers And Fingerprints

S.C.R.A.P. separates caller/domain identity from compact internal identity.

Caller/domain identity:

```text
tenant_id        # opaque caller string
transaction_id   # opaque caller string
document_name    # normalized transaction-local path string
```

Internal shard-local numeric IDs:

```text
transaction_key_id: uint64
document_key_id: uint64
```

These IDs are unique only within a shard. Full internal references are:

```text
(shard_id, transaction_key_id)
(shard_id, document_key_id)
```

`transaction_key_id` and `document_key_id` are allocated by monotonic counters in the shard consensus state machine.

Generated operational and physical IDs use UUIDv7:

```text
block_id
write_attempt_id
upload_job_id
repair_job_id
backend_location_id
```

UUIDv7 values are stored as raw 16-byte values and rendered as canonical UUID text for logs, APIs, and debugging. There is no central UUID generator. The shard leader generates `block_id` values because open blocks are leader-owned. The ingress gateway or shard leader may generate `write_attempt_id`; the leader records it. Upload and repair job IDs are generated by the component creating the job.

`shard_id` is a small numeric ID:

```text
shard_id: uint32
```

Block identity is composite:

```text
block_ref = (shard_id, block_id UUIDv7)
```

Do not pack shard bits into a custom block ID format in v1.

For compact lookup and routing, use deterministic fingerprints:

```text
tenant_fingerprint: bytes[16]
transaction_fingerprint: bytes[16]
document_name_fingerprint: bytes[16]
document_identity_fingerprint: bytes[16]
```

Fingerprint algorithm:

```text
BLAKE3-128
```

Fingerprint inputs use a domain label, format version, and length-prefixed fields. Do not use ambiguous string concatenation.

Conceptual examples:

```text
tenant_fingerprint =
  BLAKE3-128(domain="SCRAP:tenant:v1", fields=[tenant_id])

transaction_fingerprint =
  BLAKE3-128(domain="SCRAP:transaction:v1",
             fields=[tenant_fingerprint, transaction_id])

document_name_fingerprint =
  BLAKE3-128(domain="SCRAP:document-name:v1",
             fields=[normalized_document_name])

document_identity_fingerprint =
  BLAKE3-128(domain="SCRAP:document-identity:v1",
             fields=[transaction_fingerprint, document_name_fingerprint])
```

Fingerprints are stored as raw 16-byte arrays and displayed as lowercase hex. Treat them as byte arrays, not integers. Compare lexicographically on bytes if needed.

Original caller strings remain stored and are verified where correctness matters. Fingerprints are compact indexes and routing aids, not the only source of identity truth.

## Metadata And Query Scope

The gateway owns system and workflow metadata. It is not a business search engine in v1.

Core metadata includes:

```text
tenant_id
transaction_id
document_name
document_class
priority_class
content_type
byte_length
logical_sha256
stored_sha256
ciphertext_sha256?
created_at
created_by_service
workflow_stage?
tags?
storage_locations
lifecycle_state
```

`FindDocuments` is transaction-scoped and strongly consistent. It may filter by:

```text
document_name exact/prefix
document_class
content_type
workflow_stage
created_by_service
created_at range
tags
```

Arbitrary cross-transaction business search should live in a separate indexed system.

## Write Visibility

Documents are visible only after finalize succeeds.

Write flow:

```text
client streams document
gateway computes length and hashes
gateway stores bytes locally
gateway replicates bytes according to priority policy
gateway commits metadata through the shard consensus group
gateway publishes the document in the transaction index
gateway enqueues upload/lifecycle work
ACK
```

Partial writes are never visible to readers. If streaming fails before finalize, no valid document exists.

## Sharding And Routing

Shard by `transaction_id`.

All documents for a transaction live in the same shard group. This keeps `FindDocuments(transaction_id, filters)`, lifecycle decisions, and per-transaction accounting local to one shard.

Any gateway may accept any API request. Internally:

- writes are routed to the shard leader early;
- the leader coordinates byte replication and metadata commit;
- reads may be served by any shard member with safe metadata and committed local bytes;
- non-member gateways forward internally to an appropriate shard member.

Every gateway keeps a small routing index. Shard members keep hot in-memory document indexes for their owned or replicated shards. Do not keep a full global billion-document index on every gateway.

## Replication And Availability

Each shard group has five voting replicas as the baseline. This targets continued availability during two node failures, assuming placement across independent failure domains.

Consensus, such as Raft, stores metadata, ownership, lifecycle state, upload journal entries, and commit state. Document bytes are replicated through a separate data path. Bytes should not be embedded in the consensus log.

Commit protocol:

```text
writer/leader stores bytes locally
leader replicates bytes to target peers
peers fsync and checksum prepared bytes
leader commits metadata through consensus
replicas mark bytes committed/readable
ACK to client
```

Write durability policy:

```text
critical_ingest:
  ACK after bytes are durable on all 5 replicas
  metadata committed by consensus quorum

normal / bulk:
  ACK after bytes are durable on quorum, usually 3 of 5
  metadata committed by consensus quorum
  repair to all 5 replicas immediately
```

Repair target for quorum-written documents:

```text
target full replication: under 10 seconds
hard alert: over 60 seconds
throttle writes if repair backlog exceeds safety threshold
```

Verified backend storage counts for durability. Only committed local replicas count for hot read availability.

## Read Path

Reads prefer local hot storage.

Read order:

```text
lookup shard route
lookup committed metadata in memory
if local committed physical ref exists:
  stream local block range
else if peer committed physical ref exists:
  stream peer block range
else:
  stream from backend block range
```

Metadata may be served by the leader or by lease-safe/read-index-valid followers. Bytes may be served by any committed checksum-valid replica.

Priority does not affect read scheduling in the current model. Priority is write-side only.

## Transaction Lifecycle

Transaction state is used for lifecycle, cleanup, compaction, upload urgency, quotas, and reporting. It does not control document visibility.

Transaction lifecycle:

```text
active
  -> completed_explicit
  -> inactive_by_policy
  -> expired
```

Completion is both explicit and policy-driven:

- ETL orchestration may call `CompleteTransaction(transaction_id)`;
- fallback inactivity timeout is 24 hours after the last finalized document.

Document lifecycle:

```text
writing
  -> finalized/readable
  -> uploaded
  -> compacted
  -> local_cache_only
  -> evicted_locally
  -> expired/tombstoned
```

Document class default is `permanent`. Ephemeral must be explicit or policy-derived.

Classification priority:

```text
explicit writer metadata
  > transaction/workflow policy
  > document_name prefix policy
  > gateway default permanent
```

Ephemeral retention default is 7 days after explicit completion or inactivity-by-policy.

Normal services do not delete documents directly. Deletion is lifecycle/admin-driven, represented by tombstone metadata, and byte reclamation is asynchronous after retention and reconciliation rules pass.

## Block Storage Model

The physical storage unit is a block.

Every document is stored as a byte range inside a block:

```text
block_id
stored_offset
stored_length
logical_length
logical_sha256
stored_sha256
```

Blocks are scoped by:

```text
shard_id
lifecycle_class = permanent | ephemeral
size_class = small | medium
```

Size classes:

```text
small: <= 1 MiB
medium: > 1 MiB and <= 128 MiB
reject: > 128 MiB
```

Block sizing:

```text
target block size: 256 MiB
maximum block size: 512 MiB
maximum open age: 30-60 seconds
```

Blocks are appendable only while open. Open blocks are local to the shard leader. A block becomes immutable when sealed.

Documents inside an open block can be finalized and read immediately. The block itself becomes eligible for backend upload after it is sealed.

Blocks are always framed, even when encryption is disabled:

```text
default frame size: 1 MiB
optional tuning size: 4 MiB
```

Frame boundaries are over the stored plaintext block byte stream before encryption. Encryption, when enabled, produces per-frame ciphertext offsets and lengths. Document stored offsets remain independent of encryption mode.

Documents do not need to align to frame boundaries. Many small documents may share a frame, and medium documents may span many frames.

The `.blk` file includes a tiny fixed header:

```text
offset  size  field
0       8     magic = "SCRAPBLK"
8       2     format_major u16
10      2     format_minor u16
12      4     header_length u32 = 64
16      16    block_id UUIDv7 bytes
32      4     shard_id u32
36      4     flags u32
40      4     frame_size u32
44      4     reserved
48      16    reserved
```

The `.blk` header is fixed at 64 bytes. Frame payload bytes follow the header with no per-frame mini-header:

```text
.blk = 64-byte header + raw frame payloads
```

The document table, frame table, encryption metadata, and checksums live in `.idx`.

All block and index binary formats use:

```text
endianness: little-endian
integer sizes: explicit u8/u16/u32/u64
timestamps: unix epoch milliseconds as u64
UUIDv7: raw bytes[16]
fingerprints: raw bytes[16]
SHA-256: raw bytes[32]
```

Block IDs use a generated UUIDv7 plus a separate `shard_id`; content hashes verify integrity but are not the primary block name.

Replica-local layouts are allowed. A document may live at different block offsets on different replicas:

```text
replica A: block_a offset 100 length 42
replica B: block_b offset 900 length 42
replica C: block_c offset 12 length 42
```

The logical document record is cluster-wide. Physical refs are replica-specific.

## Local Block Files

Local hot storage uses `.blk` plus `.idx`.

Open local block:

```text
<block_id>.blk
<block_id>.openlog
```

Sealed local block:

```text
<block_id>.blk
<block_id>.idx
metadata DB entries
in-memory index entries
```

The hot read path uses the in-memory index and local metadata store. The local `.idx` exists for recovery, scrubbing, and repair.

`.idx` is optimized for restart, recovery, and scrubbing, not normal hot reads. Normal hot reads use the in-memory index and local metadata store.

V1 `.idx` is intentionally simple and scan-friendly. It does not need a document hash table, B-tree, or direct lookup structure yet.

Recommended v1 `.idx` structure:

```text
fixed 128-byte header
frame records[]
document records[]
metadata blobs[]
fixed 64-byte footer
```

The `.idx` fixed header:

```text
offset  size  field
0       8     magic = "SCRAPIDX"
8       2     format_major u16
10      2     format_minor u16
12      4     header_length u32 = 128
16      16    block_id UUIDv7 bytes
32      4     shard_id u32
36      4     flags u32
40      4     frame_size u32
44      4     frame_count u32
48      8     block_payload_length u64
56      32    block_payload_sha256 bytes[32]
88      8     frame_table_offset u64
96      8     document_table_offset u64
104     8     metadata_blob_offset u64
112     8     footer_offset u64
120     2     frame_record_size u16 = 96
122     2     document_record_size u16 = 224
124     2     footer_size u16 = 64
126     2     reserved
```

The header should also allow exact section-boundary validation. If the fields above are too tight during implementation, add a format-minor bump and include explicit section lengths:

```text
frame_table_length
document_table_length
metadata_blob_length
```

The `.idx` repeats and verifies `.blk` identity:

```text
block_id
format_version
block_payload_length
block_checksum
```

Checksums should exist at three levels:

```text
document checksums
frame checksums
whole-block checksum
```

Frame records are fixed-width and sorted by `frame_index` ascending.

`FrameRecord` is 96 bytes:

```text
offset  size  field
0       4     frame_index u32
4       4     flags u32
8       8     plaintext_offset u64
16      4     plaintext_length u32
20      4     ciphertext_length u32
24      8     ciphertext_offset u64
32      4     compression_mode u32    # reserved/zero in v1
36      4     encryption_mode u32
40      12    nonce bytes[12]
52      16    auth_tag bytes[16]
68      4     crc32c u32
72      24    reserved
```

For unencrypted blocks:

```text
ciphertext_offset = plaintext_offset
ciphertext_length = plaintext_length
encryption_mode = 0
nonce/auth_tag zeroed
```

Document records are fixed-width and sorted by `document_key_id` ascending. Variable strings and workflow metadata live in mandatory per-document Protobuf metadata blobs.

`DocumentRecord` is 224 bytes:

```text
offset  size  field
0       8     document_key_id u64
8       8     transaction_key_id u64
16      16    document_name_fingerprint bytes[16]
32      16    document_identity_fingerprint bytes[16]
48      8     stored_offset u64
56      8     stored_length u64
64      8     logical_length u64
72      32    logical_sha256 bytes[32]
104     32    stored_sha256 bytes[32]
136     4     content_type_id u32
140     2     document_class u16
142     2     priority_class u16
144     8     created_at_ms u64
152     8     metadata_blob_offset u64
160     4     metadata_blob_length u32
164     4     flags u32
168     16    transaction_fingerprint bytes[16]
184     4     first_frame_index u32
188     4     last_frame_index u32
192     32    reserved
```

Every `DocumentRecord` must point to a mandatory `DocumentMetadataBlob`:

```text
metadata_blob_offset != 0
metadata_blob_length > 0
```

Metadata blobs are length-prefixed Protobuf messages with optional per-blob CRC32C. Document metadata blobs preserve authoritative caller strings and variable metadata:

```text
tenant_id
transaction_id
document_name
content_type
workflow_stage
tags
client_idempotency_key
created_by_service
```

Block-level metadata blobs are optional and can carry encryption metadata, future compression dictionary information, or other block-scoped extension data.

The `.idx` footer is fixed at 64 bytes:

```text
offset  size  field
0       8     magic = "SCRAPEND"
8       8     idx_file_length u64
16      32    idx_sha256_without_footer_hash_field bytes[32]
48      8     created_at_ms u64
56      8     reserved
```

Recovery verifies:

```text
header.footer_offset points to footer
footer.idx_file_length == actual file length
idx hash matches
```

`.idx` is written only when the block seals. While the block is open, `.openlog` records append progress and finalized documents.

After block seal, retain `.openlog` for 24 hours by default for debugging. It may be deleted earlier under disk pressure after `.idx` verification succeeds.

`.openlog` records:

```text
BlockOpened
DocumentPrepared
DocumentFinalized
BlockSealStarted
BlockSealed
```

For each finalized document, `.openlog` records at least:

```text
tenant_id
transaction_id
document_name
stored_offset
stored_length
logical_length
logical_sha256
stored_sha256
content_type
document_class
priority_class
created_at
writer_attempt_id
client_idempotency_key?
```

`.openlog` uses length-prefixed Protobuf records with a CRC32C per record. Recovery replays it sequentially and stops cleanly at the last complete valid record.

The `.openlog` has a fixed 64-byte header and no footer. It is an append-only crash-cut log, valid up to the last complete CRC-verified record.

Before ACK, the local durable copy must have:

```text
durable .blk bytes
durable .openlog finalized/prepared record
required byte replicas durable
metadata committed
```

If the metadata DB is lost but block files remain:

1. consensus snapshot/log restores authoritative committed metadata;
2. local `.idx` files prove which physical byte ranges exist locally;
3. checksums verify block/document bytes;
4. gateway rebuilds local readable physical refs.

## Backend Block Storage

The backend stores sealed blocks. V1 uses two backend objects per block:

```text
<block_id>.blk
<block_id>.idx
```

The leader's sealed block is the canonical backend block in v1:

```text
leader local block sealed
verify block checksum
upload block and index to backend
commit backend block location
documents now have backend refs: block_id + stored_offset + stored_length
```

If the leader fails before upload, recovery may upload another replica's sealed block or rebuild a canonical backend block from committed documents.

Reads prefer local replica physical refs, then peer refs, then backend canonical block refs.

## Backend Abstraction

The internal model is backend-portable, not S3-specific.

Backend interface:

```text
put_object(key, stream, metadata, checksum)
get_object_range(key, offset, length)
head_object(key)
delete_object(key)
list_prefix?     # operational only, not hot path
```

Initial deployment uses one primary durable backend. Metadata should support multiple backend locations later for disaster recovery or cross-cloud replication.

Backend selection is deployment-level configuration. Tenant-based backend routing was considered and rejected as too complex for an edge case. S.C.R.A.P. should not route durable backend writes to different backend pools by `tenant_id` in v1.

`tenant_id` remains part of document identity, auditing, quotas, and fingerprints. It does not choose the durable backend.

Never depend on backend `LIST` for normal reads or transaction queries. The gateway metadata index is the source of truth.

## Backend Key Layout And Rate Control

Application virtual paths must not be used directly as backend key prefixes. Backend keys need high-cardinality physical prefixes to avoid hot partitions.

Example physical layout:

```text
blocks/v1/p=<hash-prefix>/shard=<shard_id>/<block_id>.blk
blocks/v1/p=<hash-prefix>/shard=<shard_id>/<block_id>.idx
```

The uploader is a first-class subsystem:

```text
partitioned durable queues
per-backend concurrency control
per-physical-prefix token buckets
adaptive retry/backoff on backend throttling
burst smoothing
priority lanes
backlog metrics and alerts
```

Upload lanes:

```text
permanent-block-upload
ephemeral-spill-if-policy-requires
repair/reconciliation
disaster-recovery-secondary
```

Under local disk pressure:

1. delete only expired/eligible ephemeral data;
2. throttle by tenant/workload using fair-share policy;
3. reject new writes with retryable errors before durability becomes unsafe;
4. never acknowledge a write that cannot be durably protected.

## Permanent And Ephemeral Backend Policy

Permanent documents are uploaded to the configured backend after local durable commit and block seal.

Ephemeral documents default to local replicated storage only. They are not uploaded to the backend unless policy allows spill during disk pressure or long retention.

Backend upload state may reduce durability repair urgency, but it does not replace local replicas for low-latency active workflow reads.

## Encryption And Compression

Backend encryption is policy-driven. It is primarily important for external object storage such as S3. Local hot storage may remain application-plaintext by default, relying on node/disk/Kubernetes controls.

Recommended encryption model:

- use OpenBao Transit for envelope encryption;
- do not send whole blocks to Transit for encryption;
- request a data key from Transit;
- encrypt block data locally;
- store the encrypted data key and encryption metadata in block metadata;
- discard plaintext keys after use, subject to a bounded in-memory cache.

Efficient backend range reads require framed block encryption.

Recommended encrypted block shape:

```text
block header / index:
  block_id
  encryption_mode
  frame_size
  frame_count
  encrypted_data_key
  algorithm
  frame table

frame:
  plaintext_offset
  plaintext_length
  ciphertext_offset
  ciphertext_length
  nonce
  auth tag / checksum
```

Default crypto frame size is 1 MiB, with 4 MiB as a possible tuning option.

Encryption is transparent to clients. Normal reads return original logical document bytes.

Compression default is none. Optional per-document compression may be used for compressible content such as XML. Compression happens before storage/encryption. Avoid whole-block or whole-frame compression in v1 because it complicates efficient range reads.

Hashes:

```text
logical_sha256: original client-visible bytes
stored_sha256: bytes stored inside the block before backend encryption
ciphertext_sha256: encrypted backend bytes when applicable
```

## Open Questions

These are the known gray areas to resolve before turning this into an ADR or implementation plan.

- What are the exact failure domains for replica placement: nodes, racks, zones, regions, or Kubernetes node pools?
- Which consensus implementation should back shard metadata?
- Which local durable metadata store should back each shard member?
- What are the block format versioning rules and compatibility guarantees?
- What is the OpenBao key hierarchy, cache TTL, rotation, and outage behavior?
- What are the exact backend key prefix counts and token-bucket limits per provider?
- How much local disk is required for the worst multinational bulk burst plus backend slowdown?
- What are the write admission thresholds for disk pressure and repair backlog?
- What are the scrubbing, checksum verification, and repair schedules?
- How should shard movement, resharding, and replica replacement work?
- What is the admin API for transaction listing, incomplete uploads, and lifecycle reporting?
- What authorization model protects internal APIs, even if caller metadata is trusted?
- Should there be a dedicated disaster-recovery backend in v1 or only schema support for it?

## Next Discussion Frontier

The block and `.idx` schema shape is now concrete enough to move to write-path semantics.

Resume by choosing between:

1. write prepare/finalize/retry state machine;
2. shard metadata store and consensus library;
3. repair state machine;
4. backend uploader queue/rate-control design.

The recommended next topic is the write state machine. The storage format is now concrete enough to define commit, recovery, retry, and repair states before selecting a metadata engine.
