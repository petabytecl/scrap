---
baseline_commit: 27e0cde
---

# Story 2.7: Bound Peer ReplicateDocument Input Before Side Effects

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want peer Document replication to enforce the same input bounds as public writes before allocation-heavy work or side effects,
so that a buggy or compromised peer cannot pressure memory, disk, or replica state outside the Document contract.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-4, FR-5, NFR-2, NFR-3, NFR-8.
- Release policy: any confirmed or plausible data-integrity bug is release-blocking until fixed or explicitly disproven with current evidence.
- Governing ADRs:
  - `docs/adr/0024-production-topology-and-peer-scope-policy.md`
  - `docs/adr/0026-multi-shard-release-boundary.md`
- Course-correction source: `_bmad-output/planning-artifacts/sprint-change-proposal-2026-06-14.md`.
- Prerequisites: Stories 2.1 through 2.6 are done and provide routing validation, multi-Shard composition, public Transaction routing, peer Shard-scope authorization, Shard-aware diagnostics, and Epic 2 evidence closure.
- Non-goals: no peer wire-contract change, no generated proto edits, no new Shard authority behavior, no Backend inventory authority, no release-gate reconciliation work from Story 6.8.

## Acceptance Criteria

1. **AC-2.7.1 - Invalid init rejected before side effects.** Given a peer replication init has invalid `transaction_id`, `document_name`, or `content_type`, when `ReplicateDocument` receives it, then the request is rejected before Block writer or replication sink side effects. Evidence records the validation test and changed-boundary list.
2. **AC-2.7.2 - Oversized chunk rejected before buffering.** Given a peer stream sends a chunk larger than `MaxClientChunkBytes`, when the chunk is received, then replication fails with a bounded typed error before buffering the full Document. Evidence records the peer transport test.
3. **AC-2.7.3 - Total Document size rejected without accepted state.** Given total replicated bytes exceed `MaxDocumentBytes`, when the stream continues, then replication fails without publishing accepted state. Evidence proves no committed metadata or visible Document is created and no local Block writer/sink side effect is accepted by the peer boundary.
4. **AC-2.7.4 - Replication sink path bounded.** Given the `replicationSink` path is configured, when oversized input arrives, then input is bounded before `bytes.Buffer` can grow without limit. Evidence covers the sink path.
5. **AC-2.7.5 - Peer boundary preserved.** Given the fix is reviewed, when package boundaries are checked, then `internal/peer` remains a transport boundary connected to Shard behavior through narrow interfaces. Evidence records `go test ./internal/peer/... ./internal/cmd/...`.

## Tasks / Subtasks

- [x] Add peer-side init validation before any replication side effect. (AC: 1, 5)
  - [x] In `internal/peer/server.go`, validate `ReplicateDocumentInit` immediately after `receiveReplicateDocumentInit` and before `authorizePeerShardAfterPrecheck`, `replicateToSink`, goroutine launch, or `getOrCreateBlock`.
  - [x] Reuse `internal/store.ValidateWriteMetadata(init.GetTransactionId(), init.GetDocumentName(), init.GetContentType(), "", "")`; do not invent `tenant_id` or idempotency fields on the peer wire contract.
  - [x] Map invalid metadata to `codes.InvalidArgument` while preserving `errors.Is(err, store.ErrInvalidArgument)` where practical.
  - [x] Add table-driven invalid-init tests for missing, oversized, and control-character values for Transaction ID, Document name, and content type.
  - [x] Assert invalid init never calls `ReplicationSink.AppendReplicatedDocument`, never creates `.blk`/`.idx` files, and leaves `Server.writers` empty in the local Block writer path.

- [x] Add peer-side chunk and total-byte bounds for both replication paths. (AC: 2, 3, 4)
  - [x] Apply `store.ValidateClientChunk` to every `chunk_data` message before writing it to a pipe or `bytes.Buffer`.
  - [x] Track cumulative received bytes and fail with `store.ResourceExhaustedReasonDocumentTooLarge` when the stream exceeds `store.MaxDocumentBytes`.
  - [x] Ensure chunk-too-large and document-too-large outcomes return `codes.ResourceExhausted` with bounded messages and no raw Document identifiers.
  - [x] Ensure zero-byte replicated Documents do not become accepted state. The public write path rejects zero-byte Documents through `NewDocumentBodyReader`; peer replication must not accept an empty body as a valid replicated Document.
  - [x] Keep legitimate leader-generated chunks working. `internal/shard.splitReplicationChunks` currently emits 64 KiB chunks, well below `MaxClientChunkBytes`.

- [x] Remove unbounded sink buffering. (AC: 2, 3, 4, 5)
  - [x] Replace the current unbounded `bytes.Buffer` growth in `replicateToSink` with bounded accumulation or a bounded reader strategy that returns an explicit over-limit error instead of silently truncating.
  - [x] Do not rely on `io.LimitReader` alone; `io.LimitReader` returns EOF at the limit, so over-limit input needs an additional probe or explicit running-total check.
  - [x] Call `ReplicationSink.AppendReplicatedDocument` only after init and body bounds have passed.
  - [x] Keep `ReplicationSink` as the narrow consumer-owned interface: `AppendReplicatedDocument(ctx, init, body)`.

- [x] Preserve local Block writer behavior while delaying side effects until validation passes. (AC: 1, 2, 3, 5)
  - [x] Do not call `getOrCreateBlock`, create `block.Writer`, create `block.IndexWriter`, or launch append goroutine until metadata is valid.
  - [x] In the local path, reject oversized chunks before `pw.Write`.
  - [x] On receive/write failure after a writer has started, preserve existing cleanup behavior: close the pipe with error, wait for append goroutine completion, and return a typed bounded status.
  - [x] Keep `Close()` idempotent and writer cleanup behavior unchanged for successful peer replication tests.

- [x] Add regression tests in `internal/peer`. (AC: 1-5)
  - [x] Add a focused test file such as `internal/peer/replicate_bounds_test.go`.
  - [x] Use same-package tests where white-box assertions are needed for `Server.writers` or helper streams.
  - [x] Reuse local test doubles already present in `authorization_test.go` where possible: `replicateDocumentStream`, `recordingReplicationSink`, and peer auth helpers.
  - [x] Add sink-path tests proving sink call count stays zero on invalid init, oversized chunk, over-limit total bytes, and empty body.
  - [x] Add local-path tests proving block directory remains empty and `Server.writers` remains empty for invalid init and pre-writer body-bound failures.
  - [x] Keep existing happy-path tests passing: `TestReplicateDocumentSinglePeer`, `TestFanOutQuorum`, and multi-Shard authorization tests.

- [x] Add focused verification and story evidence. (AC: 1-5)
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'ReplicateDocument|PeerServerDeniesUnauthorizedShardBeforeReplication' -count=1`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... ./internal/cmd/... -count=1`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
  - [x] Run `git diff --check`.
  - [x] Record exact commands, results, changed-boundary list, and any skipped broader gates in this story before review.

## Review Findings

- [x] [Review][Patch] Streamed peer `ReplicateDocument` input bounds — resolved as Option A (streaming) + `init.TotalBytes` pre-check. Replaced full-Document temp-file buffering (`os.CreateTemp`) with `io.Pipe` streaming to the consumer: the first body chunk is peeked and size-validated before the consumer starts (empty body and oversized first chunk fail closed with zero sink/local side effects), a declared `init.TotalBytes > MaxDocumentBytes` is rejected up front, and each subsequent chunk plus the running total is bounded mid-stream. This removes the tmpfs/RAM buffer, the ~`MaxConcurrentWrites`×128 MiB aggregate temp-disk vector, the orphaned-temp-file/reaper concern, the swallowed cleanup error, and the buffer-then-append latency regression, and restores NFR-2 streaming. A `recover()` guard on the consumer goroutine prevents a blocked producer (CONTEXT.md lesson #3). Verified: `go test ./internal/peer/... ./internal/cmd/...` and `make check`.
- [x] [Review][Defer] Local (no-sink) `ReplicateDocument` path appends before SHA-256 verification with no rollback on mismatch [internal/peer/server.go:replicateToLocalBlock] — deferred, pre-existing (old code appended then compared); production sink path validates and aborts via `internal/shard.validateReplicatedAppend`.
- [x] [Review][Defer] SHA-256 verification skipped when `init.Sha256` length != 32 [internal/peer/server.go:replicateToLocalBlock] — deferred, pre-existing condition; production sink path enforces SHA-256 downstream in `internal/shard`.
- [x] [Review][Defer] Internal gRPC error messages embed raw os/dependency errors (possible temp-path leak) on the peer surface [internal/peer/server.go] — deferred, partially pre-existing peer transport error-mapping hardening on an authenticated peer surface.
- [x] [Review][Defer] Context cancellation/deadline and `ENOSPC` during peer receive map to `codes.Internal` instead of `Canceled`/`DeadlineExceeded`/`ResourceExhausted` [internal/peer/server.go:receive] — deferred, pre-existing peer transport mapping gap (also affects other peer streams).
- [x] [Review][Resolved] Over-limit unit test no longer writes ~128 MiB to disk — the over-limit case now uses the up-front `init.TotalBytes` pre-check (`TestReplicateDocumentRejectsDeclaredOverLimitBeforeSideEffects`, no chunks streamed) plus a fast pure-function bound test (`TestValidateReplicateDocumentChunkBounds`).

## Dev Notes

### Current State

- `internal/peer/server.go` owns peer gRPC ingress. `ReplicateDocument` currently checks peer auth, receives the init message, checks Shard authorization, then either calls `replicateToSink` or writes directly to local Block files.
- `receiveReplicateDocumentInit` currently only checks that the first message has an `init` part. It does not validate `transaction_id`, `document_name`, or `content_type`.
- `replicateToSink` currently buffers all chunk bytes into a `bytes.Buffer` before calling `ReplicationSink.AppendReplicatedDocument`. There is no per-chunk or total Document bound in this path.
- The local Block writer path starts a goroutine that calls `getOrCreateBlock` and `block.Writer.AppendDocument`; chunk receive then streams bytes through `recvChunks`. This can create `.blk` and `.idx` files before chunk bounds are enforced unless the implementation delays side effects or validates before launch.
- `recvChunks` currently writes any non-empty chunk to the pipe and does not call `store.ValidateClientChunk` or track cumulative bytes.
- `internal/store/validation.go` defines the public write contract limits and validation helpers:
  - `MaxTransactionIDBytes = 256`
  - `MaxDocumentNameBytes = 512`
  - `MaxContentTypeBytes = 255`
  - `MaxClientChunkBytes = 1 << 20`
  - `MaxDocumentBytes = 128 << 20`
  - `ValidateWriteMetadata`
  - `ValidateClientChunk`
  - `NewDocumentBodyReader`
- The public write path in `internal/server/server.go` is the behavioral reference: validate metadata before body work, reject oversized chunks, track total Document size, and map invalid input to typed gRPC status without logging client-driven errors as server failures.
- Production `scrapd` wires `peer.WithReplicationSink(shardSetReplicationSink{shards: shards})` in `internal/cmd/app.go`, so the sink path is the production path for peer replication.
- `internal/cmd/shard_set.go` dispatches `AppendReplicatedDocument` by `init.GetShardId()` and should remain a thin router. Story 2.7 should not move validation into `internal/cmd`.
- `internal/shard/replication.go` owns follower append semantics after the peer boundary. `Shard.AppendReplicatedDocument` validates offset, openlog attempt, frame count, total bytes, and SHA-256 after append. Story 2.7 must prevent oversized peer input from reaching this layer rather than relying on Shard cleanup after side effects.
- Existing peer authorization tests already prove wrong-Shard denial before Raft route, replication sink, local files, rebuild/scrub, and Block transfer. Story 2.7 should mirror that no-side-effect assertion style for invalid metadata and oversized body cases.

### What This Story Changes

- Add peer transport validation for replicated Document metadata and byte limits.
- Apply the same public write bounds to `ReplicateDocument` without changing `proto/scrap/v1/peer.proto`.
- Bound the `replicationSink` path before `bytes.Buffer` can grow without limit.
- Reject invalid or oversized replicated input before Block writer, index writer, openlog, Shard sink, or accepted-state side effects.
- Add regression tests proving no side effect occurs on invalid or oversized input.

### What Must Be Preserved

- `internal/peer` remains a transport boundary. It may import `internal/store` for shared domain validation and limits, but it must not import `internal/shard` internals.
- `ReplicationSink` remains narrow and consumer-defined in `internal/peer`.
- Shard ownership remains unchanged: `internal/shard` owns follower append, openlog, Block paths, Raft authority, and post-append validation.
- `internal/cmd` remains the composition root and Shard-set router; do not add validation policy there unless only wiring is required.
- Peer Shard-scope authorization from ADR 0024 must still run before Shard-side effects.
- No `proto/`, `gen/`, Block/Frame layout, Backend object format, or Raft command shape changes are expected.
- Do not treat Backend keys, local files, peer addresses, certificate presence, metrics, or evidence artifacts as Shard authority.
- Do not add dependencies or assertion libraries.

### Implementation Guidance

- Prefer a small peer-local helper set in `internal/peer/server.go`:
  - `validateReplicateDocumentInit(init *scrapv1.ReplicateDocumentInit) error`
  - `validateReplicateDocumentChunk(chunk []byte, totalSoFar int64) (int64, error)` or equivalent
  - `replicateDocumentStatus(err error) error` or equivalent mapper if needed
- Reuse store sentinels and reasons so dev/review can assert `errors.Is` and status code:
  - `store.ErrInvalidArgument`
  - `store.ErrResourceExhausted`
  - `store.ResourceExhaustedReasonChunkTooLarge`
  - `store.ResourceExhaustedReasonDocumentTooLarge`
- Keep error messages bounded. They may name fields such as `transaction_id`, `document_name`, `content_type`, chunk too large, or Document too large, but must not include raw values.
- If adding `errdetails.ErrorInfo` for peer errors, match public reason semantics where practical, but do not make this story depend on a broad transport refactor.
- Avoid `io.ReadAll` in `internal/peer` for incoming replicated Documents. If buffering remains necessary for the sink path, it must be bounded by explicit checks before each write.
- A pure `io.LimitReader` wrapper is insufficient because it masks over-limit input as EOF unless paired with a probe. Prefer explicit per-chunk running-total checks in the receive loop.
- For the local Block writer path, the safest approach is to validate init before goroutine launch and validate each chunk before `pw.Write`. Avoid creating `.blk`/`.idx` until the implementation can guarantee the failure case being tested has passed pre-side-effect validation.
- Legitimate replication chunks from the leader are 64 KiB (`internal/shard/replication.go`), so `MaxClientChunkBytes` should not reject normal traffic.
- Keep client-driven malformed input out of server `ERROR` logs. Return typed gRPC errors; add tests for status codes rather than log severity unless this story changes logging.

### Project Structure Notes

Likely update:

- `internal/peer/server.go` - add metadata/body validation and bounded sink/local receive paths.
- `internal/peer/replicate_bounds_test.go` or existing peer test files - add focused regression tests for AC-2.7.1 through AC-2.7.4.
- `internal/peer/authorization_test.go` - may be reused or minimally extended if existing same-package helpers are the best fit.
- `_bmad-output/implementation-artifacts/2-7-bound-peer-replicatedocument-input-before-side-effects.md` - record implementation evidence during dev.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - story status updates during workflow.

Likely avoid:

- `proto/scrap/v1/peer.proto` and `gen/go/scrap/v1/*` - no wire change is needed.
- `internal/shard/replication.go` - do not push transport-bound validation into Shard unless a narrowly scoped defense-in-depth check is justified and tested.
- `internal/cmd/shard_set.go` - dispatch should remain simple Shard routing by `shard_id`.
- `internal/backend/*` - Backend is not involved in peer ingress validation.
- `docs/adr/` - no ADR is expected unless implementation changes wire/storage/security contracts or package ownership.

### Previous Story Intelligence

- Story 2.4 established the required no-side-effect style for peer failures: wrong-Shard peer RPCs are denied before Raft routing, replication sink calls, local Block files, rebuild/scrub, or Block transfer.
- Story 2.6 added multi-Shard deployed evidence and kept peer scrub/rebuild fail-closed until peer scrub/rebuild RPCs carry Shard ID. Do not use Story 2.7 to change scrub/rebuild peer wire scope.
- Story 2.6 review deferred CI runner migration as unrelated; do not mix CI runner evidence into this peer input-bound story.
- Story 2.6 final gates included `make package-boundaries`, `make check`, and Tier 2 evidence. Story 2.7 is narrower and should use focused peer/cmd gates first, with broader gates only if implementation touches shared behavior.

### Git Intelligence Summary

Recent commits show the current release-blocker direction:

- `27e0cde fix(block): fail closed on missing document digest` changed `internal/block/reader.go`, `internal/block/verify.go`, and added zero-SHA256 tests. Pattern: release-blocking integrity bugs get focused red/green tests and evidence updates.
- `c9caa90 chore: finalize SCRAP naming and local gates` touched release artifacts, ADR naming, `internal/cmd/app.go`, `internal/shard/scrub_coordinator.go`, scripts, and broad gates. Pattern: release evidence must stay synchronized with actual status.
- `6231a9d docs(release): flip V2 final gate decision to PASS` is now superseded in spirit by the course correction: contradictory release evidence is a blocker handled by Story 6.8, not this story.
- `8f4dce8 fix(release): stabilize release gate blockers` touched `internal/shard/replication.go`, restore, evidencebundle, and E2E tests. Pattern: flaky or unsafe data-integrity paths are fixed with focused tests before final evidence.
- `13d23dc test(e2e): Shard-scope upload pending-blocks wait` fixed #437-related E2E flake by scoping waits to Shard behavior. Pattern: avoid global or ambiguous waits when Shard scope matters.

### Latest Technical Information

- Repo-pinned versions remain authoritative: Go `1.26.4` in `go.mod`/`tools.go.mod`, gRPC `v1.81.1`, protobuf `v1.36.11`, Buf `v1.70.0`.
- gRPC-Go exposes receive-size controls such as `grpc.MaxRecvMsgSize` and call options such as `grpc.MaxCallRecvMsgSize`; default receive size is documented as 4 MiB. These transport limits are helpful but do not replace application-layer `ReplicateDocument` validation because Story 2.7 requires public write-style bounds and typed failures before side effects.
- Go `io.LimitReader` returns a reader that stops with EOF after N bytes. Do not use it alone for over-limit detection; pair any limited reader with explicit over-limit probing or use running totals while receiving chunks.
- No dependency upgrades, new frameworks, or new libraries are required.

### Testing Requirements

- Use standard `testing`; no assertion libraries.
- Prefer same-package peer tests for white-box assertions on `Server.writers` and existing stream test doubles.
- Tests must assert both the returned error and absence of side effects.
- Cover both paths:
  - sink path: `WithReplicationSink(recordingReplicationSink{})`
  - local path: no sink, local Block writer fallback
- Suggested test names:
  - `TestReplicateDocumentRejectsInvalidInitBeforeSideEffects`
  - `TestReplicateDocumentRejectsOversizedChunkBeforeBuffering`
  - `TestReplicateDocumentRejectsDocumentOverLimitBeforeAcceptedState`
  - `TestReplicateDocumentSinkPathBoundsInputBeforeAppend`
  - `TestReplicateDocumentRejectsEmptyBodyBeforeAcceptedState`
- Suggested focused verification:

```sh
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'ReplicateDocument|PeerServerDeniesUnauthorizedShardBeforeReplication' -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... ./internal/cmd/... -count=1
env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries
git diff --check
```

## References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.7 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-4, FR-5, NFR-2, NFR-3, NFR-8, and release rules.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - V2 package boundaries, peer Shard-scope policy, and evidence requirements.
- `_bmad-output/planning-artifacts/sprint-change-proposal-2026-06-14.md` - approved data-integrity release-blocking story split.
- `_bmad-output/project-context.md` - package boundaries, testing rules, redaction rules, and workflow rules.
- `CONTEXT.md` - glossary and storage gateway invariants.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope authorization before Raft, byte replication sink, or Block transfer side effects.
- `docs/adr/0026-multi-shard-release-boundary.md` - multi-Shard release boundary and `internal/peer` authorized Shard set source.
- `docs/go-style-guide.md` - Go design, errors, concurrency, tests, and logging rules.
- `_bmad-output/implementation-artifacts/2-4-peer-rpc-shard-scope-authorization.md` - prior wrong-Shard no-side-effect evidence.
- `_bmad-output/implementation-artifacts/2-6-multi-shard-evidence-closure.md` - previous story learnings and Epic 2 closure evidence.
- `internal/peer/server.go` - `ReplicateDocument`, `receiveReplicateDocumentInit`, `replicateToSink`, `recvChunks`, and Shard-scope authorization.
- `internal/peer/peer_test.go` - current happy-path peer replication and test server helper.
- `internal/peer/authorization_test.go` - peer auth and wrong-Shard no-side-effect test patterns.
- `internal/peer/audit_ratelimit_test.go` - peer audit/rate-limit and redaction test patterns.
- `internal/server/server.go` - public write-path metadata, chunk, total-size validation, and transport error mapping reference.
- `internal/store/validation.go` - public write validation helpers and limits.
- `internal/shard/replication.go` - leader chunk splitting and follower append behavior.
- `internal/cmd/shard_set.go` - production replication sink dispatch by Shard ID.
- `proto/scrap/v1/peer.proto` - current peer wire contract; expected unchanged for this story.

## Dev Agent Record

### Agent Model Used

GPT-5.5

### Debug Log References

- CREATE-STORY: auto-discovered first backlog story from `_bmad-output/implementation-artifacts/sprint-status.yaml`: `2-7-bound-peer-replicatedocument-input-before-side-effects`.
- CREATE-STORY: resolved workflow customization with no activation hooks and persistent facts from `_bmad-output/project-context.md`.
- CREATE-STORY: loaded current V2 PRD, master architecture, epics, approved sprint change proposal, ADR 0024, ADR 0026, Go style guide, previous Story 2.6, and peer implementation/test files.
- CREATE-STORY: read current update targets `internal/peer/server.go`, peer tests, `internal/store/validation.go`, `internal/shard/replication.go`, and `internal/cmd/shard_set.go`.
- CREATE-STORY: used a read-only subagent to independently inspect peer replication guardrails; recommendation matched the story direction.
- RESEARCH: checked current gRPC-Go receive message limit APIs and Go `io.LimitReader` behavior; no dependency changes required.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run TestReplicateDocumentRejectsInvalidInitBeforeSideEffects -count=1` failed because invalid peer init metadata returned success in sink and local paths.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run TestReplicateDocumentRejectsInvalidInitBeforeSideEffects -count=1` passed after peer init validation and store-error status mapping.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'TestReplicateDocumentRejects(OversizedChunk|DocumentOverLimit|EmptyBody)' -count=1` failed because oversized chunks, over-limit Documents, and empty bodies returned success.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'TestReplicateDocumentRejects(OversizedChunk|DocumentOverLimit|EmptyBody)' -count=1` passed after bounded receive validation.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -count=1` passed after updating existing wrong-Shard authorization tests to use valid metadata.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... -run 'ReplicateDocument|PeerServerDeniesUnauthorizedShardBeforeReplication' -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer/... ./internal/cmd/... -count=1`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS: `git diff --check`.
- FAIL/PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` first failed on lint shape (`ReplicateDocument` cyclomatic complexity, `receiveReplicateDocumentBody` cognitive complexity, unused old `recvChunks`); after refactoring helpers and removing the unused path, the rerun passed.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Created Story 2.7 as a release-blocking peer input-bound story and set status to ready-for-dev.
- Scoped implementation to peer transport validation before side effects.
- Explicitly preserved peer wire contract, Shard authority, Backend authority, and package boundaries.
- Included sink-path and local-path side-effect assertions so implementation cannot pass by returning errors after accepting state.
- Implemented peer init validation before Shard authorization, sink dispatch, local Block writer creation, or append goroutine launch.
- Implemented a shared bounded peer receive path that validates per-chunk size, total Document size, and empty body before sink dispatch or local Block writer creation.
- Replaced unbounded sink buffering with a bounded temporary reader that is deleted after use, avoiding unbounded heap growth while preserving the `ReplicationSink` interface.
- Updated existing wrong-Shard peer tests so they continue testing authorization with valid replicated Document metadata.
- Refactored peer replication helper shape to satisfy lint complexity gates without changing behavior.
- Verified broad regression with `make check`, including lint, package-boundaries, buf lint/generate diff, `go test ./...`, race tests, integration tests, and builds.

### File List

- `_bmad-output/implementation-artifacts/2-7-bound-peer-replicatedocument-input-before-side-effects.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/peer/replicate_bounds_test.go`
- `internal/peer/audit_ratelimit_test.go`
- `internal/peer/authorization_test.go`
- `internal/peer/server.go`

## Change Log

- 2026-06-15: Implemented bounded peer `ReplicateDocument` init/body validation, added side-effect regression tests, updated story status to review.
- 2026-06-15: Code review (3-layer adversarial). Resolved the one decision-needed finding by refactoring to `io.Pipe` streaming (Option A) with an `init.TotalBytes` pre-check and peek-first-chunk, removing temp-file buffering. 4 pre-existing items deferred (local-path SHA rollback, wrong-length SHA skip, peer error-text mapping, ctx/ENOSPC mapping); 6 dismissed. `make check` green. Status → done.
