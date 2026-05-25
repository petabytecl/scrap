# Storage Gateway Durability-Sensitive Coding Guidelines

Status: planning gate for GitHub issue `#17`
Last updated: 2026-05-22

These guidelines apply to code paths that can affect acknowledged write
durability, document visibility, corruption handling, idempotency, operator
safety, or production write ACK readiness.

They are intentionally stricter than ordinary Go style. S.C.R.A.P. acknowledges
writes before backend upload, so local and peer durability, authoritative
metadata, repairability, and observable failure semantics are part of the
product contract.

## Scope

Durability-sensitive code includes:

- public write, finalize, head, read, and transaction APIs;
- local block, index, frame, openlog, and projection storage;
- Raft command, log, snapshot, replay, ReadIndex, and membership code;
- peer prepare, catch-up, repair, quarantine, restore, prewarm, scrub, and
  backend upload workflows;
- OpenBao envelope, rewrap, and crypto-unavailable paths;
- admin operations that drain, cordon, repair, restore, tombstone, override
  capacity, rotate keys, or rebuild from DR artifacts;
- production readiness gates and release evidence aggregation.

Spike code may be looser, but reusable spike conclusions must be distilled
into production code that follows these rules.

## Streaming And Memory

Production write and read paths must stream document bytes in bounded slabs.
They must not accumulate whole document payloads on the heap, store whole
payloads inside protobuf messages, or use `io.ReadAll`-style helpers on
document bodies.

Allowed buffering:

- fixed-size slabs for stream ingress, local writes, checksums, backend upload,
  and peer transfer;
- small metadata and index records;
- bounded per-request scratch buffers whose maximum size is documented or
  enforced by config;
- test fixtures that intentionally build small payloads outside production
  paths.

Required behavior:

- request size limits, slab size, queue depth, and worker counts are explicit;
- slow clients, slow disks, slow peers, backend throttling, and OpenBao
  latency apply backpressure instead of unbounded buffering;
- large result sets use streaming, pagination, or typed operation status rather
  than large in-memory response construction;
- hot-path code does not rely on garbage collection to hide unbounded
  allocation growth.

Tests for production byte paths should include payloads large enough to prove
streaming shape, not only tiny documents that would pass despite full buffering.

## File IO Durability

Durable local file operations must make the intended crash boundary visible in
code and tests. If a write, rename, link, or delete changes the set of bytes or
metadata that recovery depends on, the code must document the sync sequence
near the operation.

Write path rules:

- bytes are prepared and synced before metadata visibility;
- `.openlog` or equivalent prepare records are synced before they are trusted
  during recovery;
- consensus metadata controls document visibility after crashes;
- post-commit local bookkeeping failures repair from committed metadata and
  prepared bytes instead of changing the ACK result after commit;
- prepared but uncommitted bytes remain unreadable after restart.

Sync rules:

- use `fsync` or `fdatasync` deliberately and explain which durability property
  the call protects;
- when file creation, rename, link, or delete affects durable recovery state,
  sync the parent directory where the target filesystem requires it;
- do not assume temp-file rename is durable unless both file and directory sync
  requirements are satisfied;
- do not hide risky operations behind generic helpers unless the helper name
  and tests make the crash boundary clear;
- mmap-heavy, async IO, network filesystems, and FFI storage shortcuts are not
  v1 defaults and require ADR-level evidence before production use.

Risky boundary comments should be short and concrete. A useful comment names
the crash that the sequence survives; an unhelpful comment merely restates that
the code writes a file.

## Errors, Retryability, And Transport Mapping

Durability-sensitive packages return typed internal errors. A typed error must
preserve the information needed to answer:

- did the operation definitely not happen, definitely happen, or become
  unknown to the caller?
- is retrying safe, unsafe, or safe only with the same idempotency key or
  operation ID?
- is the failure transient, throttled, capacity-related, validation-related,
  unauthorized, corrupt, crypto-unavailable, stale, conflict, or permanent?
- does the failure require repair, quarantine, operator action, or release-gate
  evidence?

gRPC status codes and client-visible protobuf error details are created at API
or CLI transport boundaries. Storage, shard, repair, backend, and metadata
packages must not depend on `google.golang.org/grpc/status` or choose status
codes directly.

Concrete mapping pattern:

```text
internal/localstorage:
  return appstatus.New(appstatus.CodeDataLoss, "stored bytes failed checksum")

internal/api:
  call storageapp.DocumentApplication
  map the returned appstatus error with ToGRPCError
  attach client-visible details only after validation/sanitization
```

Core packages own the durable failure class and retry semantics. Transport
adapters own client status codes, error details, and message redaction.

Retry rules:

- retries must be bounded by count, total time, and backoff policy;
- retry only failures classified as retryable by the owner package;
- never retry validation failures, authorization denials, corrupt data, or
  permanent format incompatibility as if they were transient;
- avoid nested retry loops across API, workflow, backend, and CLI layers;
- retry logs and metrics must avoid high-cardinality document IDs as labels.

Error aggregation helpers such as `multierr` may be used for cleanup or
shutdown aggregation only when the primary typed failure remains available to
the caller. They must not flatten durability-critical failure classes into an
opaque combined error.

## Checksums, Corruption, And Quarantine

S.C.R.A.P. checksums are the correctness contract. Provider checksums,
filesystem checksums, and transport checksums are useful supporting signals,
but they do not replace S.C.R.A.P. frame, block, index, envelope, and metadata
checks.

Read rules:

- full reads verify every touched frame before streaming response bytes;
- ranged reads verify every frame segment touched by the requested range;
- if verification cannot prove the requested bytes are correct, the read fails
  before streaming corrupt or partial-prefix bytes;
- a clean range may succeed independently only when every frame touched by that
  range verifies and the API contract allows that range result.

Corruption rules:

- suspect byte sources are quarantined from serving;
- repair uses only verified peer or backend sources;
- missing or corrupt local refs become repair work, not readable refs;
- all known sources corrupt is a data-loss condition and must be reported as
  such;
- forensic evidence defaults to metadata-only unless compliance or legal hold
  requires corrupt-byte retention.

Quarantine records should include source, block/frame/range identity, expected
and observed checksum facts, detection path, request or operation correlation,
and repair status. Do not put sensitive document bytes or plaintext secrets in
logs, metrics, audit records, or quarantine metadata.

## Idempotency

Any externally retryable write, background workflow, or admin operation needs a
stable idempotency key.

Write idempotency:

- document writes are idempotent by document identity plus
  `client_idempotency_key` when provided or required;
- `CRITICAL_INGEST` requires a client idempotency key;
- retrying the same key with the same payload after an unknown client outcome
  returns the existing committed result or continues the same in-flight attempt;
- retrying the same key with different payload, metadata, or checksum fails
  closed with a typed conflict;
- incomplete attempts have bounded retention and cleanup that cannot delete
  committed visibility or acknowledged bytes.

Background job idempotency:

- upload, restore, prewarm, repair, scrub, rewrap, publish, and DR rebuild
  workflows record a durable operation or job identity before side effects are
  trusted;
- duplicate execution verifies existing side effects instead of blindly
  repeating unsafe mutation;
- side effects are committed only after verification of the complete required
  artifact set or state transition;
- restart resumes, retries, or fails with a typed terminal state.

Admin operation idempotency:

- dangerous actions use plan/start workflows where useful;
- start requests carry an operation ID and a plan hash or equivalent stable
  intent proof;
- retrying the same operation ID returns the existing operation state;
- retrying with the same operation ID and different intent fails closed;
- cancel, drain, repair, restore, tombstone, capacity override, and key
  rotation actions are audited and restart-survivable.

## Operator Safety

Operator-facing code must fail closed when continuing could weaken durability,
visibility, auditability, or recovery evidence.

Required safeguards:

- production write ACK mode refuses startup or write admission when required
  readiness gates are missing;
- bad config or policy hot reload keeps the last valid config where safe and
  alerts;
- capacity overrides are bounded, authorized, audited, and cannot force ACKs
  that violate durability invariants;
- destructive operations have typed targets, dry-run or plan phases where
  useful, and clear abort/rollback behavior;
- operator commands call the admin API instead of mutating server internals or
  local files directly.

## Test Style For Durability-Sensitive Code

Tests should prove externally visible contracts and crash/recovery boundaries,
not private helper details.

Expected coverage:

- streaming shape and memory limits for write/read paths;
- crash boundaries around byte append, file sync, openlog sync, metadata apply,
  ACK, replay, cleanup, and repair;
- typed error classes and gRPC mapping at transport boundaries;
- checksum mismatch, missing refs, quarantined sources, and all-sources-corrupt
  behavior;
- idempotent duplicate writes, duplicate job execution, operation restart, and
  plan mismatch;
- deterministic fault injection for file IO, clocks, backend clients, OpenBao
  Transit, and Raft transport.

`testify/require`-style assertions may be used when they make tests easier to
read. `testify/suite`-style shared fixtures should be reserved for integration,
transport, or fault-harness tests with expensive setup. Suites must not hide the
specific invariant each test proves, and production code must not depend on test
libraries.

Randomized and fault-injection tests must preserve failing seeds, workload
shape, runtime config, and enough reproduction metadata to rerun the failure.

## Review Checklist

- Does the code avoid whole-document buffering in production byte paths?
- Are file and directory sync boundaries explicit, commented where risky, and
  covered by crash/recovery tests?
- Does every durability-sensitive error preserve retryability and failure
  class until the transport boundary maps it?
- Do reads verify all touched bytes before streaming and quarantine suspect
  sources?
- Are duplicate writes, jobs, and admin operations idempotent across process
  restart and unknown client outcomes?
- Are operator actions authorized, audited, bounded, and recoverable?
