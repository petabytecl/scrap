# Storage Gateway Design Notes

## Reader And Goal

This note is for an internal engineer resuming the storage gateway architecture discussion.

After reading it, they should be able to continue the design review from the current frontier: business-specific DR targets, stale-import availability policy, compliance retention, and implementation ADR boundaries.

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

Shard consensus metadata is the authority for document visibility, physical refs, encryption envelopes, restore state, and background jobs. Local in-memory indexes are derived read accelerators.

Backend encryption uses OpenBao Transit envelope encryption. Routine KEK rotation rewraps small per-block envelopes; it does not re-encrypt block payload data.

Backend upload capacity is controlled by explicit provider profiles, local disk runway, and fail-closed production validation. Internal authorization is based on workload identity plus service capabilities, not caller-supplied metadata.

V1 replica placement targets distinct Kubernetes storage nodes. Admin operations are typed, audited, idempotent control-plane jobs exposed through a gRPC admin service and CLI.

Bit rot is treated as a first-class durability threat. Block, index, and envelope formats are long-lived compatibility contracts because immutable data may remain archived for years.

V1 disaster recovery targets rebuild from the primary backend plus metadata recovery artifacts. Always-on secondary backend writes are not part of the v1 durability contract.

V1 includes a cell model for federated read cache deployments. A cell is a S.C.R.A.P. deployment that owns a disjoint `source_namespace` for writes. Other cells may import published metadata snapshots/tails read-only and serve bounded-staleness cached reads, but they do not join the source cell's shard consensus or write authoritative metadata.

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

## Write State Machine

`WriteDocument` is coordinated by the shard leader. The ingress gateway routes the stream to the leader early so the leader owns block append, peer prepare, metadata commit, and upload intent creation.

V1 write-attempt states:

```text
RECEIVING
LOCAL_PREPARED
PEERS_PREPARED
COMMITTING
COMMITTED
ABORTED
```

Normal sequence:

```text
client streams bytes
leader appends bytes to open .blk
leader fsyncs .blk
leader writes and fsyncs .openlog DocumentPrepared
leader prepares required peer replicas
peers fsync bytes and verify checksums
leader commits document metadata through consensus
document becomes readable
leader records backend upload intent in the consensus outbox
client receives success
```

Prepared bytes are durable but unreadable. Consensus metadata is the source of truth for document visibility after crashes. `.openlog` proves local byte durability and supports recovery, but it does not make a document visible by itself.

`.openlog` commit semantics:

```text
DocumentPrepared:
  durable local bytes exist
  document is still invisible

DocumentFinalized:
  optional post-commit trace/debug record
  not required before ACK
```

ACK does not require fsyncing a post-commit `DocumentFinalized` record. Once consensus commit succeeds, the write is successful. If local post-commit bookkeeping fails after consensus commit, return success and repair local bookkeeping from committed metadata plus prepared bytes.

Consensus document commit records should include:

```text
document metadata
prepared replica physical refs
desired replica count
achieved replica count
repair requirement if under full replication
backend upload-required outbox entry
```

Upload intent is durable through the shard consensus outbox, not only a local queue. Later repair completions add or remove replica refs through consensus so read routing sees a consistent committed view.

Client-visible write outcomes:

```text
active duplicate attempt:
  gRPC ABORTED, retryable

same identity/idempotency/hash already finalized:
  OK with existing committed metadata

same identity but different bytes or incompatible metadata:
  ALREADY_EXISTS
```

Duplicate active writes return in-progress/retryable behavior rather than attaching to the active stream in v1.

Timeout and cleanup defaults:

```text
write stream idle timeout: 60 seconds
write stream total timeout: 15 minutes
aborted/prepared-but-uncommitted cleanup TTL: 24 hours
```

Cleanup must confirm no committed metadata references the prepared bytes before deleting local block ranges or attempt records.

## Sharding And Routing

Shard by transaction key:

```text
tenant_id + transaction_id
```

All documents for one tenant-scoped transaction live in the same shard group. This keeps `FindDocuments(transaction_id, filters)`, lifecycle decisions, and per-transaction accounting local to one shard.

Any gateway may accept any API request. Internally:

- writes are routed to the shard leader early;
- the leader coordinates byte replication and metadata commit;
- reads may be served by any shard member with safe metadata and committed local bytes;
- non-member gateways forward internally to an appropriate shard member.

Every gateway keeps a small routing index. Shard members keep hot in-memory document indexes for their owned or replicated shards. Do not keep a full global billion-document index on every gateway.

## Cells And Federated Read Cache

Do not call a Kubernetes deployment a shard. In this document:

```text
shard = transaction-keyed Raft group inside one S.C.R.A.P. deployment
cell  = S.C.R.A.P. deployment/private Kubernetes deployment that owns writes for a source namespace
```

The cell model is part of core v1 scope. It is not a later optional extension, because bulk read-cache deployments and private Kubernetes locations affect document visibility, source ownership, cache invalidation, crypto permissions, and operator runbooks.

Cell ownership model:

```text
public document identity:
  tenant_id + transaction_id + document_name

write ownership:
  source_namespace -> authoritative cell

backend physical prefix:
  cells/<cell_id>/...
```

`source_namespace` is ownership metadata. It is not part of public document identity and must not leak into normal caller keys. Two authoritative cells must not own the same `source_namespace` at the same time.

Simple topology:

```text
              published metadata snapshots/tails
        +---------------------------------------------+
        |                                             v
+-------------------+       shared backend     +-------------------+
| Cell A            |  .blk/.idx/.env objects  | Cell B            |
| authoritative     +------------------------->| read-only import  |
| source_namespace=a|                         | cache/catalog     |
+-------------------+                         +-------------------+
        ^                                             |
        | local strong reads/writes                   | bounded-staleness reads
        |                                             v
   ETL writers/readers                          bulk readers
```

Authoritative cells publish verified metadata snapshots and compact metadata tails. Importing cells must build local read-only catalogs from those published metadata artifacts, not by scanning backend buckets or discovering blocks with `LIST`.

Read order inside a cell:

```text
1. local authoritative shard metadata
2. imported read-only catalogs
3. structured NOT_FOUND / restore-pending / stale-import response
```

If the same public document identity exists in local authoritative metadata and an imported catalog, the read must fail closed as a conflict and alert. S.C.R.A.P. must not guess which cell is correct.

Imported read consistency is bounded by the imported source metadata version:

```text
imported read sees: source metadata version N
not guaranteed: source writes after version N
```

Imported read misses return a versioned response that includes the source, imported version, and lag information when available. A cache cell must not search backend blocks directly to answer a miss.

Imported metadata is stored in a separate read-only keyspace/catalog. It is not merged into local authoritative shard consensus state. This keeps the source of truth clear:

```text
authoritative metadata: local shard consensus
imported metadata: verified source snapshot/tail projection
local cache bytes: rebuildable acceleration
```

Read-only cache cells use a cache profile:

```text
storage nodes: 1 or more rebuildable cache nodes
writer SLA: none
local writes: cache/catalog/index only
promotion to writer: not allowed in this mode
```

A read-only cache cell cannot become an authoritative writer by accident. DR takeover or failover is a separate workflow that requires source fencing, ownership transfer, metadata validation, and explicit operator action.

Cache invalidation follows source metadata. If imported source metadata declares a tombstone, reclaim, block supersession, or ref invalidation, the importing cell must evict or quarantine matching local cache bytes before serving from them.

Encrypted imports require read-only decrypt/unwrap grants for the source cell's envelopes. They must not require encrypt, rewrap, key-admin, or source write privileges.

V1 source namespace configuration:

```text
cell_id: stable operator-assigned backend/logging identifier
mode: authoritative | read_only_cache
source_namespace: stable operator-assigned ownership namespace
default source_namespace: cell_id
authoritative namespaces per cell in v1: exactly one
read-only imported namespaces per cache cell: many
```

`cell_id` and `source_namespace` should be lowercase ASCII slugs safe for backend keys and operations tooling. They are configuration identities, not runtime-generated UUIDs. Changing either is a migration event.

There is no global runtime source-namespace registry in v1. Cross-cell uniqueness is an infrastructure and deployment contract. S.C.R.A.P. validates local config, records source identity in published metadata, and fails closed on observed conflicts.

Authority movement is out of band. To move a `source_namespace` to another cell:

1. fence or stop writes in the old authoritative cell;
2. copy/verify backend data and published metadata as needed;
3. deploy the new cell with the same `source_namespace` and intended backend root;
4. validate recovered metadata and byte refs;
5. explicitly enable writes in the new cell.

Do not make authority movement an automatic background behavior in v1.

## Published Metadata And Imported Catalogs

Authoritative cells publish metadata artifacts for two consumers:

```text
DR restore tooling
read-only cache cells
```

The published format is provider-neutral and independent of local Pebble/Raft internals. Artifacts use versioned length-prefixed Protobuf records with per-record CRC32C and whole-object SHA-256 recorded in the manifest. Do not publish JSON as the canonical metadata format.

Default metadata artifact layout:

```text
cells/<cell_id>/metadata/v1/current.pointer
cells/<cell_id>/metadata/v1/manifests/<manifest_id>.manifest
cells/<cell_id>/metadata/v1/snapshots/shard=<shard_id>/<snapshot_id>.snap
cells/<cell_id>/metadata/v1/tails/shard=<shard_id>/<from_index>-<to_index>.tail
```

`current.pointer` is the only mutable well-known object. It contains the latest manifest id, manifest checksum, source namespace, generation, and published timestamp. Importers poll `current.pointer`; they do not use backend `LIST` to discover current metadata.

Publish protocol:

```text
write immutable snapshot/tail artifacts
verify artifact length and checksum
write immutable manifest that references only verified artifacts
verify manifest length and checksum
update current.pointer last
```

If pointer update fails, older importers continue using the previous complete manifest. If pointer update succeeds but a referenced artifact is unavailable or fails checksum, importers reject that manifest and keep the last valid imported version.

Manifest contents:

```text
format version
cell_id
source_namespace
manifest_id and generation
published_at
per-shard high watermarks
snapshot artifact refs and checksums
tail artifact refs and checksums
required block/index/envelope object refs
tombstone/reclaim watermarks
OpenBao Transit key/context metadata needed for decrypt checks
producer build/schema versions
```

Snapshot contents:

```text
complete committed shard metadata at a high watermark
transactions and documents
physical block/index/envelope refs
restore/tombstone/reclaim state
checksums and format versions
no document bytes
no hot in-memory indexes
```

Tail contents:

```text
idempotent committed metadata mutations after a snapshot
monotonic shard log/index range
document finalize records
block/backend/envelope state changes
tombstone/reclaim records
repair-visible physical-ref changes
```

Default publication cadence:

```text
metadata snapshot target: every 5 minutes
metadata tail target: every 30 seconds
metadata tail size cap: 64 MiB before forced roll
```

Read-only import rules:

```text
install only checksum-valid manifests
apply snapshot, then complete ordered tails
store imported state in separate read-only keyspaces
never merge imported state into authoritative shard consensus
mark source degraded if an import gap cannot be filled
```

Default import lag SLOs under healthy backend/source conditions:

```text
target: p95 imported metadata lag <= 60 seconds
warning: lag > 5 minutes
degraded: lag > 15 minutes
serve-imported-reads default: refuse when lag > 15 minutes
```

The conservative v1 default is to fail closed when imported metadata is beyond the serve lag. Known immutable bytes might still be correct, but stale metadata can miss tombstones, reclaim decisions, or policy changes.

Imported catalog sizing:

```text
metadata_catalog_bytes: explicit profile budget
byte_cache_bytes: explicit profile budget
catalog completeness: all-or-source-degraded
byte cache: rebuildable and evictable
```

If a cache cell cannot fit the configured source catalog, it marks that source degraded and does not expose a partial catalog as complete. Byte cache pressure may evict cached blocks/ranges, but it must not corrupt catalog completeness.

Read-only cache byte admission:

```text
cache key: source_namespace + source_version + block_id + range/frame
verify source checksum before serving or admitting
prefer prewarm/pinned bulk jobs within TTL and budget
use scan-resistant admission for opportunistic read-through
evict on source tombstone/reclaim/ref invalidation
```

Read-only cache cells do not initiate source authority operations by default. They do not rewrap source envelopes, mutate source metadata, or issue source-side lifecycle/restore changes. If a needed backend object is archived, the read-only cache returns restore-pending/stale-source detail rather than pretending it can make the source warm.

Conflict handling:

```text
same public document identity in local authoritative metadata and imported catalog: fail closed
same public document identity from two imported sources: fail closed
same source_namespace from two source cells: fail closed
```

Conflict alerts are high severity. Operators inspect the conflicting source manifests and either correct import config, fence one source, or run an explicit recovery/migration workflow. S.C.R.A.P. must not pick a winner automatically.

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
  target bytes durable on all 5 replicas before ACK
  after 2s prepare deadline, may downgrade to quorum ACK
  metadata committed by consensus quorum
  degraded quorum ACK is tracked in logs/metrics/traces, not persisted per document
  degraded repair target: under 2 seconds
  degraded repair alert: over 10 seconds

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

Once metadata is committed, reads may use any checksum-valid committed replica even if full replication repair is still pending.

## Replica Placement And Failure Domains

V1 failure domain target:

```text
failure domain: Kubernetes worker node
formal promise: survive loss of any two Kubernetes worker nodes that host shard replicas
not promised in v1: zone, region, rack, or cloud-provider failure-domain survival
```

Each shard group has five voting replicas on five distinct eligible storage nodes. This is a hard placement requirement for production. If five distinct eligible nodes are not available, the replica stays pending or the shard is reported placement-unhealthy rather than silently weakening the two-node-loss model.

Zone spread is preferred when topology labels exist, but it is not required for v1 correctness. Zone placement should avoid concentrating all five voters in one zone when the cluster can do better.

Eligible nodes:

```text
preferred dedicated storage node pool
labeled and tainted for S.C.R.A.P. storage members
fast local disks with expected capacity and durability
predictable network and operational treatment
```

Dedicated storage nodes are the normal production profile, but they are not a hard product assumption. A deployment may intentionally relax onto a shared node pool only through an explicit risk mode. Shared-pool risk mode must warn loudly and must be excluded from strict production SLO claims.

Production minimum:

```text
minimum eligible storage nodes: 7
reason: 5 active voters + replacement headroom + maintenance/failure headroom
```

If fewer than seven eligible storage nodes are available in production mode, the deployment is placement-unhealthy. It must not silently weaken the failure model.

Kubernetes pod model:

```text
single storage-member StatefulSet
stable stateful storage-member pods
local PV/PVC per storage member
many shard replicas assigned internally to each member
Kubernetes places storage members
S.C.R.A.P. manages shard replica membership and byte safety
```

V1 does not use a separate stateless ingress tier. Storage-member pods expose the service API and forward internally when the local member is not the right shard leader or safe read replica.

V1 also does not use one pod per shard replica. That would make pod count and scheduling churn scale with shard count rather than storage-member count.

Kubernetes scheduling guardrails:

```text
hard pod anti-affinity: one storage-member pod per Kubernetes worker node
hard node anti-affinity for voting replicas of the same shard
preferred topology spread across zones
PodDisruptionBudget: maxUnavailable = 1 for storage members
```

PDBs only protect voluntary disruption. They do not replace S.C.R.A.P.'s own placement health, membership, and byte-verification rules.

Shard membership changes use Raft joint consensus. Kubernetes may add capacity, but only the shard consensus state machine decides when a member is added or removed from a shard group.

Replacement safety:

```text
new replica catches up Raft metadata
new replica verifies required local byte refs/checksums
new replica becomes read-eligible
old replica may then be removed through joint consensus
```

Do not remove an old replica only because a new pod started or caught up metadata. Metadata catch-up without verified bytes weakens hot-read availability and local durability.

Returning member policy:

```text
returning member starts non-serving
catch up Raft metadata
verify local refs and checksums
serve only verified refs
repair or discard stale/corrupt local data after safe replacement exists
```

Pod readiness is not storage readiness. A storage member is not byte-serving until S.C.R.A.P. verifies that its local refs match committed metadata.

Scaling and rebalancing:

```text
storage-member scaling: manual/operator-controlled
automatic actions: replacement and capacity rebalance inside available pool
deferred from v1: automatic hot-shard splitting
```

S.C.R.A.P. may move shard replicas off failed, full, draining, or skewed members to restore placement health and disk balance. Shard-key semantics remain stable.

Planned maintenance uses S.C.R.A.P.-aware drain:

```text
operator marks storage member draining
dry-run reports affected shards, bytes, and estimated movement
S.C.R.A.P. moves/re-replicates shard replicas safely
member reports safe-to-evict only after affected shards are healthy elsewhere
Kubernetes drain proceeds after S.C.R.A.P. safety gate
```

Placement health gate:

```text
5 voters on 5 distinct eligible nodes
Raft quorum healthy
local byte refs verified
repair backlog below configured threshold
no unsafe drain/replacement in progress
```

Raft quorum alone is not placement health. Pod readiness alone is not placement health.

If a shard is under-replicated or placement-unhealthy but still has quorum, writes may continue only with guardrails:

```text
allow writes while quorum and durability policy can be satisfied
alert placement risk
accelerate repair/rebalance
throttle lower-priority writes if backlog or placement risk grows
reject retryably before ACK safety is compromised
```

## Kubernetes Deployment Topology And Operations

Authoritative v1 deployment shape:

```text
one storage-member StatefulSet
one durable data PVC per storage member
client-facing Service for public gRPC traffic
headless member Service for stable peer discovery
separate logical ports for client, admin, and peer traffic
NetworkPolicies around admin and peer ports
```

Storage-member pods are both gateways and storage members. Any member can accept a client request, then serve it locally or forward it internally to the shard leader or a safe read replica.

Durable paths:

```text
metadata KV / WAL / Raft state: durable data PVC
local block files and indexes: durable data PVC
temporary write/seal files that affect recovery: durable data PVC
pure scratch that can be discarded safely: optional non-durable scratch
```

Do not use `emptyDir` for prepared bytes, open block state, metadata WALs, or any temporary file needed to recover an acknowledged or potentially committed write.

Kubernetes readiness is routability, not storage safety. S.C.R.A.P. exposes detailed storage conditions separately:

```text
process_ready
routable
byte_serving
placement_healthy
safe_to_evict
disk_pressure
repair_lag
```

Liveness should catch a dead process or hard deadlock. It must not restart a pod merely because a shard is degraded, a backend is slow, or repair is lagging.

Rollout and drain:

```text
StatefulSet rollout: ordered and gated by S.C.R.A.P. safety
planned drain: plan -> start -> wait for safe_to_evict -> Kubernetes drain
PDB: maxUnavailable = 1
```

The drain plan should report affected shards, byte movement, expected catch-up work, and blockers. Kubernetes should act after S.C.R.A.P. reports `safe_to_evict`; PDBs are only a coarse guardrail.

Bootstrap and replacement:

```text
cluster bootstrap: explicit cluster-init/admin operation
member identity: persistent member ID on the data PVC
pod ordinal: scheduling identity only, not storage identity
permanent node/PV loss: operator-declared lost workflow
replacement: catch up metadata, verify bytes, then joint-consensus membership change
```

A pod restart or ordinal reuse must not create a new storage identity by accident. If a data PVC is gone, the operator declares the member lost and S.C.R.A.P. performs safe replica replacement.

Admin exposure:

```text
admin API: cluster-internal
authentication: mTLS/workload identity
authorization: capability policy
auditing: mandatory for dangerous operations
```

Core runbooks required before production:

```text
bootstrap cluster
scale out storage members
planned drain / maintenance
node or PV declared lost
rolling upgrade
disk pressure
readiness / byte-serving debugging
```

Core alerts:

```text
placement-unhealthy
unsafe drain
node/PV loss
PVC missing or identity mismatch
member not byte-serving
quorum risk
disk pressure
repair lag above threshold
```

Dev topology mode:

```text
single-node / multi-member dev: allowed only in explicit unsafe dev mode
production SLO claims: never
```

## Shard Consensus And Metadata Store

One shard group is one consensus group. The shard consensus state machine owns:

```text
transactions
documents
committed physical refs
block lifecycle state
backend locations
active encryption envelopes
restore state
upload / repair / restore / rewrap outboxes
replica membership
```

The consensus log carries deterministic metadata commands only. It must not carry document bytes, full `.idx` payloads, or large block data.

Applied shard state is stored in a local RocksDB-like LSM using typed binary keyspaces and versioned Protobuf values.

The primary implementation language is Go. The storage gateway core should remain a single-language Go service. Java, .NET, and other consumers should use generated gRPC clients rather than pushing those languages into the gateway implementation. A Rust helper may be considered later only for a measured hot path, and should default to a process boundary rather than FFI.

Default Go substrate choices:

```text
Raft: go.etcd.io/raft/v3
metadata KV: Pebble
API: grpc-go plus protobuf
backend clients: native provider SDK adapters behind a small internal backend interface
crypto: OpenBao Go API client for Transit wrapping plus Go standard crypto for local payload encryption
observability: OpenTelemetry, Prometheus exposition where needed, and log/slog
Go version: latest stable patch pinned at implementation time
```

These are default implementation candidates, not a license to skip validation. The Go decision is accepted if a representative spike proves the mixed hot path under the storage system's latency, memory, and GC budgets. The spike should exercise streaming writes and reads, block append and index write, metadata KV update, checksum work, fsync rhythm, bounded memory, and simulated commit pressure. It does not need real Raft in the first pass; real Raft integration should be validated separately after the runtime and byte path are proven.

Go runtime guardrails:

```text
GC CPU <= 5% under target load
p99 GC pause <= 10ms
stable heap under GOMEMLIMIT
no full-document heap buffering
no unbounded per-transaction or hot-index growth
```

If the first Go spike fails, perform one Go tuning pass focused on allocation shape, buffer reuse, cache layout, and benchmark realism. If tuned Go still misses hard budgets, run the same representative spike in Rust before reconsidering the implementation language.

Recommended keyspaces:

```text
transaction
document
document_by_transaction
block
block_ref
envelope
upload_job
repair_job
restore_job
rewrap_job
replica_membership
```

Metadata schemas use versioned Protobuf values and additive-first compatibility. Breaking metadata or block-format changes require explicit feature gates and upgrade choreography so rolling upgrades do not reinterpret old state.

The in-memory/read-optimized index is a derived projection. It exists to hit the metadata lookup latency target for `HeadDocument`, `ReadDocument`, and transaction-scoped `FindDocuments`, but it is never the source of truth. It must be rebuildable from:

```text
installed shard snapshot
local applied KV state
Raft log tail / WAL after snapshot
verified local `.idx` files for physical byte refs
```

Strong metadata reads use Raft ReadIndex by default. Lease reads are allowed only as an optimization after the chosen Raft implementation, clock assumptions, and fencing behavior are explicitly validated. If a follower cannot prove freshness, it forwards or redirects to the leader.

Shard snapshots contain metadata, job state, envelope state, restore state, and membership state. They do not contain document bytes or derived hot indexes. After installing a snapshot, a replica must verify referenced local block files and indexes before it can serve byte reads. Missing or corrupt local refs become repair work.

Background work uses a Raft-backed transactional outbox. Upload, repair, restore, and rewrap jobs are at-least-once operations with idempotent external effects. The shard leader owns scheduling and fencing. Capable replicas may execute leased jobs and report completion through consensus.

If a shard cannot reach consensus quorum, it must reject writes and strong metadata reads. The system targets continued availability within the configured fault budget, not split-brain operation after quorum loss.

## Go Runtime Design Defaults

The byte path should use strict streaming slabs. Document payloads must not be accumulated on the heap, stored inside protobuf messages, or passed through `io.ReadAll`-style full-body buffering. Small documents should be fast because metadata and local block ranges are hot, not because the runtime silently buffers complete objects.

The core concurrency model should be shard actors plus bounded workers. Shard-owned loops sequence Raft application, metadata mutation, open-block ownership, and commit visibility. Blocking file IO, backend upload, scrub, restore, and crypto work run in bounded pools and report completion through the shard state machine or durable operation model.

Local file IO should use explicit Go standard-library primitives with reviewable durability boundaries. Fsync and fdatasync decisions must be visible in the code and covered by crash-recovery tests. Async IO libraries, mmap-heavy designs, or FFI storage shortcuts are not v1 defaults; they require profiling evidence and an ADR-level trade-off.

The hot metadata index should be a bounded typed cache derived from durable shard state. Cache entries should favor compact structs, byte-array fingerprints, integer IDs, and explicit size accounting. Full metadata blobs can remain in Pebble unless they are deliberately admitted to the hot cache. Unbounded maps and pointer-rich nested structures are not acceptable defaults.

## Go Correctness Harness

Correctness validation is a hard gate for production write-path work. The harness should be layered and adversarial:

```text
unit invariants
model-based lifecycle tests
deterministic shard simulators
crash/recovery tests
in-process Raft integration tests
fault-injection tests
allocation and GC gates
race and goroutine-leak tests
representative performance spikes
```

The correctness oracle must assert the full storage contract:

```text
no ACKed document is lost
no corrupt bytes are returned
unfinalized/prepared bytes are never visible
idempotent retries preserve the documented semantics
cleanup never deletes committed or possibly committed bytes
metadata visibility and byte-serving eligibility remain consistent after recovery
```

Crash tests should cover every durability boundary in the write path: prepared byte append, local fsync, `.openlog` record fsync, peer prepare, consensus commit, post-commit bookkeeping, block seal, `.idx` creation, backend upload intent, and cleanup. Each crash/restart assertion must prove that ACKed documents are readable or repairable from verified sources, and that non-ACKed prepared data does not become visible by accident.

The deterministic simulator starts with the shard write lifecycle. It should model writes, prepare/finalize states, commit visibility, duplicate attempts, recovery replay, and repair eligibility. Model-based tests are required: generated operation sequences compare implementation behavior against a small authoritative model. Go's built-in fuzzing should be used for parsers and binary formats, and `pgregory.net/rapid` is the default candidate for stateful/model tests with shrinking.

Fault injection should be built through injected interfaces rather than only external chaos. File IO, clocks, backend clients, OpenBao Transit, and Raft transport need deterministic fault hooks. V1 tests must inject storage write/read/fsync failures, torn or corrupt records, node crashes, dropped/reordered Raft messages, backend throttling/errors, and OpenBao unavailability.

Disk durability tests should use real Linux filesystem directories plus a faulting IO wrapper. V1 storage-member support assumes Linux local persistent volumes with ext4/XFS-like semantics; generic network filesystems are not part of the safety claim. File creation, rename, link, and delete operations that affect durable state require parent directory fsync where needed.

Raft integration tests use an in-process multi-node harnessed transport around `go.etcd.io/raft/v3`. Hard-gated scenarios include leader changes, partitions, dropped/reordered messages, snapshots, joint consensus membership changes, ReadIndex, replay after restart, and byte-serving eligibility after snapshot install. Initial strong metadata reads use ReadIndex only; lease reads remain deferred until timing and fencing assumptions are explicitly validated.

Backend and OpenBao tests use deterministic fakes for correctness and separate smoke tests for real native SDK/OpenBao compatibility. The fake backend must support range reads, missing objects, corrupted objects, throttling, retryable errors, restore-pending behavior, and upload verification. The fake Transit service must support wrap, unwrap, rewrap, key-version behavior, and outage/error modes.

The first Go spike is representative but disposable. It should include real `grpc-go` streaming, real file IO, real Pebble, real checksums, a minimal real subset of the block/openlog/index concepts, a fake commit barrier instead of real Raft, and fake backend/OpenBao adapters. The spike code should not become production code by default; preserve benchmark reports, failure cases, and distilled tests as the reusable output.

The initial synthetic workload is ETL-biased and trace-replay-ready:

```text
2-7 documents per transaction
70%: 16-128KiB
25%: 128KiB-4MiB
4.9%: 4-32MiB
0.1%: 128MiB max
concurrent writes
immediate post-finalize reads
metadata HeadDocument reads
background seal/upload simulation
```

Authoritative performance gates require a dedicated perf profile, such as stable local NVMe or Kubernetes storage-node hardware with pinned Go version, `GOMEMLIMIT`, `GOGC`, container memory limit, filesystem, disk class, and kernel/runtime notes. Developer laptops may run the harness, but they are not pass/fail authority.

Initial hot-read gates:

```text
metadata / HeadDocument p95 <= 8ms
small document first byte p95 <= 8ms
small document full read p95 <= 25ms
p99 <= 3x the matching p95 target, except tests marked burst-only
```

Write ACK latency is reported in the first spike, not hard-gated until quorum, hardware, and deployment profiles are fixed. Write correctness, bounded memory, allocation shape, GC guardrails, and read-tail isolation under writer pressure are hard gates.

Every spike report should include read/write p50, p95, p99, first-byte latency, throughput, GC CPU, GC pause distribution, heap profile, allocation rate, fsync latency, open-block behavior, error counts, and invariant results. Randomized tests must persist failing seeds, workload shape, runtime config, and enough reproduction metadata to replay the failure.

CI should be tiered:

```text
PR: unit tests, deterministic simulators, core race tests, core leak tests
nightly: crash/recovery, broader race tests, fault injection, model-based long runs
dedicated/manual: performance gates, backend/OpenBao smoke tests, large corpus runs
```

Observability is part of the harness. Tests should assert key metrics for ACKs, read source, repair, fsync latency, Raft lag, backend throttling, GC/runtime behavior, restore-pending paths, and integrity failures. High-cardinality document identifiers belong in logs/traces, not metric labels.

## Go Project Layout

Use one root Go module:

```text
github.com/petabytecl/scrap
```

The repository should use a minimal `golang-standards/project-layout`-inspired structure, not a full scaffold. Create directories only when they contain real code or configuration:

```text
cmd/scrapd
cmd/scrapctl
cmd/scrap-spike
internal/
internal/gen/scrap/v1
proto/scrap/v1
configs/
deployments/
build/
scripts/
test/
```

Do not create `/pkg` in v1. Implementation code remains private under `internal/`. Public service contracts are protobuf sources under `proto/scrap/v1`. Generated Go code is committed under `internal/gen/scrap/v1` for reproducible server builds. A public Go client package can be introduced later only after the API contract is stable and there is a real external import need.

Use Buf as the default protobuf tool for linting, breaking-change checks, and code generation. Tool versions should be pinned in repo-managed configuration; CI must not rely on `@latest` or runner-global protobuf tools.

Initial internal package map:

```text
internal/node       process lifecycle, wiring, shard registry, listeners, shutdown
internal/shard      one Raft group, write lifecycle, visibility, hot index ownership
internal/blockstore local .blk/.idx/.openlog files, frame IO, checksums, recovery
internal/metastore  Pebble keyspaces, snapshots, applied metadata state
internal/raftstore  persistent Raft log/storage glue and snapshot plumbing
internal/backend    provider-neutral backend interface helpers
internal/backend/s3
internal/backend/gcs
internal/backend/azure
internal/backend/fs
internal/cryptoenv  envelope handling, AAD, DEK cache, OpenBao integration boundary
internal/api        public gRPC transport handlers and error mapping
internal/admin      admin operation service and durable operation orchestration
internal/authz      workload capability policy and authorization decisions
internal/config     typed config loading, validation, and hot reload
internal/identity   tenant/transaction/document identity, UUIDv7, fingerprints, key encoding
internal/observe    metrics, tracing, logging helpers, cardinality policy
```

Package dependency rules:

```text
cmd/* stays tiny and calls internal/node or generated clients
internal/node is the composition root
internal/shard orchestrates lifecycle and imports lower-level stores/adapters
low-level packages do not import internal/shard or internal/api
gRPC handlers stay thin: validate, authorize, stream, map errors, call shard/admin services
scrapctl imports generated clients only and does not import server internals
```

Use explicit constructors and small consumer-owned interfaces. Avoid global registries, service locators, and DI frameworks until the dependency graph proves that they remove more complexity than they add.

Core storage packages should use explicit internal Go structs for identities, physical refs, lifecycle state, and invariants. Do not pass protobuf-generated request/response types through the storage core by default. Convert between protobuf and internal structs at API, admin, snapshot, and metadata serialization boundaries.

Internal errors should be typed or sentinel errors with retryability and failure class preserved. Map them to gRPC status codes only at transport boundaries. Storage, repair, and shard logic should not depend on gRPC status types.

`internal/node` owns process lifecycle and the shard registry. Each `internal/shard` instance owns one Raft group and its state. Worker pools are node-level resources with per-shard admission and fairness controls for IO, backend, crypto, repair, restore, and upload work. The hot metadata index is shard-owned and derived from committed metadata, not a global node cache.

## Protobuf API Schema Direction

Public and admin APIs use protobuf over gRPC. Public and admin schemas are split by audience:

```text
scrap.v1: public service-fleet API and shared logical types
scrap.admin.v1: operator/admin workflow services
```

Public protobuf sources live under `proto/scrap/v1`. Admin protobuf sources live under `proto/scrap/admin/v1`. Generated Go code is committed under:

```text
internal/gen/scrap/v1          package scrapv1
internal/gen/scrap/admin/v1    package adminv1
```

Buf is the schema tool. Use strict `FILE` breaking-change checks by default, reserve removed field numbers and names, and evolve schemas additively. Do not rely on proto3 implicit zero values when unset differs from zero or empty. Use `optional` deliberately for scalar presence, such as `expected_length`, `range.length`, and optional timestamps or hints.

Recommended proto file layout:

```text
identity.proto
document.proto
transaction.proto
errors.proto
admin.proto
operations.proto
```

Use sparse field numbering. Keep common fields in low numbers, reserve gaps, and group future extension areas by hundreds when messages are expected to grow. Avoid `google.protobuf.Any` in public API contracts unless a later ADR justifies it. Standard imports allowed in v1 include `google.protobuf.Timestamp`, `google.protobuf.Duration`, and standard Google RPC status detail types.

Client-visible errors use canonical gRPC status codes plus typed protobuf error details. Standard validation failures use field-violation details with S.C.R.A.P. reason codes. Storage-specific error detail messages:

```text
RestorePendingDetail
CryptoUnavailableDetail
IntegrityFailureDetail
DuplicateWriteDetail
UnsafeCapacityDetail
RetryHint
```

Retry hints should appear in typed status details and may also be echoed through gRPC metadata for generic clients.

Status-code mapping:

```text
missing document: NOT_FOUND
prepared/unfinalized document for ordinary readers: NOT_FOUND
cold archived bytes / restore pending: UNAVAILABLE + RestorePendingDetail
OpenBao unavailable and DEK not cached: UNAVAILABLE + CryptoUnavailableDetail
all known byte sources corrupt: DATA_LOSS + IntegrityFailureDetail
active duplicate write: ABORTED + DuplicateWriteDetail
finalized identity with different bytes/metadata: ALREADY_EXISTS
expected_length or expected_sha256 mismatch: FAILED_PRECONDITION
invalid identity, tags, ranges, or stream ordering: INVALID_ARGUMENT
unauthorized capability: PERMISSION_DENIED
```

Request/correlation IDs are accepted through gRPC metadata, logged/traced, and echoed where useful in responses or operations. Security identity is server-derived from mTLS/workload identity. Request fields such as `created_by_service` are caller metadata, not the security principal.

### Public API Catalog

`DocumentService` v1 RPCs:

```text
WriteDocument(stream WriteDocumentRequest) returns WriteDocumentResult
HeadDocument(HeadDocumentRequest) returns HeadDocumentResponse
ReadDocument(ReadDocumentRequest) returns stream ReadDocumentResponse
FindDocuments(FindDocumentsRequest) returns FindDocumentsResponse
```

`TransactionService` v1 RPCs:

```text
CompleteTransaction(CompleteTransactionRequest) returns TransactionState
GetTransaction(GetTransactionRequest) returns TransactionState
```

`WriteDocument` uses ordered stream messages:

```text
WriteDocumentRequest
  oneof message:
    init WriteDocumentInit
    chunk WriteDocumentChunk

WriteDocumentInit
  identity DocumentIdentity
  document_class DocumentClass
  priority_class PriorityClass
  content_type optional string
  expected_length optional uint64
  expected_sha256 optional bytes
  client_idempotency_key optional string
  created_by_service string
  workflow_stage optional string
  tags map<string,string>

WriteDocumentChunk
  data bytes
```

The first write-stream message must be `init`; later messages must be `chunk`. The server rejects wrong ordering with `INVALID_ARGUMENT`. `client_idempotency_key` is optional in the schema but required by validation for `CRITICAL_INGEST`.

`WriteDocumentResult`:

```text
metadata DocumentMetadata
desired_replica_count uint32
achieved_replica_count uint32
repair_required bool
idempotent_replay bool
```

`ReadDocument` uses ordered response messages:

```text
ReadDocumentRequest
  identity DocumentIdentity
  range optional ReadRange
  chunk_size_hint optional uint32

ReadDocumentResponse
  oneof message:
    metadata ReadDocumentMetadata
    chunk ReadDocumentChunk

ReadDocumentMetadata
  metadata DocumentMetadata
  selected_range ReadRange
  source StorageSource

ReadDocumentChunk
  data bytes
```

`ReadRange` is `offset + optional length`. An absent range means full document. An absent length means from offset to EOF. `chunk_size_hint` is clamped by the server to the documented 64 KiB to 4 MiB range; default is 1 MiB.

Core shared public messages:

```text
DocumentIdentity
  tenant_id string
  transaction_id string
  document_name string

TransactionIdentity
  tenant_id string
  transaction_id string

ReadRange
  offset uint64
  length optional uint64

TimeRange
  start_time optional google.protobuf.Timestamp
  end_time optional google.protobuf.Timestamp
```

`document_name` stays a string for caller ergonomics, with documented validation: valid UTF-8, relative virtual path, no empty segments, no duplicate slashes, no `.` or `..` traversal segments, and no leading slash.

Public `DocumentMetadata` exposes logical status but not physical storage refs:

```text
identity DocumentIdentity
document_class DocumentClass
priority_class PriorityClass
content_type optional string
length uint64
logical_sha256 bytes
document_identity_fingerprint bytes
created_by_service string
workflow_stage optional string
created_at google.protobuf.Timestamp
finalized_at google.protobuf.Timestamp
availability DocumentAvailability
lifecycle_state DocumentLifecycleState
tags map<string,string>
```

Raw hashes and fingerprints use `bytes` on the wire. Display encoding is lowercase hex for logs and CLIs. Physical block, shard, member, and replica refs are admin-only.

Public enums:

```text
DocumentClass:
  DOCUMENT_CLASS_UNSPECIFIED
  DOCUMENT_CLASS_PERMANENT
  DOCUMENT_CLASS_EPHEMERAL

PriorityClass:
  PRIORITY_CLASS_UNSPECIFIED
  PRIORITY_CLASS_CRITICAL_INGEST
  PRIORITY_CLASS_NORMAL
  PRIORITY_CLASS_BULK

DocumentAvailability:
  DOCUMENT_AVAILABILITY_UNSPECIFIED
  DOCUMENT_AVAILABILITY_HOT
  DOCUMENT_AVAILABILITY_COLD
  DOCUMENT_AVAILABILITY_RESTORE_PENDING
  DOCUMENT_AVAILABILITY_CRYPTO_UNAVAILABLE
  DOCUMENT_AVAILABILITY_DEGRADED_REPAIR

DocumentLifecycleState:
  DOCUMENT_LIFECYCLE_STATE_UNSPECIFIED
  DOCUMENT_LIFECYCLE_STATE_ACTIVE
  DOCUMENT_LIFECYCLE_STATE_TRANSACTION_COMPLETED
  DOCUMENT_LIFECYCLE_STATE_TOMBSTONED
  DOCUMENT_LIFECYCLE_STATE_PENDING_RECLAMATION

StorageSource:
  STORAGE_SOURCE_UNSPECIFIED
  STORAGE_SOURCE_LOCAL
  STORAGE_SOURCE_PEER
  STORAGE_SOURCE_BACKEND
  STORAGE_SOURCE_RESTORED_BACKEND

TransactionStateKind:
  TRANSACTION_STATE_KIND_UNSPECIFIED
  TRANSACTION_STATE_KIND_OPEN
  TRANSACTION_STATE_KIND_COMPLETED
  TRANSACTION_STATE_KIND_TIMED_OUT
```

Every enum has an `UNSPECIFIED` zero value, and request validation rejects unspecified values for fields where the caller must choose.

`FindDocuments` remains transaction-scoped and unpaginated in v1 because normal transactions contain only a small number of documents:

```text
FindDocumentsRequest
  transaction TransactionIdentity
  filter optional DocumentFilter

DocumentFilter
  document_name_exact optional string
  document_name_prefix optional string
  document_class optional DocumentClass
  content_type optional string
  workflow_stage optional string
  created_by_service optional string
  created_at optional TimeRange
  tags map<string,string>  // match all supplied tags

FindDocumentsResponse
  documents repeated DocumentMetadata
```

`CompleteTransaction` is a lifecycle/reporting marker. It does not control document visibility; finalized documents are visible immediately after each successful write.

```text
CompleteTransactionRequest
  transaction TransactionIdentity
  completed_by_service optional string
  tags map<string,string>

GetTransactionRequest
  transaction TransactionIdentity

TransactionState
  transaction TransactionIdentity
  state TransactionStateKind
  document_count uint32
  permanent_document_count uint32
  ephemeral_document_count uint32
  created_at google.protobuf.Timestamp
  completed_at optional google.protobuf.Timestamp
  timeout_at optional google.protobuf.Timestamp
  tags map<string,string>
```

Initial public validation limits:

```text
tenant_id: <= 256 bytes
transaction_id: <= 512 bytes
document_name: <= 1024 bytes
tags: <= 64 entries
tag key: <= 128 bytes
tag value: <= 1024 bytes
total metadata: <= 16 KiB
content_type: optional printable string; not a security boundary
zero-length documents: allowed
```

### Admin API Catalog

Admin APIs live in `scrap.admin.v1`. Explicit restore/prewarm remains admin/control-plane only. Ordinary application reads auto-queue restore for cold data according to scheduler limits but do not expose explicit restore/prewarm RPCs.

Admin target messages use typed protobufs, not free-form strings:

```text
Target
  oneof target:
    document DocumentTarget
    transaction TransactionTarget
    block BlockTarget
    shard ShardTarget
    storage_member StorageMemberTarget
    snapshot SnapshotTarget

DocumentTarget: tenant_id, transaction_id, document_name
TransactionTarget: tenant_id, transaction_id
BlockTarget: shard_id, block_id
ShardTarget: shard_id
StorageMemberTarget: storage_member_id
SnapshotTarget: shard_id optional, snapshot_id/checkpoint_id
```

`Operation` is the shared durable admin job model:

```text
Operation
  operation_id string   // UUIDv7
  operation_type string
  state OperationState
  requested_by_identity string
  requested_at google.protobuf.Timestamp
  started_at optional google.protobuf.Timestamp
  finished_at optional google.protobuf.Timestamp
  dry_run bool
  targets repeated Target
  progress OperationProgress
  warnings repeated OperationWarning
  last_error optional OperationError
  metadata map<string,string>

OperationState:
  OPERATION_STATE_UNSPECIFIED
  OPERATION_STATE_PLANNED
  OPERATION_STATE_QUEUED
  OPERATION_STATE_RUNNING
  OPERATION_STATE_SUCCEEDED
  OPERATION_STATE_FAILED
  OPERATION_STATE_CANCELED
  OPERATION_STATE_EXPIRED
```

`WatchOperation` streams an initial full snapshot followed by sequenced progress/state deltas. `CancelOperation` is cooperative only; operations stop at safe checkpoints and return `CANCELED` or the already-terminal state.

Admin service surface:

```text
InspectService:
  GetClusterSummary
  GetShard
  GetDocument
  GetBlock
  GetMember
  GetCapacityRunway

OperationService:
  GetOperation
  WatchOperation
  ListOperations
  CancelOperation

RestoreService:
  PlanRestore
  StartRestore
  PlanPrewarm
  StartPrewarm

RepairService:
  GetRepairQueue
  PlanRepair
  StartRepair

MemberService:
  CordonMember
  UncordonMember
  GetEvictionSafety
  PlanDrain
  StartDrain

LifecycleService:
  PlanTombstone
  StartTombstone

DisasterRecoveryService:
  GetRecoveryReadiness
  PlanRecovery
  StartMetadataRestore
  StartCopyVerify
  StartDRDrill
```

Dangerous or costly workflows use Plan/Start. Plan RPCs return an `operation_plan_id`, `plan_hash`, `expires_at`, affected targets, estimated impact, and warnings. Start RPCs require the recent plan token/hash. Direct raw membership edits are not exposed; member and replica changes happen through typed workflows.

Capacity overrides and key-rotation/rewrap state are inspect-only in the initial admin schema. Add dedicated `CapacityService` or `KeyService` only when implementation pressure proves a typed mutating API is needed.

## Go Build And Release Policy

Use `Makefile` as the primary developer and CI command entrypoint. Expected targets include:

```text
make test
make test-race
make lint
make proto
make proto-check
make build
make image
make perf-smoke
```

Initial CI assumes GitHub Actions for PR and release workflows. Dedicated self-hosted runners are required for authoritative performance, fsync-heavy, crash/recovery, and storage-node tests. GitHub-hosted runners are acceptable for normal compile, lint, proto, unit, and lightweight simulator checks.

Every PR should run:

```text
go test
race tests for core packages
go vet / staticcheck-equivalent lint
buf lint
buf breaking
generated-code cleanliness check
unit invariant tests
deterministic simulator smoke tests
```

Nightly CI should run:

```text
crash/recovery suites
fault-injection suites
long model-based tests
broader race and goroutine-leak tests
backend adapter smoke tests
OpenBao Transit smoke tests
```

Production release gates require:

```text
PR gates green
nightly correctness suite green
dedicated performance profile pass
vulnerability gate pass or documented exception
SBOM generated
artifact/image signatures
build provenance attestation
immutable image tag and digest
```

Go dependencies and build/codegen tools are pinned. Updates happen through scheduled PRs or explicit security PRs. Dependency update PRs must run the normal gates, and storage/runtime dependency updates must include at least a benchmark or perf-smoke comparison when relevant.

Production images use pinned multi-stage container builds. The runtime image is minimal/distroless-style and nonroot. It should contain only the S.C.R.A.P. binary and required runtime assets, not a package manager or shell. A matching debug image or documented ephemeral debug workflow should be published separately; production images stay minimal.

Release artifacts should include SBOMs, signatures, and provenance attestations. Vulnerability scanning is severity-gated with documented, time-bound exceptions for non-reachable or base-image noise. Critical/high reachable issues block release.

Runtime configuration uses typed config files plus environment overrides for deployment/runtime settings such as `GOMEMLIMIT`, `GOGC`, log level, listener addresses, and profile selection. Bad hot reloads keep the last valid config and alert.

Go toolchain upgrades are benchmark-gated. Upgrade Go through deliberate PRs, rerun correctness and perf gates, and compare GC, heap, and latency profiles before promoting a release.

Release versions use SemVer tags. Binaries and images also carry build metadata:

```text
git SHA
Go version
schema version
active block/index/envelope format versions
build time
dirty tree flag
```

Images are published with immutable version and git-SHA tags, and production should deploy by digest. Do not mutate release tags. `latest` may exist for development only and must not be used by production manifests.

Production rollout strategy is canary, then shard-safe rollout. Do not rely on plain Kubernetes rolling update semantics alone. Rollout must respect shard quorum, storage-member readiness, PDBs, byte-serving eligibility, and consensus-owned format feature gates.

## Read Path

Reads prefer local hot storage.

Read order:

```text
lookup shard route
lookup committed metadata through safe shard metadata read
if local committed physical ref exists:
  stream local block range
else if peer committed physical ref exists:
  stream peer block range
else if backend block is warm and decryptable:
  stream from backend block range
else if backend block is archived/cold:
  queue block restore and return restore-pending
else if backend block is warm but crypto material is unavailable:
  return crypto-unavailable
```

Metadata may be served by the leader or by ReadIndex-valid followers. Bytes may be served by any committed checksum-valid replica.

Priority does not affect read scheduling in the current model. Priority is write-side only.

`HeadDocument` confirms existence and metadata from shard consensus state even when bytes are no longer local. `ReadDocument` is all-or-error before streaming bytes: it plans the requested range first and does not stream a partial prefix if any required frame needs archive restore or OpenBao decrypt is unavailable.

Cold backend restore is explicit:

```text
restore trigger: automatic on first cold read, plus admin/prewarm API
restore unit: physical `.blk` object
restore targets accepted by API: transaction, document, or block
restore state: tracked in shard consensus and refreshed from backend metadata
```

Object-store archive restore is block-level because backend archive classes restore whole objects, not arbitrary document ranges. A document or transaction restore request expands to the affected block set.

When a cold `.blk` becomes readable in the backend, S.C.R.A.P. may serve read-through from the backend and opportunistically rehydrate local replicated cache if disk and admission policy allow. Prewarm may request a bounded `pin_until` TTL, but pinning is capped by configuration and refused under disk pressure.

Restore/prewarm work uses operational lanes such as `interactive-restore`, `planned-prewarm`, and `bulk-audit`. It does not reuse caller-supplied write priorities.

Client-visible cold-read outcomes:

```text
document missing:
  NOT_FOUND

document exists but bytes are archived:
  structured restore-pending error
  includes affected block ids, restore state, retry_after, restore_queued

document exists and bytes are warm, but OpenBao is unavailable and DEK is not cached:
  structured crypto-unavailable error

document exists and requested range is immediately serveable:
  stream metadata first, then bytes
```

Normal reads use one verified source for the requested range. On corruption or missing bytes, the gateway may retry affected frames from a peer or backend source before failing the read.

## Repair And Scrubbing

Repair is a first-class safety path, not only a background optimization. Repair work is created when:

```text
write ACK used quorum instead of full replica target
replica failed during prepare/commit
returning replica is missing committed refs
scrub detects checksum mismatch
read path detects missing or corrupt bytes
backend upload/reconciliation finds divergent state
```

Bit rot is explicitly in scope for v1. S.C.R.A.P. cannot prevent all media corruption, but it must:

```text
detect corruption
avoid serving suspect bytes
quarantine evidence
repair from a verified source
report integrity incidents when no valid source remains
```

S.C.R.A.P. end-to-end hashes are authoritative:

```text
logical_sha256: client-visible document bytes
stored_sha256: bytes stored inside local/plaintext block representation
ciphertext_sha256: encrypted backend object bytes when applicable
frame checksums
whole-block checksums
idx/envelope integrity checks
```

Provider checksums and filesystem checksums are useful supporting signals, but they are not the correctness contract.

Preferred repair source order:

```text
committed peer local ref
verified backend block ref
rebuild from other committed document refs when necessary
```

Repair unit is a document range or the minimum safe frame range needed to restore the missing/corrupt local ref. Repair completes only after checksum verification and a consensus update that adds the repaired physical ref or clears the repair requirement.

Checksum mismatch behavior:

```text
quarantine bad local ref
preserve evidence for operator inspection
emit repair job
avoid serving the suspect bytes
do not delete the only remaining evidence until another valid ref exists
```

Read-path corruption behavior:

```text
quarantine suspect source
retry the requested range from an alternate verified replica or backend source
return only bytes that pass S.C.R.A.P. verification
enqueue repair for the bad ref
```

If every known local and backend source for the requested document/frame fails verification:

```text
return typed integrity-failure error
freeze/quarantine available evidence
page operators
prevent reclamation of related evidence
never return least-bad bytes
```

Corruption evidence retention:

```text
retain bad-ref metadata, checksums, source, block/frame ids, and incident context
retain corrupt bytes only when policy allows
keep evidence through repair plus configured forensic TTL
do not retain corrupt bytes forever by default
```

A returning node must catch up through Raft and verify local refs before serving reads. Catching up metadata alone is not sufficient to become a byte-serving replica.

Repair lanes:

```text
critical
normal
scrub
```

Critical repair protects degraded critical-ingest writes and has the tightest target. Scrub repair must not starve write safety, normal repair, or foreground reads.

Scrub coverage:

```text
local .blk/.idx/.openlog where present
backend .blk/.idx/.env inventory and integrity
local metadata references against consensus state
backend object refs against consensus inventory
```

Backend scrub is tiered:

```text
frequent metadata/head/checksum inventory checks
continuous sampled byte verification
risk-prioritized byte reads for older, recently restored, provider-suspect, or previously repaired blocks
annual full-equivalent backend byte verification target where cost allows
```

Local scrub schedule:

```text
continuous low-rate background scrub
target full local block coverage: 7 days
immediate event-driven scrub after crashes, disk errors, return from quarantine, suspicious reads, or repair completion
```

Scrub priority:

```text
routine scrub: lowest-priority lane
evidence-driven verification: may jump to repair/verification lane
known corruption repair outranks routine scrub
foreground reads/writes and critical repair outrank routine scrub
```

Repair admission uses runway-based thresholds, not raw job counts. Inputs include:

```text
estimated time to restore full replica health
critical repair age
bytes under repair
placement risk
disk runway
repair source availability
```

Default general repair/admission thresholds:

```text
warn: estimated time to safe replica health > 1 minute
throttle lower-priority writes: > 5 minutes
reject new writes retryably before unsafe ACK: > 15 minutes
```

Critical-ingest degraded repair keeps the stricter target:

```text
target: under 2 seconds
alert: over 10 seconds
```

Default replica-health posture:

```text
5 verified local refs: healthy
4 verified local refs: degraded, repair promptly
3 verified local refs: quorum-only risk, page if older than 5 minutes
<3 verified local refs: unsafe for the normal write durability model
```

If a shard is at quorum-only risk and repair cannot restore a fourth verified ref within the 5-minute throttle threshold, throttle bulk and normal writes for that shard. Reject new writes retryably before accepting a write that cannot meet the selected durability policy.

Routine scrub budgets are deployment-profile values with conservative defaults:

```text
local scrub cap: 5% of measured per-node sequential read bandwidth
local scrub target: full local block coverage within 7 days
backend inventory/head cap: 5% of backend HEAD/read-metadata budget
backend sampled-byte cap: 1% of backend restore/read byte budget
backend sampled-byte borrow cap: 5% when upload/repair runway is healthy
archive-tier byte scrub: disabled unless explicitly budgeted
```

If the local scrub cap cannot meet the 7-day local coverage target, S.C.R.A.P. alerts instead of silently increasing scrub pressure. Foreground reads, writes, repair, and upload runway remain higher priority than routine scrub.

Backend scrub defaults:

```text
manifest/object-ref inventory: daily
metadata/head verification: weekly full logical inventory
sampled byte verification: continuous within scrub byte budget
risk-prioritized byte verification: immediately after restore, repair, provider anomaly, or checksum mismatch
full-equivalent backend byte verification: annual target where cost allows
```

Routine backend sampled reads must not trigger archive restore by default. Archive-class verification requires an explicit audit/DR budget because it can create large provider cost and long restore queues.

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

The document table, frame table, envelope reference, and checksums live in `.idx`. Active wrapped DEK material and crypto profile metadata live in `.env` when backend encryption is enabled.

All block and index binary formats use:

```text
endianness: little-endian
integer sizes: explicit u8/u16/u32/u64
timestamps: unix epoch milliseconds as u64
UUIDv7: raw bytes[16]
fingerprints: raw bytes[16]
SHA-256: raw bytes[32]
```

## Block Format Compatibility

The `.blk`, `.idx`, and `.env` formats are long-lived compatibility contracts. Permanent documents may remain archived for years, so old readers cannot be removed just because new writers exist.

Format versions use major/minor semantics:

```text
format_major: breaking compatibility boundary
format_minor: additive/backward-compatible extension
```

Rolling upgrade rule:

```text
new binaries must read old live formats
writers keep using the shard's active committed format
new writable format is enabled only by explicit shard consensus feature gate
binary default must not silently change persisted format
```

The shard consensus state machine owns the active writable format for:

```text
.blk
.idx
.env
crypto profile
compression profile when introduced
```

Deployment config may declare allowed formats, but it does not decide what a shard writes at runtime. This avoids config skew during rolling upgrades.

Unknown fields and flags:

```text
unknown required/incompatible flag: fail closed
unknown optional section: skip only if section is length-delimited and declared skippable
unknown crypto/compression mode: fail closed
unknown checksum mode: fail closed
```

Additive minor versions should prefer length-delimited optional sections over changing fixed record layouts. If a fixed record layout must change, use a new record size and format gate so old readers reject safely.

Migration strategy:

```text
old immutable blocks remain readable in place
new sealed blocks use the active writable format
repair/rehydration/re-encryption may rewrite into the active format when bytes are rewritten anyway
no eager full-corpus rewrite for routine format upgrades
```

Reader retention:

```text
keep old major readers until shard/backend inventory proves no live object uses that major
do not retire reader code on a fixed calendar window
deprecation requires inventory/audit proof, like Transit key-version retirement
```

Format inventory is authoritative in shard metadata. Each committed block/envelope record should track:

```text
blk_format_major/minor
idx_format_major/minor
env_format_major/minor when encrypted
crypto_profile
compression_profile when used
required feature flags
```

Backend scans may reconcile this inventory, but backend `LIST` is not the source of truth.

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
40      16    auth_tag bytes[16]
56      4     crc32c u32
60      36    reserved
```

Frame nonces are not stored per frame. Encrypted backend objects store a per-block nonce seed in the encryption envelope, and each frame nonce is deterministically derived from:

```text
nonce_seed
object_kind = block | index
crypto_profile
block_id
frame_index
```

This keeps the frame table algorithm-neutral across AES-GCM and XChaCha20-Poly1305 profiles.

For unencrypted blocks:

```text
ciphertext_offset = plaintext_offset
ciphertext_length = plaintext_length
encryption_mode = 0
auth_tag zeroed
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

Block-level metadata blobs are optional and can carry future compression dictionary information, envelope identity/hash references, or other block-scoped extension data. Active wrapped DEK material lives in `.env`, not in `.idx`.

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

The backend stores sealed blocks. Unencrypted V1 blocks use two backend objects:

```text
<block_id>.blk
<block_id>.idx
```

When backend encryption is enabled, V1 uses a third small warm metadata object:

```text
<block_id>.env
```

Backend object roles:

```text
.blk: large immutable block payload object
.idx: immutable block index/checksum/document metadata object
.env: tiny active encryption envelope and crypto metadata
```

For encrypted backend blocks, `.blk` and `.idx` are protected payloads. `.env` is a cleartext control object containing only wrapped key material and non-secret crypto metadata. `.env` and `.idx` should remain in a warm metadata tier; `.blk` is the object expected to move to cold/archive tiers under backend lifecycle policy.

The leader's sealed block is the canonical backend block in v1:

```text
leader local block sealed
verify block checksum
upload block, index, and envelope when applicable to backend
commit backend block location
documents now have backend refs: block_id + stored_offset + stored_length
```

If the leader fails before upload, recovery may upload another replica's sealed block or rebuild a canonical backend block from committed documents.

Reads prefer local replica physical refs, then peer refs, then backend canonical block refs. Routine backend recovery must not depend on object-store `LIST`; shard metadata is the inventory.

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

Federated cells may share the same backend account or bucket, but ownership is separated by cell/source metadata and physical prefixes such as `cells/<cell_id>/...`. This is not tenant-based backend routing; it is a safety boundary for source ownership, import, and recovery.

## Disaster Recovery Scope

V1 disaster recovery scope is schema plus tooling, not active always-on secondary backend replication.

V1 DR contract:

```text
recover from local cluster loss using:
  primary backend .blk/.idx/.env objects
  shard metadata snapshots/checkpoints
  compact uploaded metadata tail segments
  OpenBao Transit key material and policy restored separately
```

The v1 contract is primary-backend recovery. Cross-region or cross-cloud RPO is not promised until active secondary replication, lag SLOs, and failover tests become product requirements.

Metadata recovery artifacts:

```text
periodic shard metadata snapshot/checkpoint objects
default snapshot target: every 5 minutes
compact async metadata tail segments between snapshots
stored in the primary backend
```

Backend latency must not enter the metadata commit path. Snapshot and tail uploads are asynchronous durability/recovery jobs, but recovery RPO should be observable from their lag.

DR ordering rule:

```text
metadata snapshots/tails may reference only verified backend data
required objects for an encrypted block: .blk + .idx + .env
required objects for an unencrypted block: .blk + .idx
```

A recovered cluster must not see committed backend refs that point to missing or unverified backend objects.

DR restore tooling:

```text
load metadata snapshots/checkpoints
replay compact metadata tail segments
verify referenced .blk/.idx/.env objects and checksums
verify OpenBao Transit availability, key versions, context policy, and decrypt permissions
rebuild shard inventory and routing state
admit reads/writes only after safety gates pass
```

OpenBao DR is a hard dependency for encrypted backend data. S.C.R.A.P. must not persist emergency plaintext DEKs in snapshots.

Secondary backend support in v1:

```text
metadata model supports multiple backend locations
admin jobs can copy and verify data to a secondary backend
secondary copies are not part of write ACK
secondary copies do not define a v1 RPO/RTO contract
```

Secondary copy/verify jobs may target:

```text
metadata snapshot/checkpoint
shard
transaction
block
```

Successful v1 DR drill:

```text
start a fresh cluster
restore metadata snapshot plus tail
verify backend inventory
verify OpenBao decrypt path
serve sampled document reads
report missing/corrupt objects and recovery timing
```

Full-corpus byte reads may be scheduled as separate audit work, but they are not required for every DR drill.

Default DR evidence cadence:

```text
daily: validate latest metadata manifest, pointer, checksums, and OpenBao decrypt health
weekly: restore latest metadata snapshot/tail into an offline verifier
monthly: disposable-environment sampled byte-read drill
quarterly: operator-led fresh-cluster recovery exercise
```

Default sampled-read drill size:

```text
sample at least 10,000 documents
also sample at least 0.001% of documents represented by the manifest
cap backend byte reads at 1 TiB per routine monthly drill unless explicitly approved
```

Samples are stratified rather than purely random:

```text
newest 24h
oldest retained permanent documents
largest documents
small hot documents
recently repaired documents
recently restored documents
encrypted backend blocks
cold/archive-class blocks when the drill budget allows restore
multiple shards and storage members
```

Every drill report includes:

```text
snapshot/tail source and lag
restore start/end timestamps
metadata object counts and checksum results
sampled document count and byte count
OpenBao key/context validation result
missing/corrupt/unreadable object list
operator actions required
measured RTO/RPO evidence
```

V1 documents measured recovery evidence, but it does not invent business RTO/RPO promises. Business RTO/RPO targets remain a product/operations decision.

## Backend Key Layout And Rate Control

Application virtual paths must not be used directly as backend key prefixes. Backend keys need high-cardinality physical prefixes to avoid hot partitions.

Backend capacity is configured through an explicit deployment profile. The core scheduler is provider-neutral; each backend implementation supplies the concrete limits and retry semantics for that provider.

Provider profile inputs:

```text
backend type: s3 | gcs | azure_blob | filesystem | other
physical hash fanout
per-backend request budgets
per-physical-prefix request budgets when applicable
bytes/sec upload and restore budgets
provider retryable status/error classes
provider ramp-up rules
object lifecycle hints
cost/restore class hints
```

Do not hardcode S3 rate constants into the core design. S3, GCS, Azure Blob, and future backends expose different scaling boundaries. The default first-production S3-like profile uses fixed hash fanout and provider-calibrated token buckets.

Default physical fanout:

```text
hash prefixes per backend profile: 256
hash source: block identity
not based on tenant_id, transaction_id, document_name, or wall-clock time
```

Default physical key shape:

```text
cells/<cell_id>/blocks/v1/p=<hash_prefix>/shard=<shard_id>/<block_id>.blk
cells/<cell_id>/blocks/v1/p=<hash_prefix>/shard=<shard_id>/<block_id>.idx
cells/<cell_id>/blocks/v1/p=<hash_prefix>/shard=<shard_id>/<block_id>.env
cells/<cell_id>/metadata/v1/...
```

The `cell_id` prefix separates source ownership and import/recovery boundaries. It is not a tenant routing mechanism.

Provider profiles are explicit deployment configuration. They are not inferred from provider type alone.

Backend profile shape:

```text
profile_id
backend_type: s3 | gcs | azure_blob | filesystem | other
physical_hash_fanout
key_layout_version

accepted_ingress_budgets:
  peak_5m_mib_per_sec
  rolling_1h_mib_per_sec
  daily_bytes
  max_docs_per_sec
  max_sealed_blocks_per_sec
  critical_ingest_reserve_percent

request_budgets:
  per_backend_put_per_sec
  per_backend_get_per_sec
  per_backend_head_per_sec
  per_backend_delete_per_sec
  per_prefix_put_per_sec?
  per_prefix_get_per_sec?
  per_prefix_head_per_sec?

byte_budgets:
  upload_mib_per_sec
  restore_mib_per_sec
  scrub_mib_per_sec
  dr_copy_mib_per_sec

lane_budgets:
  repair/reconciliation
  permanent-block-upload
  restore/prewarm
  rewrap-envelope-sync
  disaster-recovery-secondary
  routine-scrub

retry_policy:
  normalized retry classes
  exponential backoff base/max
  jitter
  token feedback behavior
  per-lane circuit breaker thresholds

ramp_policy:
  initial_fraction
  healthy_window
  max_increase_per_window
  throttle_backoff_window

admission_guards:
  local_durability_window_hours
  disk_warn_percent
  disk_throttle_percent
  disk_reject_percent
  runway_warn_hours
  runway_throttle_hours
  runway_reject_hours

lifecycle_hints:
  provider-neutral storage intent
  provider class name
  estimated restore delay/cost hints
```

Production profiles require explicit measured budgets. Do not start production with unbounded accepted ingress, upload, restore, scrub, DR-copy, or circuit-breaker budgets. Request-count defaults may exist for the S3-like profile only; GCS, Azure Blob, filesystem, and future providers require measured deployment-specific request budgets before production acceptance.

Primary capacity units are bytes:

```text
MiB/sec
bytes/day
backlog bytes
disk runway hours
```

Document rate and sealed-block rate are still required secondary limits. Document rate protects API and metadata capacity. Sealed-block rate protects backend request pressure and object-count behavior.

The first concrete default is an S3-like profile:

```text
physical_hash_fanout: 256
per_prefix_put_budget: about 1,750 PUT-class requests/sec
per_prefix_get_head_budget: about 2,750 GET/HEAD-class requests/sec
ramp: controlled increase after healthy windows
throttle response: reduce effective token rate and back off with jitter
byte budgets: required per deployment
```

These S3-like request budgets are deliberately conservative, roughly half of the common public S3 per-prefix performance guidance. They are starting points, not entitlement claims. Production deployments must measure real account, region, bucket, network, object-size, and retry behavior.

GCS and Azure Blob profiles use the same schema but should not receive fake universal numeric defaults. Provider-specific guidance belongs in the adapter/profile documentation:

```text
GCS: account for bucket-level initial request capacity, gradual ramp-up, and object-key index hotspotting.
Azure Blob: account for storage-account limits, partition behavior, 503/500 throttling signals, and exponential backoff.
```

Filesystem backend also uses a profile. It is not unlimited just because it is local. Filesystem profiles must declare byte budgets, fsync behavior assumptions, capacity guard bands, and backpressure behavior for dev, test, and on-prem deployments.

Backend adapters normalize provider errors before they reach the scheduler:

```text
THROTTLED
TRANSIENT
AUTH
NOT_FOUND
CONFLICT
CORRUPT
PERMANENT
```

Scheduler retries use capped exponential backoff with jitter, coordinated with token-bucket feedback. Provider SDK retries may still be used locally by an adapter, but they must not hide retry storms or bypass S.C.R.A.P. lane/backlog accounting.

Circuit breakers are per backend and lane, not one global backend switch. A restore/prewarm outage should not automatically stop permanent uploads or repair if those lanes remain healthy. Auth or permission failures trip immediately. Sustained throttling or transient lane failure trips after a default 5-minute unhealthy window unless the production profile declares a stricter threshold. Lane circuits emit alerts and preserve local ACK safety through backlog/runway gates.

Backend adapters may use multipart or resumable upload internally. Core metadata still treats `.blk`, `.idx`, and `.env` as whole logical backend objects. Provider-specific upload parts must not leak into shard consensus metadata.

Backend upload verification is `put` then verify. After upload, the adapter must verify required object length/checksum/metadata through provider-appropriate `head` or equivalent calls before S.C.R.A.P. marks the backend ref committed. The backend commit unit is the whole required object set:

```text
unencrypted block: .blk + .idx
encrypted block: .blk + .idx + .env
```

Do not expose a committed backend ref that points to a partial object set.

S.C.R.A.P. owns lifecycle hints, not full backend retention policy. The profile can express a provider-neutral storage intent plus exact provider class name:

```text
HOT
WARM
COLD
ARCHIVE
provider_class_name
estimated_restore_delay
estimated_restore_cost_hint
```

Actual retention, tiering, and legal-hold policies remain backend/infrastructure concerns unless a future requirement expands S.C.R.A.P. into an active lifecycle-policy engine. Restore planning should show profile estimates plus observed backend restore state; it must not promise exact provider restore timing.

Profile validation is fail-closed in production. Startup rejects missing, invalid, or mathematically unsafe backend profiles unless an explicit non-production risk mode is enabled. Bad hot reload keeps the last valid profile and alerts. If configured backend capacity is lower than accepted ingress, production startup is unsafe unless the profile proves local durability window sizing, backlog drain, and runway guards remain safe. Write admission must still throttle or reject before durability is threatened.

Example physical layout:

```text
cells/<cell_id>/blocks/v1/p=<hash-prefix>/shard=<shard_id>/<block_id>.blk
cells/<cell_id>/blocks/v1/p=<hash-prefix>/shard=<shard_id>/<block_id>.idx
cells/<cell_id>/blocks/v1/p=<hash-prefix>/shard=<shard_id>/<block_id>.env
```

Backend sizing uses sealed blocks per second plus bytes per second as the primary unit. Document rate still matters for API and metadata capacity, but backend request pressure is driven by sealed block upload sets:

```text
unencrypted block: .blk + .idx
encrypted block: .blk + .idx + .env
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

Backend scheduler lanes:

```text
permanent-block-upload
ephemeral-spill-if-policy-requires
repair/reconciliation
disaster-recovery-secondary
restore/prewarm
rewrap-envelope-sync
routine-scrub
```

Backend upload is committed only after every required object for the block is uploaded and verified:

```text
unencrypted block: .blk + .idx
encrypted block: .blk + .idx + .env
```

Upload retries use exponential backoff plus per-backend and per-physical-prefix token buckets. Upload backlog alone does not stop write ACKs; local replicated durability remains the immediate source of truth. Write admission stops only when disk/backlog guards show that continued local acceptance would make durability unsafe.

Backend capacity goal:

```text
drain accepted daily load
drain accepted bursts within the configured local durability window
do not require backend capacity to match instantaneous write burst peak
```

This is the production sizing target. The local replicated tier absorbs short peaks and backend brownouts; the backend budget must still prove that the deployment can drain what it accepts before the local durability window becomes unsafe.

V1 uses sliced local rate limiting:

```text
shard-local lane limits
node-level backend aggregate limits
node-level physical-prefix limits
no global distributed rate limiter in v1
```

Each storage member receives a configured budget slice and enforces it with local token buckets. This avoids a global distributed limiter in the write safety path while still bounding aggregate backend pressure.

Capacity is shared by lane and shard fair-share. Tenant fair-share is not part of the v1 backend uploader because tenant-based durable routing was rejected and tenant sets are dynamic.

Lane budgets use floors plus borrowing. Each lane has a minimum floor and a maximum cap. Idle capacity may be borrowed by higher-priority backlog, but operator-triggered work must give capacity back when upload or repair runway is threatened.

Default production lane floors:

```text
permanent-block-upload: 70%
repair/reconciliation: 20%
restore/prewarm: 5%
rewrap-envelope-sync: 3%
disaster-recovery-secondary: 2% if explicitly enabled, otherwise 0%
routine-scrub: opportunistic unused capacity only
```

If `disaster-recovery-secondary` is disabled, its 2% default floor returns to the borrowable pool.

Default constrained-capacity lane priority:

```text
1. repair/reconciliation
2. permanent-block-upload
3. restore/prewarm
4. rewrap-envelope-sync
5. disaster-recovery-secondary
```

Permanent backend upload lag SLO under healthy backend conditions:

```text
95% of sealed permanent blocks uploaded and verified within 15 minutes
```

A missed upload SLO alerts. It does not directly throttle writes. Throttling and rejection are driven by disk/runway safety math.

Backend lag alone does not make acknowledged writes unsafe. The local replicated tier is sized to absorb backend outage or brownout.

Default local durability window:

```text
24 hours of accepted peak ingress
```

Default write reserve:

```text
critical_ingest reserve: 10% of usable local write capacity
```

Capacity planning formula:

```text
required local bytes =
  peak accepted ingress bytes/sec
  * durability window seconds
  * replica overhead
  + open block slack
  + repair churn slack
  + safety margin

default safety margin: 30%
```

Average daily ingest is not sufficient for this workload because massive multinational bulk bursts can exceed normal backend upload rate while still needing safe local ACKs.

Admission uses multi-window math:

```text
current 5-minute accepted ingress rate
rolling 1-hour accepted ingress rate
rolling 24-hour accepted bytes
backend drain rate by lane
local disk free/reserved bytes
computed hours until unsafe
```

The deployment has one accepted-ingress budget plus the critical reserve. Priority class controls throttling order. Per-service budgets may be added later if operational evidence requires them. Per-tenant write admission is not part of v1.

Under local disk pressure:

1. delete only expired/eligible ephemeral data;
2. increase upload/cleanup pressure;
3. throttle by workload class, lane, and shard fair-share policy;
4. reject new writes with retryable errors before durability becomes unsafe;
5. never acknowledge a write that cannot be durably protected.

Default disk guard bands:

```text
70% used: pressure cleanup, upload acceleration, early warning
80% used: throttle bulk and normal write classes
90% used: reject new writes except configured critical reserve
```

Default disk runway guard bands:

```text
runway < 12 hours: warn and accelerate cleanup/upload
runway < 6 hours: throttle bulk and normal write classes
runway < 2 hours: reject new normal writes except configured critical reserve
```

`critical_ingest` may continue at the 90% guard only from preconfigured reserved capacity. When the critical reserve is exhausted, critical writes are rejected retryably too. Critical writes are never allowed to consume the last unsafe bytes.

Operator-triggered restore, DR copy, rewrap, and scrub jobs run only inside explicit lane budgets by default. They pause or shed borrowed capacity when disk/runway guards show risk to permanent upload or repair.

Emergency capacity overrides:

```text
requires admin_capacity_override
requires dry-run plan and reason
must have a TTL
is audited
rolls back automatically to the last valid profile
```

Burst handling:

```text
absorb within the 24h local durability window
throttle/shedding order: bulk, then normal, then critical reserve
avoid a hard cliff at disk exhaustion
```

Mandatory capacity metrics:

```text
upload backlog bytes
oldest upload backlog age
per-lane queue depth and age
disk used/free/reserved/runway
critical reserve usage
throttle and reject counts by class
provider error classes and retry delay
backend/prefix token saturation
upload SLO compliance
```

High-severity alerts fire before write rejection when:

```text
disk runway drops below configured hours
runway falls below 12/6/2h thresholds
oldest permanent upload exceeds SLO
persistent provider throttling occurs
critical reserve is being consumed
backend backlog growth exceeds drain capacity
capacity override is active or near expiry
```

The write admission controller must reject safely before any durability invariant is at risk.

## Permanent And Ephemeral Backend Policy

Permanent documents are uploaded to the configured backend after local durable commit and block seal.

Ephemeral documents default to local replicated storage only. They are not uploaded to the backend unless policy allows spill during disk pressure or long retention.

Backend upload state may reduce durability repair urgency, but it does not replace local replicas for low-latency active workflow reads.

## Encryption And Compression

Backend encryption is policy-driven. It is primarily important for external object storage such as S3. Local hot storage may remain application-plaintext by default, relying on node/disk/Kubernetes controls.

Accepted encryption model:

```text
KEK scope: deployment-level OpenBao Transit key
DEK scope: one data-encryption key per sealed block
DEK storage: wrapped in per-block `.env` envelope
payload encryption: local, inside S.C.R.A.P.
Transit use: datakey, decrypt, rewrap, rotate
routine rotation: rewrap `.env`, do not re-encrypt `.blk`
```

Do not send whole blocks, indexes, or frames to OpenBao Transit for encryption. Transit wraps and unwraps DEKs; S.C.R.A.P. encrypts backend payloads locally.

For encrypted backend blocks:

```text
<block_id>.blk: encrypted block payload frames
<block_id>.idx: encrypted index/checksum/metadata payload
<block_id>.env: cleartext envelope with wrapped DEK and crypto metadata
```

The active envelope is authoritative in shard consensus for hot operations. Backend `.env` is a small warm disaster-recovery/export copy. Rewrap updates consensus first, then syncs and verifies the backend `.env`.

`.env` format:

```text
fixed magic/version header
length-prefixed Protobuf body
CRC/length checks
```

Envelope body should carry:

```text
block_id
shard_id
crypto_profile
transit_key_name
transit_key_version
wrapped_dek
nonce_seed
canonical Transit context/AAD hash
current envelope hash/version
previous envelope while backend sync is pending
rewrap status
```

Consensus stores the expected envelope hash/version. Transit context/AAD authenticates the wrapped DEK against the canonical block identity. The `.env` object carries local CRC/length checks for corruption diagnostics.

The Transit KEK must be created as a strict non-exportable key:

```text
exportable: false
plaintext backup: disabled
deletion: disabled
upsert: disabled
derivation: enabled
rotation: allowed
min_decryption_version: changed only by audited operator workflow
```

Transit calls use canonical derived context plus associated data bound to:

```text
deployment_id
shard_id
block_id
object_kind = block | index | envelope
block/index format version
crypto_profile
frame_size
frame_index when encrypting a frame
```

This makes swapped or cross-deployment envelopes fail authentication instead of silently decrypting under the wrong block identity.

Efficient backend range reads require framed block encryption.

Encrypted backend frame shape:

```text
frame record:
  plaintext_offset
  plaintext_length
  ciphertext_offset
  ciphertext_length
  auth_tag
  checksum
```

Default crypto frame size is 1 MiB, with 4 MiB as a possible tuning option. Frame nonces are derived from the envelope nonce seed and canonical context; they are not stored per frame.

Crypto profiles are configurable. V1 default is AES-256-GCM. XChaCha20-Poly1305 is an explicit allowed profile. "Quantum-ready" in this design means 256-bit symmetric keys plus crypto agility; it does not mean a blanket guarantee against every future quantum-relevant attack.

One OpenBao data key is requested per sealed block. S.C.R.A.P. derives separated subkeys for block payload encryption, index payload encryption, and nonce/AAD domains using context-bound key derivation. Do not reuse the raw DEK directly across object kinds.

Plaintext DEKs:

```text
never persisted
cached only in bounded process memory
default cache TTL: 5 minutes
evicted by TTL, size cap, restart, or operator action
wiped on eviction where runtime allows
```

OpenBao outage behavior:

```text
local plaintext hot reads continue
encrypted backend reads continue only when the needed DEK is cached
new encrypted backend upload/seal work pauses until Transit recovers
local replicated write ACK may continue until disk/backlog guards reject safely
never upload plaintext as an encryption fallback
never use an emergency local wrapping key in v1
```

Key rotation policy:

```text
scheduled KEK rotation: every 90 days by default
incident rotation: immediate operator-triggered rotation
routine post-rotation work: async `.env` rewrap
rewrap completion target: 7 days
old key retirement: audit-proven only
```

Do not raise `min_decryption_version` or trim old Transit key versions until shard inventory proves no active envelope requires them. Rewrap progress is tracked by consensus inventory and a rewrap outbox; OpenBao audit logs are supporting evidence, not the source of truth.

Rewrap is not retroactive protection after a real compromise. If an attacker has copied old ciphertext and envelopes and also obtains the old KEK or plaintext DEK, routine rewrap does not make that copied data safe. Confirmed KEK-plus-backend, DEK, or algorithm compromise may require affected block re-encryption or breach handling.

OpenBao authentication uses Kubernetes auth with least-privilege policies. Normal gateways should not have broad key-admin rights. Rewrap workers need rewrap permission; operator automation owns rotate/config workflows.

OpenBao DR is part of the durability model. Because KEKs are non-exportable and plaintext key backup is disabled, deployments require OpenBao HA, tested storage snapshots, and tested unseal/recovery procedures. Losing Transit key material makes encrypted backend data unrecoverable.

Crypto audit is mandatory. OpenBao audit devices must be enabled. S.C.R.A.P. logs key name, key version, job ID, block ID, request ID, and status, but never plaintext DEKs or wrapped DEK blobs.

Encryption is transparent to clients. Normal reads return original logical document bytes.

Compression default is none. Optional per-document compression may be used for compressible content such as XML. Compression happens before storage/encryption. Avoid whole-block or whole-frame compression in v1 because it complicates efficient range reads.

Hashes:

```text
logical_sha256: original client-visible bytes
stored_sha256: bytes stored inside the block before backend encryption
ciphertext_sha256: encrypted backend bytes when applicable
```

## Admin API And Operator Control Plane

V1 exposes admin operations through:

```text
typed gRPC admin service
operator CLI built on the gRPC service
```

The CLI is an operator client, not a separate source of truth. Automation should be able to use the same typed gRPC service.

Admin API scope:

```text
inspect cluster/shard/document/block state
restore and prewarm cold backend blocks
cordon/drain storage members
inspect and enqueue bounded repair
prepare and execute lifecycle tombstones
plan/copy/verify disaster-recovery artifacts
inspect capacity, backlog, placement, and runway
apply bounded emergency capacity/write-admission overrides
inspect key-rotation and rewrap status
```

Admin RPC shape:

```text
resource-specific workflow RPCs
shared durable Operation model
typed target messages
separate Plan* and Start*/Execute* calls for costly or dangerous operations
GetOperation for polling
WatchOperation server-stream for live progress
```

Avoid a single generic `AdminAction` RPC. It is too weakly typed for dangerous storage operations. Avoid raw free-form target strings on the wire; the CLI may parse friendly target strings into typed protobuf messages.

V1 admin RPC groups:

```text
InspectService
OperationService
RestoreService
RepairService
MemberService
LifecycleService
DisasterRecoveryService
```

Common typed targets:

```text
DocumentTarget: tenant_id + transaction_id + document_name
TransactionTarget: tenant_id + transaction_id
BlockTarget: shard_id + block_id
ShardTarget: shard_id
StorageMemberTarget: storage_member_id
SnapshotTarget: snapshot_id or shard_id + checkpoint id
```

Every mutating admin request requires an `operation_id`:

```text
operation_id: caller-supplied or CLI-generated UUIDv7
purpose: idempotent retry, deduplication, status lookup, audit correlation
```

Mutating admin operations create durable operation jobs. Long-running operations are not hidden in logs or tied to a single synchronous RPC.

Operation job model:

```text
operation_id
operation_type
requested_by_identity
requested_at
dry_run flag
state: planned | queued | running | succeeded | failed | canceled
progress counters
affected shards / blocks / documents / members
last_error
audit metadata
```

The CLI may poll or stream job status. Retrying a command with the same `operation_id` must return the existing operation state rather than creating duplicate work.

Plan/execute model:

```text
Plan* RPCs are non-mutating
Plan* RPCs return affected targets, estimated impact, warnings, and plan token/hash
Start*/Execute* RPCs mutate state and require operation_id
dangerous/costly Start*/Execute* RPCs require a recent plan token/hash
```

Dry-run is required for costly or dangerous operations:

```text
restore/prewarm
member drain
force/bounded repair
lifecycle tombstone
disaster-recovery copy/verify
capacity/write-admission override
```

Read-only inspection should expose:

```text
cluster placement health
storage member state
shard membership and leader
document metadata and physical refs
block refs, upload state, restore state, envelope state
capacity backlog and disk runway
repair backlog and bad/quarantined refs
authorization/audit policy status
```

Restore/prewarm API:

```text
accepted targets: transaction, document, block
dry-run expands targets to physical block set
plan reports current restore state, estimated backend action, cost/storage class hints, and optional pin impact
execute queues durable restore jobs
optional pin_until is bounded by config and refused under disk pressure
```

Restore is physically block-level even when requested by transaction or document.

Drain API:

```text
cordon storage member
dry-run reports affected shards, bytes, placement risk, and estimated movement
execute moves/re-replicates shard replicas safely
status reports safe_to_evict when all affected shards are placement-healthy elsewhere
```

Kubernetes drain should wait for S.C.R.A.P. `safe_to_evict`. PDBs are a guardrail, not the drain protocol.

Repair API:

```text
inspect bad, missing, under-replicated, or quarantined refs
enqueue bounded repair by shard, block, document, or storage member
report repair source, target, checksum result, and consensus update
```

Operators may ask S.C.R.A.P. to repair. They must not hand-edit committed document refs or bypass consensus state.

Lifecycle/tombstone API:

```text
two-step prepare and execute
prepare reports affected documents, blocks, backend refs, and retention/audit impact
execute records durable tombstones
reclamation is asynchronous and separately observable
```

There is no ordinary direct delete API. Tombstones are administrative, audited, and durable.

Capacity override API:

```text
time-limited emergency override
audited and capability-protected
bounded by hard safety ceilings
cannot force ACKs that violate durability invariants
```

Disaster-recovery admin API:

```text
plan restore from primary backend snapshot/tail
start restore inventory rebuild
plan secondary copy/verify by snapshot, shard, transaction, or block
start secondary copy/verify jobs
inspect DR snapshot/tail freshness and recovery readiness
run DR drill and report sampled read results
```

DR admin operations use the same operation model and typed targets as other admin workflows.

CLI vocabulary:

```text
scrapctl inspect cluster|shard|document|block|member
scrapctl operation get|watch|cancel
scrapctl restore plan|start|status
scrapctl repair inspect|plan|enqueue|status
scrapctl member inspect|cordon|drain|status
scrapctl lifecycle tombstone plan|execute|status
scrapctl dr snapshot|restore-plan|restore-start|copy-plan|copy-start|verify|drill
scrapctl capacity inspect|override-plan|override-start
```

Admin operations require the specific service capability documented in the authorization section. A broad admin bit is not sufficient for dangerous operations such as tombstone, key rotation, repair override, or capacity override.

## Internal Authorization

S.C.R.A.P. is internal infrastructure, but authorization still protects expensive and safety-critical operations.

Authentication source:

```text
primary identity: mTLS workload identity
recommended identity form: SPIFFE-style or Kubernetes workload identity equivalent
caller-supplied created_by_service: audit metadata only
caller-supplied tenant_id: document identity/audit/quotas/fingerprints only
```

Caller metadata is trusted for normal business meaning after validation, but it is not the security principal. A caller cannot gain critical priority, retention behavior, restore rights, or admin rights by setting request fields.

Authorization model:

```text
authenticated service identity -> configured capabilities
```

V1 does not enforce per-tenant ACLs. All services may handle all tenants if their service capability allows the operation. This matches the dynamic tenant set and avoids a tenant-policy system in the hot path.

Recommended service capabilities:

```text
write_documents
write_critical_ingest
write_permanent
write_ephemeral
read_documents
restore_prewarm
admin_inspect
admin_force_repair
admin_lifecycle_tombstone
admin_capacity_override
admin_key_rotation
admin_disaster_recovery
```

Priority authorization:

```text
critical_ingest requires write_critical_ingest
unauthorized critical_ingest request: PERMISSION_DENIED
do not silently downgrade unauthorized critical writes
```

Document class authorization:

```text
permanent requires write_permanent
ephemeral requires write_ephemeral
unauthorized class request: PERMISSION_DENIED
do not silently promote ephemeral to permanent
```

Read authorization:

```text
read_documents allows service-level reads across tenants
tenant-level read ACLs are out of scope for v1
```

Restore/prewarm authorization:

```text
explicit prewarm requires restore_prewarm
automatic restore-on-read is governed by read authorization and restore scheduler limits
bulk prewarm is cost-impacting and must not be available to every reader by default
```

Admin authorization is split by operation. Do not use one broad admin bit for dangerous actions such as tombstone, key rotation, repair override, or capacity override.

Policy source:

```text
static deployment policy
hot reload supported
startup without valid policy: fail closed
bad hot reload: reject new policy, keep last valid policy, alert
```

The authorization policy is deployment configuration, not shard consensus metadata in v1. Avoid an external policy engine until the operational need is proven.

Mandatory authorization audit events:

```text
all denied requests
successful critical_ingest writes
successful ephemeral writes
explicit restore/prewarm requests
admin operations
key rotation/config operations
lifecycle tombstone operations
capacity override operations
```

Normal hot reads and ordinary writes do not require full audit logging by default. They remain observable through request logs, metrics, traces, and immutable document metadata unless a later compliance mode requires per-access audit.

## Open Questions

These are the known gray areas to resolve before turning this into an ADR or implementation plan.

- What business RTO/RPO targets should the product promise beyond measured DR evidence?
- Should read-only cache cells ever serve known immutable documents when imported metadata lag exceeds the default 15-minute serve limit?
- What compliance policy should govern corrupt-byte forensic retention, legal hold, and audit evidence retention?
- What real production ingress, disk, backend, and OpenBao capacity numbers should each deployment profile use?
- Should active secondary backend replication become a v1 product requirement, or remain a post-v1 extension?

## Next Discussion Frontier

The write path, block format, OpenBao envelope policy, cold-read semantics, bit-rot handling, repair/scrub policy, Go consensus/store substrate, correctness harness, package architecture, public/admin API schema direction, build/release policy, backend provider-profile model, capacity budget model, replica placement model, Kubernetes deployment topology, cell/read-only import model, published metadata format, DR drill model, admin API shape, and internal authorization model are concrete enough to move toward ADRs and implementation spikes.

Resume by choosing between:

1. convert the settled design into ADRs for the hard-to-reverse decisions;
2. define the first implementation spike and benchmark harness;
3. review the reserved business/opinion questions above;
4. turn the design into initial GitHub issues/PRD slices.

The recommended next topic is ADR extraction. The architecture now has enough concrete decisions that the risky, expensive-to-change choices should be promoted from exploratory notes into short decision records before implementation work starts.

The previous estimate for deployment topology was 45 to 90 minutes. That was directionally reasonable for a single authoritative Kubernetes deployment, but incomplete once the cell/read-only import layer became a core v1 requirement. The discovered federated-cache scope adds roughly 60 to 120 minutes of design work for import formats, source ownership config, lag SLOs, cache sizing, conflict handling, and read-only crypto grants.
