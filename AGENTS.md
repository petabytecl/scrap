# Repository Instructions

## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues for `petabytecl/scrap`. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default mattpocock/skills triage label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context repo: read root `CONTEXT.md` and `docs/adr/` when present. See `docs/agents/domain.md`.

# S.C.R.A.P. — Project Rules

## Write Path Safety

### Never delete a file before its replacement is verified
When replacing a block file, backend object, or any durable artifact: write the replacement to a temp path first, verify its checksum, then atomically rename. Never call os.Remove on the original before the replacement is confirmed intact.
Failure mode: F-02-003 — replaceBlockFromBackend deleted the local block before confirming the backend copy was retrievable, causing permanent data loss on transient backend failure.
Retire when: All replace operations use atomic temp-file-then-rename pattern and have regression tests.

### Command IDs must be deterministic functions of stable inputs only
Any function named *CommandID or *commandID must produce identical output for identical logical operations regardless of wall-clock time, retry count, or execution context. Never include timestamps, random values, or mutable state in command ID computation.
Failure mode: F-03-003 — completeTransactionCommandID included wall-clock time, making retries non-idempotent and corrupting the billing audit trail.
Retire when: All *CommandID functions are tested with a determinism assertion (call twice with same logical input, assert identical output).

### Guard against empty chunks in streaming reads
Any io.Reader wrapping a gRPC stream must skip or reject zero-length data chunks. Never loop on `len(pending) == 0` without checking if the received chunk was empty.
Failure mode: F-03-002 — chunkReader.Read spun indefinitely on empty chunks, enabling CPU DoS via the write stream.
Retire when: chunkReader has an explicit empty-chunk guard and a regression test.

### Do not use zero as a sentinel for uninitialized uint64 offsets
When tracking whether a uint64 offset has been set, use a separate boolean flag (`initialized`), not a comparison against 0. Zero is a valid block offset (the first frame of any block starts at offset 0 after the header).
Failure mode: F-03-001 — includeVerificationFrame used verifyStart==0 as "not yet set", causing incorrect verification windows for documents at block offset 0 and a potential uint64 underflow panic.
Retire when: All sentinel-based offset tracking uses explicit boolean flags and has offset-0 regression tests.

## Data Integrity

### StoredSHA256 must differ from LogicalSHA256 when encryption is active
When constructing a Document record with encryption enabled (EncryptionMode != NONE), StoredSHA256 must be computed from the actual stored (ciphertext) bytes, not copied from LogicalSHA256. These two fields exist to detect different corruption classes.
Failure mode: F-02-001 — StoredSHA256 was always set equal to LogicalSHA256, making encryption-layer corruption invisible to the verification layer while a production gate asserted this verification worked.
Retire when: A regression test encrypts a block, corrupts a ciphertext byte, and asserts the verification layer rejects it.

### Update ALL secondary indexes when modifying document state
When updating document state in metastore (availability, restore state, lifecycle), always update all three indexes: documentKey, transactionDocumentKey, AND blockDocumentKey. Use replaceDocument() which updates all three, not individual batch.Set calls that may omit one.
Failure mode: F-02-002 — updateTransactionRestoreState updated two of three indexes, causing ListBlockDocuments to return stale availability values that misguided scrub and repair operations.
Retire when: All document-state mutations use replaceDocument() or equivalent that provably updates all indexes, verified by test.

## Security

### Never pass raw internal errors to gRPC callers
All error returns from gRPC handlers must go through ToGRPCError or equivalent mapping. Non-appstatus errors must be wrapped as codes.Internal with a generic message. Context errors (context.Canceled, context.DeadlineExceeded) must map to their corresponding gRPC codes.
Failure mode: F-04-004 — raw Pebble and filesystem errors leaked storage paths and internal identifiers to callers. Context cancellation returned codes.Unknown instead of codes.Canceled.
Retire when: All public and admin server methods have error-mapping coverage tests.

### Audit events must carry the authenticated caller identity
Never use a hardcoded string (like "pre-production-admin-api") as the actor identity in audit events. Extract the workload identity from the gRPC context (authz.WorkloadIdentityFromContext) and record it as the actor.
Failure mode: F-04-007 — All admin audit events recorded the same hardcoded placeholder, defeating forensics and non-repudiation for destructive operations on financial data.
Retire when: All admin audit events carry the authenticated caller identity, verified by test.

### Do not accept "none" as a valid encryption algorithm in production mode
When EnableProductionWriteACK is true, reject any envelope record with AEAD algorithm "none". The "none" bypass should be gated by a config flag that is blocked by the production write-ACK gate.
Failure mode: F-04-005 — The "none" algorithm bypass allowed blocks to be stored and served without encryption, detectable only by manual code inspection.
Retire when: Algorithm allowlist validation is enforced at both envelope creation and validation paths.

## Architecture

### Do not import generated wire types (gen/scrap/*) in domain packages
The internal/localstorage package must not import internal/gen/scrap/admin/v1 or internal/gen/scrap/v1. Domain return types should be defined in internal/storageapp (the port layer). Translation to proto types belongs in internal/api at the gRPC boundary.
Failure mode: F-01-002 — Domain layer was structurally coupled to transport schema, causing proto changes to propagate into storage logic without an interface boundary.
Retire when: grep -r 'gen/scrap' internal/localstorage returns zero hits.

### Do not add methods to localstorage.Application — extract to sub-types
New functionality should be added as methods on focused sub-types (TransactionCoordinator, VerificationEngine, OperationExecutor) that Application delegates to. Do not add new methods directly to the Application struct.
Failure mode: F-01-001 — Application grew to 114 methods across 7,800 lines, concentrating blast radius and making all changes high-regression-risk.
Retire when: Application has fewer than 40 direct methods and all new features go through extracted interfaces.

## Testing

### Every crash-fault catalog pattern must be verified against real test names
When adding or modifying crash-fault scenarios in internal/crashfault/evidence.go, verify each Pattern regex matches at least one real test function using `go test -list <pattern> <package>`. Never rely on recordingRunner for pattern verification.
Failure mode: F-RG-008 — Crash-fault catalog patterns were never verified against real tests. Renamed or deleted tests silently caused the catalog to report "passed" for unexecuted scenarios.
Retire when: CI includes a catalog-pattern-verification step that fails on unmatched patterns.

### Add a compatibility fixture for any new durable binary format
Any new binary format written to disk (Raft log frames, snapshot files, prepare log entries, block headers) must have a golden-file compatibility test in internal/compat. Write a known payload, store the binary as testdata, and assert future reads produce identical results.
Failure mode: F-06-009 — The Raft snapshot binary format had no compatibility fixture. A refactor could silently change the format, breaking node restarts after upgrade.
Retire when: All durable binary formats are covered by compatibility fixtures.

## Deployment

### Base Kustomize manifests must include resource limits
The base StatefulSet (deploy/kustomize/base/statefulset.yaml) must always specify resources.requests and resources.limits for CPU and memory. Never merge a change that removes resource limits from the base manifest.
Failure mode: F-07-001 — No resource limits allowed runaway operations to OOM-kill the pod mid-write.
Retire when: CI validates resource limits are present in rendered Kustomize output.

### The admin gRPC port must not be exposed as NodePort in base manifests
Admin services in deploy/kustomize/base/ must use ClusterIP, not NodePort. NodePort exposure should be confined to local development overlays only.
Failure mode: F-RG-007 — Admin port was exposed as NodePort 30081 with no NetworkPolicy, making destructive operations reachable from the host network.
Retire when: Base manifests use ClusterIP and a NetworkPolicy restricts admin ingress.

### gRPC servers must specify MaxConcurrentStreams and MaxRecvMsgSize
Both public and admin gRPC servers must be created with explicit grpc.MaxConcurrentStreams, grpc.MaxRecvMsgSize, and grpc.KeepaliveEnforcementPolicy options. Do not create grpc.NewServer with only interceptor options.
Failure mode: F-07-003 — Unbounded concurrent streams and message sizes enabled resource exhaustion via the public API.
Retire when: Server creation includes all three options, verified by test or code review.
