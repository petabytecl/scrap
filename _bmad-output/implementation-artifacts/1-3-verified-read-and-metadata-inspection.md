---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
created_at: 2026-06-11T02:08:30-04:00
---

# Story 1.3: Verified Read and Metadata Inspection

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want `ReadDocument` and `HeadDocument` to return verified data or typed failures,
so that billing consumers never process partial or corrupt bytes.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-3.
- Acceptance IDs: AC-1.3.1, AC-1.3.2, AC-1.3.3, AC-1.3.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0002, ADR 0003, ADR 0004, ADR 0014, ADR 0016, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.3.1 - Verified read success.** Given a committed visible Document, when `ReadDocument` is called, then returned bytes pass Block header, Frame header, Frame payload CRC, Frame ordering/count, and Document SHA-256 verification before public metadata or chunks are emitted. Evidence includes the verification command, changed-boundary list, and a persisted metadata check.
2. **AC-1.3.2 - Corruption fails closed.** Given visible metadata or Block bytes are corrupt, when `ReadDocument` or `HeadDocument` is called, then the operation fails closed with the expected typed error. Evidence proves no least-bad, partial, or unverified bytes are returned.
3. **AC-1.3.3 - Cancellation and cleanup.** Given request cancellation occurs while read work is in progress, when `ReadDocument` is called, then read, restore, and stream work stop without leaked goroutines, blocked restore calls, or partial-byte success. Evidence includes cancellation and cleanup proof.
4. **AC-1.3.4 - Route context preserved.** Given internal metadata carries route or Shard context, when read/head verification executes in a single-Shard fixture, then the contract preserves Shard/routing assumptions without adding `tenant_id` to storage identity. Evidence records the storage/index/replay boundary assumptions for Epic 2 routing.

## Tasks / Subtasks

- [x] Add characterization tests before production changes. (AC: 1-4)
  - [x] Cover a successful real Shard read: `WriteDocument`, `HeadDocument`, and `ReadDocument` return matching content type, size, SHA-256, and original `CreatedAt`; the read content exactly matches the committed Document bytes.
  - [x] Cover public gRPC `ReadDocument` through `server.Register` with a real Shard-backed `store.Store`: the first response is metadata only after verification succeeds, followed by chunks whose aggregate bytes match the committed Document.
  - [x] Cover corrupt Block payload, corrupt/truncated Frame data, and corrupt `.idx` metadata through Shard and at least one registered gRPC path. The expected storage error is `store.ErrDataLoss`; the expected gRPC status is `codes.DataLoss`.
  - [x] Cover that a corrupt `ReadDocument` stream returns zero successful `ReadDocumentResponse` messages before the `DATA_LOSS` error. Do not accept a test that only checks the final error after metadata or chunk messages were already received.
  - [x] Cover cancellation with a bounded context and deterministic synchronization. Avoid sleeps; use a blocking Backend/restore test double, a controlled reader, or an existing restore fixture.
- [x] Preserve all-or-error read verification in the storage boundary. (AC: 1, 2)
  - [x] Keep verification behind `store.Store` / `internal/shard`, not in `internal/server`. The server may map errors and stream already-verified bytes, but it must not become the authority for Block, Frame, or Document integrity.
  - [x] Ensure `Shard.ReadDocument` does not return a reader that can later discover integrity corruption after public metadata was emitted. If a reader can still fail after Store returns it, refactor so verification completes before returning metadata/reader.
  - [x] Reuse `internal/block` parsing and verification primitives. Do not duplicate Block or `.idx` parsing in `internal/shard` or `internal/server`.
  - [x] Preserve Block/Frame layout and public protobuf messages. If a storage format or wire change appears necessary, stop and add/update an ADR before implementation closure.
  - [x] Keep `DATA_LOSS` reserved for verified corruption or unrecoverable committed metadata/object mismatch. Missing Documents remain `NOT_FOUND`; transient restore dependency failures remain `UNAVAILABLE`.
- [x] Preserve metadata inspection fail-closed behavior. (AC: 2, 4)
  - [x] Keep strict `index.Resolver` methods (`ResolveDocument`, `ListDocuments`, `ContainsDocument`) as the read-side Projection Resolution authority.
  - [x] Keep `HeadDocument` and `FindDocuments` failing closed when visible Projection entries cannot resolve through Block `.idx` files, when `DocCount` drifts, or when local Block lifecycle indicates metadata loss.
  - [x] Keep metadata-only reads local for evicted Blocks when retained `.idx` and committed Confirmed Upload Catalog authority are valid. `HeadDocument` and `FindDocuments` must not trigger Backend restore.
  - [x] Preserve `tenant_id` as validation-only future routing input. Do not add it to Document identity, Block `.idx`, Pebble Projection keys, Backend keys, logs, metrics, or trace attributes.
- [x] Preserve cancellation and bounded-resource behavior. (AC: 1, 3)
  - [x] Propagate `context.Context` through read, restore, and server streaming work. Check cancellation before expensive verification/decryption work and before sending stream messages where applicable.
  - [x] Do not add sleeps, unbounded goroutines, unbounded channels, package-level state, or full-Block buffering.
  - [x] If changing `internal/block` read helpers, preserve the all-or-error guarantee and avoid buffering full Documents in production paths. Use a two-pass reader or bounded spool pattern rather than returning unverified bytes.
  - [x] Preserve existing restore sharing behavior: cancellation of one waiting reader must not leak the shared restore call, and a later reader must still be able to observe the restored Block when restore completed successfully.
- [x] Preserve transport and evidence behavior. (AC: 1-4)
  - [x] Keep `internal/server` as transport mapping only. It should validate request fields, require `DocumentReader`, map Store errors centrally, and stream verified Store output.
  - [x] Keep public error messages, logs, metrics, traces, and evidence redacted. Do not expose raw `transaction_id`, `document_name`, Backend object keys, local file paths, dependency logs, or Document bytes.
  - [x] Add Debug Log References with exact commands and PASS/FAIL result.
  - [x] Include changed-boundary list, typed-error mapping, cancellation fixture, corruption fixture, and redaction proof in Completion Notes.
  - [x] Run at minimum: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/index ./internal/server ./internal/shard`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` because the story touches server streaming and Shard read/restore state.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` if any package boundary, import graph, or server/store/shard boundary changes.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving to review unless a narrower gate is explicitly justified in the story notes.

### Review Findings

- [x] [Review][Patch] Preserve context and gRPC status errors returned from Store, reader, and stream sends [internal/server/server.go:277]
- [x] [Review][Patch] Add in-progress cancellation coverage for Store/read/send phases, not only pre-canceled calls [internal/server/read_cancellation_test.go:17]
- [x] [Review][Patch] Add explicit Block-header and Frame count/order verification evidence for the read path [internal/shard/read_verification_test.go:48]

## Dev Notes

### Current State

- Story 1.1 and Story 1.2 are complete in this working tree, not necessarily committed. Do not revert their changed files or untracked story/test files while implementing Story 1.3.
- `internal/store.Store` defines `HeadDocument(ctx, txID, docName)`, `ReadDocument(ctx, txID, docName)`, and `FindDocuments(ctx, txID)`. Public transport behavior should remain expressed through this interface.
- `internal/server.HeadDocument` and `internal/server.ReadDocument` validate request identity, require `security.RoleDocumentReader`, add redacted identity telemetry attributes, call `store.Store`, and map errors through `mapStoreError`.
- `internal/server.ReadDocument` currently sends metadata before reading chunks from the returned `io.ReadCloser`. That is safe only if the Store has already completed integrity verification before returning the reader. Do not move integrity authority into the server stream loop.
- `internal/shard.HeadDocument` performs leader/read-index gating, strict Projection Resolution through `findDocEntry`, local lifecycle metadata-read checks, and returns `store.DocumentMeta`.
- `internal/shard.ReadDocument` performs leader/read-index gating and calls `readDocumentFromProjection`, which resolves metadata, checks/restores local Block availability, copies the Block index entry, unlocks, and calls `readDocumentBytes`.
- `internal/shard.readDocumentBytes` currently uses `block.ReadDocument` for unencrypted Documents. Encrypted Documents read stored frames and decrypt through `internal/encryption`, then map missing key, auth denied, Transit outage, invalid envelope, and integrity failures to Store errors.
- `internal/block.ReadDocument` delegates to `ReadDocumentTwoPass`. It verifies first, then returns a reader. Existing implementation details may buffer the verified Document; if you touch this path, preserve all-or-error while moving toward bounded memory instead of adding more buffering.
- `internal/index.Resolver` is the read-side Projection Resolution authority. Strict methods fail closed on visible corruption; lenient methods are for recovery/replay only.
- `internal/shard/read_lifecycle.go` already enforces metadata-read policy for hot, evicted, metadata-loss, and unexpected-loss local Block states. `HeadDocument` and `FindDocuments` should not trigger restore for evicted Blocks with retained `.idx` metadata.
- Restore code in `internal/shard/restore.go` already shares concurrent Block restores by Block ID and uses `context.WithoutCancel` with deadline preservation for the leader restore call. Story 1.3 should prove this behavior where it matters for read cancellation rather than rewriting restore ownership.

### Exact Read Contract

- `ReadDocument` is all-or-error: no metadata response and no chunk response may be emitted until the selected source has passed integrity checks.
- Verification for plaintext local Blocks includes Block/Frame structure, Frame header CRC, Frame payload CRC, expected Frame count/order, and final Document SHA-256.
- Verification for encrypted Documents includes stored ciphertext/frame integrity, envelope validity, Transit/decryption success, plaintext SHA-256, and plaintext byte count.
- `HeadDocument` is metadata inspection only. It must prove visible metadata resolves through strict Projection Resolution and valid retained `.idx` state, but it must not read or restore `.blk` bytes.
- `FindDocuments` remains transaction-scoped and write-order preserving, but Story 1.4 owns public discovery semantics. Only add `FindDocuments` coverage here when needed to prove shared metadata-read fail-closed behavior.
- Corruption means the system cannot safely serve the requested Document or metadata. Return `store.ErrDataLoss` / gRPC `DATA_LOSS`; do not repair silently, return least-bad bytes, or mask as `NOT_FOUND`.
- Cancellation is not success. If the caller cancels before verified bytes are returned, return the context-derived error and prove no partial public success was emitted.

### Implementation Guardrails

- Do not put Document bytes, Block bytes, or verification payloads in Raft. ADR 0001 keeps Raft metadata-only.
- Do not use Backend inventory, local file existence alone, Upload Outbox state, or telemetry as read authority. Use strict Projection Resolution plus Shard-owned local/restore lifecycle checks.
- Do not add a multi-Shard router here. Story 2.3 owns public API routing by Transaction, and Stories 2.1/2.2 own routing and Cell startup boundaries.
- Do not add `tenant_id` to storage identity or response metadata. It remains validation-only future routing input for this story.
- Do not change public protobuf messages. `ReadDocumentResponse` already has `meta` and `chunk_data`; `HeadDocumentResponse` already has the metadata needed by this story.
- Do not introduce new assertion/mocking libraries. Use `testing`, local fakes, existing fixtures, and standard-library helpers.
- Preserve `log/slog` as the application logging API and existing OpenTelemetry identity hashing defaults.
- If storage format, wire protocol, dependency/runtime choices, security/encryption contracts, or cross-package ownership changes, stop and add/update an ADR before implementation closure.

### Project Structure Notes

- Likely touched production files:
  - `internal/block/reader.go` - only if current two-pass reader needs context-aware or bounded-memory behavior while preserving all-or-error.
  - `internal/shard/shard.go` - read/head orchestration, cancellation checks, or mapping around `readDocumentFromProjection`.
  - `internal/shard/read_lifecycle.go` - only if metadata-read lifecycle policy gaps are found.
  - `internal/server/server.go` - only for transport cancellation/error mapping around already-verified Store output; do not add storage verification here.
- Likely touched tests:
  - `internal/block/reader_test.go` or `internal/block/twopass_test.go` for Block/Frame/SHA verification and no returned reader on corruption.
  - `internal/shard/shard_test.go`, `internal/shard/read_lifecycle_test.go`, or a new focused `internal/shard/read_verification_test.go`.
  - `internal/shard/restore_test.go` for cancellation and shared-restore cleanup if existing tests need stronger assertions.
  - `internal/server/metadata_test.go`, `internal/server/server_test.go`, or a new focused `internal/server/read_verification_test.go` through `server.Register`.
- Avoid new packages. If helper extraction is useful, keep it in the package that owns the behavior: Block parsing in `internal/block`, Projection Resolution in `internal/index`, Shard orchestration in `internal/shard`, and transport mapping in `internal/server`.

### Testing Notes

- Reuse existing fixtures before adding new ones: `openTestShard`, `openUploadTestShard`, `stageEvictedConfirmedBlock`, `server.Register`, `startTestServer`, Block corruption helpers, and local fake Backends.
- Tests must assert exact metadata, not just success: content type, size, SHA-256, and `CreatedAt`.
- Server tests for read corruption should call `Recv` on the stream and assert the first receive returns `codes.DataLoss`; no successful response should be observed before the error.
- For cancellation, prefer a blocking Backend or restore fixture with explicit channels. Do not use arbitrary sleeps.
- If checking goroutine cleanup, use bounded polling with a clear failure message or assert completion of owned channels/calls rather than relying only on global goroutine counts.
- For redaction evidence, use the Story 1.1/1.2 pattern: captured logs, OTel spans, and metric attributes must omit raw Transaction IDs, Document names, Backend keys, local paths, and Document bytes.
- If a test corrupts files, corrupt a temp-dir fixture only. Do not mutate checked-in fixtures or shared paths.

### Previous Story Intelligence

- Story 1.2 implemented exact replay as same `(transaction_id, document_name)`, same content type, same plaintext SHA-256, and same total byte size. Story 1.3 read/head tests should verify the same committed metadata is what reads expose.
- Story 1.2 review fixed conflict error redaction. Carry that forward: read/head `DATA_LOSS` errors and telemetry must not include raw Document identity, local paths, Backend keys, or bytes.
- Story 1.2 review fixed duplicate error paths that returned before draining bodies. The analogous read-path risk is returning success metadata before verification or cancellation outcome is known.
- Story 1.2 hardened duplicate metadata corruption with `store.ErrDataLoss`. Story 1.3 should use the same fail-closed standard for visible read/head metadata corruption.
- Story 1.1 and Story 1.2 server tests deliberately use `server.Register`; Story 1.3 server tests must not bypass registration when proving public gRPC behavior.
- Story 1.1 found that tests must assert exact persisted metadata, not just successful calls. Do the same for read/head metadata.

### Git Intelligence

- Recent commits show narrow, test-backed Shard/security changes: `d970de3 fix(security): enforce peer Shard scope`, `4013b66 fix(security): harden public API and deploy controls`, `69ad47f feat(shard): coordinate upload pressure pause ownership`, `954bfda feat(shard): harden upload confirmation replay`, and `e0c72ce feat(shard): add upload outbox event boundary`.
- The pattern to follow is characterization test first, narrow Shard/server changes, central Store error mapping, and full local gates before review.

### Technical Research Notes

- Repo-pinned versions remain the authority: Go `1.26.4`, gRPC `v1.81.1`, Pebble `v1.1.5`, OpenTelemetry `v1.44.0`. No dependency upgrade or registry search is in scope.
- Official Go `context` docs define Context as carrying deadlines and cancellation signals across API boundaries, and warn that `CancelFunc` must be called to release resources. Source: https://pkg.go.dev/context
- Official Go `context.WithoutCancel` docs state that it detaches cancellation while returning no deadline or error unless a new deadline is applied. Existing restore code re-applies the caller deadline; preserve that if modifying restore cancellation. Source: https://pkg.go.dev/context#WithoutCancel
- Official gRPC status docs define `CANCELLED`, `DEADLINE_EXCEEDED`, `UNAVAILABLE`, and `DATA_LOSS`; `DATA_LOSS` is the correct public code for unrecoverable data loss or corruption. Source: https://grpc.io/docs/guides/status-codes/
- Official gRPC-Go `status` package for repo-pinned `v1.81.1` remains the central transport-status API. Keep Store/core packages free of `grpc/status` imports. Source: https://pkg.go.dev/google.golang.org/grpc/status
- Official Go `crypto/sha256` docs define SHA-256 digest size as 32 bytes. Store, Block, and Index code should keep raw `[32]byte` digests internally and render hex only at the API boundary. Source: https://pkg.go.dev/crypto/sha256

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 1 and Story 1.3 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-3 and NFR-1 through NFR-7.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - source of truth, package boundaries, authority, error/redaction/evidence patterns.
- `_bmad-output/implementation-artifacts/1-2-immutable-replay-and-conflict-handling.md` - previous story learnings and review fixes.
- `CONTEXT.md` - glossary, V2 API contract, all-or-error read semantics, Projection model, read state machine, and safety invariants.
- `docs/adr/0002-dual-checksum-architecture.md` - Frame CRC and Document SHA-256 verification.
- `docs/adr/0003-mirror-block-layout.md` - Block physical refs and mirrored layout.
- `docs/adr/0004-lean-pebble-with-metadata-tiering.md` - `.idx` metadata read authority and fail-closed metadata semantics.
- `docs/adr/0014-projection-resolution-boundary.md` - strict vs lenient Projection Resolution semantics.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - metadata-only reads for evicted Blocks and restore boundaries.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - future routing boundary and no hidden Shard ID assumptions.
- `docs/go-style-guide.md` - Go style, errors, tests, and package conventions.
- `https://pkg.go.dev/context` - official Go context cancellation reference.
- `https://grpc.io/docs/guides/status-codes/` - official gRPC status code semantics.
- `https://pkg.go.dev/google.golang.org/grpc/status` - official gRPC-Go status API for repo-pinned module.
- `https://pkg.go.dev/crypto/sha256` - official Go SHA-256 reference.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Implementation Plan

- Add read-path characterization tests before production changes for exact Shard metadata, registered gRPC success streaming, fail-closed corruption, and cancellation-before-send behavior.
- Keep Block, Frame, `.idx`, and Document SHA verification behind `store.Store` / `internal/shard`; use `internal/server` only for validation, status mapping, and streaming already-verified Store output.
- Add minimal transport cancellation checks before Store access and before public stream sends without changing storage format, protobuf messages, routing identity, or Block reader behavior.

### Debug Log References

- FAIL (RED): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentCanceledContextReturnsBeforeStoreOrSend|TestGRPCReadDocumentCorruptBlockReturnsDataLossBeforeAnyMessage' -count=1` - `TestReadDocumentCanceledContextReturnsBeforeStoreOrSend` returned `OK`, wanted `Canceled`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentCanceledContextReturnsBeforeStoreOrSend|TestGRPCReadDocumentCorruptBlockReturnsDataLossBeforeAnyMessage' -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/shard -run 'TestGRPCReadDocumentStreamsVerifiedMetadataThenChunks|TestGRPCReadDocumentCorruptBlockReturnsDataLossBeforeAnyMessage|TestReadDocumentCanceledContextReturnsBeforeStoreOrSend|TestReadDocumentReturnsCommittedMetadataAndBytes|TestReadDocumentCorruptBlockPayloadFailsClosedWithoutReader|TestReadDocumentTruncatedFrameFailsClosedWithoutReader|TestHeadAndReadDocumentCorruptIndexFailClosed' -count=1`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/index ./internal/server ./internal/shard` - first run hit existing intermittent `TestWriteDocumentPeerDurabilityQuorumAllowsOnePeerFailure`; isolated rerun passed and second full package run passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- PASS: `git diff --check`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/server ./internal/shard -run 'TestReadDocumentFromBlockRejectsCorruptHeader|TestReadDocumentTwoPassRejectsFrameSequenceMismatch|TestReadDocumentCorruptBlockHeaderFailsClosedWithoutReader|TestReadDocumentFrameSequenceMismatchFailsClosedWithoutReader|TestReadDocumentCanceledContextReturnsBeforeStoreOrSend|TestReadDocumentCancellationDuringStoreReturnsCanceledWithoutSend|TestReadDocumentReaderContextErrorPreservesCanceledStatus|TestReadDocumentSendStatusErrorIsNotRemappedToInternal' -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/index ./internal/server ./internal/shard`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/block ./internal/shard ./internal/spike ./test/integration -run 'TestReadDocumentTwoPassRejectsFrameSequenceMismatch|TestReadDocumentFrameSequenceMismatchFailsClosedWithoutReader|TestLargeDocumentExceedsSealThreshold|TestLargeDocument128MiB' -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- PASS: `git diff --check`.
- PASS: `ruby -ryaml -e 'YAML.safe_load_file("_bmad-output/implementation-artifacts/sprint-status.yaml", permitted_classes: [Date, Time], aliases: true); puts "sprint-status.yaml OK"'`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added focused Shard read verification tests for exact committed metadata (`Name`, content type, size, SHA-256, `CreatedAt`) and exact committed bytes.
- Added registered gRPC read verification tests proving metadata is the first successful message only after Store verification succeeds, followed by chunks matching the committed Document.
- Added fail-closed corruption tests for corrupt Block payload, truncated Frame data, corrupt `.idx` metadata, and registered gRPC `DATA_LOSS` with zero successful `ReadDocumentResponse` messages before the error.
- Added deterministic cancellation coverage for an already-canceled stream context: `ReadDocument` returns context-derived `Canceled`, does not call Store, and emits zero responses.
- Changed boundary list: `internal/server` transport only, new `internal/server` tests, new `internal/shard` tests. `internal/block`, `internal/index`, `internal/store`, public protobufs, Block/Frame layout, and routing identity were not changed.
- Typed-error mapping preserved: Store corruption remains `store.ErrDataLoss`; public transport maps it through existing `mapStoreError` to `codes.DataLoss`; missing and unavailable paths were not changed.
- Cancellation fixture: package-local fake Store plus recording server stream with a canceled context; no sleeps, goroutines, channels, or shared state added for this case.
- Corruption fixture: temp-dir Shard Blocks and `.idx` files only; helpers mutate temp data with byte flip/truncate and assert nil reader plus zero metadata on Store corruption failure.
- Redaction proof: no new logs, metrics, traces, or error messages include raw `transaction_id`, `document_name`, Backend keys, local paths, or Document bytes; existing hashed telemetry behavior and central Store error mapping remain untouched.
- Code review patches preserve context-derived and existing gRPC status errors from Store, reader, and stream-send failures instead of remapping them to `Internal`.
- Added in-progress cancellation evidence for Store, reader, and stream-send phases; Store cancellation returns `Canceled` without successful stream messages.
- Added explicit Block header verification before returning read bytes and Frame sequence/order checks against the index `FrameCount`; existing exact-frame-multiple documents remain valid because reads do not require a final-frame flag when the index count is authoritative.

### File List

- `_bmad-output/implementation-artifacts/1-3-verified-read-and-metadata-inspection.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/block/reader.go`
- `internal/block/twopass_test.go`
- `internal/server/server.go`
- `internal/server/read_cancellation_test.go`
- `internal/server/read_verification_test.go`
- `internal/shard/shard.go`
- `internal/shard/read_verification_test.go`

### Change Log

- 2026-06-11: Created Story 1.3 Verified Read and Metadata Inspection context package and marked it ready for development.
- 2026-06-11: Implemented Story 1.3 verified read and metadata inspection, added fail-closed/cancellation coverage, and moved story to review.
- 2026-06-11: Applied code review patches for status preservation, in-progress cancellation, and explicit Block header/Frame order read verification; moved story to done.
