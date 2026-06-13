---
baseline_commit: e0c72ce5f3649582293c9a331bc940a75233d0a6
---

# Story 4.2: Replay-Safe Upload Confirmation and Backend Failure Handling

Status: review

## Story

As a storage engineer,
I want Upload Outbox processing to be replay-safe and failure-aware,
so that sealed Blocks converge to confirmed upload state without duplicate confirmation, lost retry, or corrupted lifecycle evidence.

## Traceability

- Source: Epic 4 in `_bmad-output/planning-artifacts/epics.md`.
- Previous story: `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md`.
- Requirements: NFR7, NFR8, NFR9, NFR12, NFR13.
- Governing ADR: `docs/adr/0010-upload-outbox-via-raft.md`.
- Related implementation: `internal/shard/upload_outbox_events.go`, `internal/shard/upload.go`, `internal/shard/upload_controller.go`, `internal/shard/block_upload_lifecycle.go`, `internal/shard/upload_obligations.go`, `internal/index/upload_outbox.go`, `internal/index/confirmed_upload_catalog.go`.
- GitHub issue: not assigned in the current epic artifact. Before PR, cite this BMAD story and ADR 0010, or create/link an execution issue.

## Acceptance Criteria

1. Given a Block sealed event is delivered once, delivered more than once, or replayed after restart, when Upload Outbox processing evaluates the event, then the Block becomes eligible for upload exactly according to durable Upload Outbox state, and duplicate sealed events do not create duplicate pending work, duplicate Backend uploads, or inconsistent pressure state.
2. Given an upload succeeds but `ConfirmUpload` proposal, Raft apply, or process completion is interrupted, when the system restarts or replays upload confirmation handling, then confirmation processing is idempotent, and duplicate confirmations do not corrupt the Confirmed Upload Catalog, Pebble Projection, Local Block Lifecycle, or upload pressure state.
3. Given the Backend returns timeout, cancellation, transient failure, partial failure, permanent failure, or success-after-retry, when Upload Outbox processing runs, then retry eligibility, backoff/concurrency behavior, pressure state, and observable error state remain bounded and testable, and failed Backend upload never becomes write ACK authority or Document visibility authority.
4. Given startup recovery finds pending Upload Outbox entries, when the Shard leader resumes upload work, then pending sealed Blocks are retried or skipped according to durable state and local Block availability, and missing local Blocks, unexpected local loss, metadata loss, or Block Quarantine state produce fail-closed observable outcomes rather than silent success.
5. Given replay/failure tests run, when evidence is collected, then tests prove sealed event replay, upload confirmation replay, Backend failure, restart recovery, cancellation, and concurrency limits, and logs/metrics/evidence avoid raw Document identifiers, Backend object keys, raw paths, sensitive peer data, or unbounded error strings.

## Tasks / Subtasks

- [x] Add replay-focused Upload Outbox tests before changing behavior. (AC: 1, 2)
  - [x] Cover duplicate `Block sealed` events through `uploadOutbox.ApplyBlockSealed` and assert one pending Upload Outbox entry with stable size/generation semantics.
  - [x] Cover duplicate/replayed `upload confirmed` events through `uploadOutbox.ApplyUploadConfirmed` and assert stable Confirmed Upload Catalog state plus no pending Upload Outbox entry.
  - [x] Cover stale upload confirmation generation and confirm the newer pending or confirmed generation remains authoritative.
  - [x] Cover pressure stats after duplicate sealed/replayed confirm so duplicate events do not double-count pending bytes or Blocks.
- [x] Characterize interrupted confirmation proposal and restart recovery. (AC: 2, 4)
  - [x] Cover Backend `.blk` and `.idx` success followed by failed `ConfirmUpload` proposal; assert pending Upload Outbox state remains and no Confirmed Upload Catalog row is created.
  - [x] Cover success-after-retry for the same pending Block and assert exactly one committed confirmation outcome.
  - [x] Cover process restart with pending Upload Outbox entries using a real Shard reopen path, not only a controller fake.
  - [x] Keep `ConfirmUpload` as the only outbound Raft authority write; do not introduce direct Pebble or Backend authority shortcuts.
- [x] Expand Backend failure and cancellation behavior coverage. (AC: 3, 5)
  - [x] Cover timeout or context cancellation during Backend upload/retry and assert upload processing exits without false confirmation.
  - [x] Cover partial failure where `.blk` upload succeeds and `.idx` upload or verification fails; assert pending Upload Outbox state remains and Confirmed Upload Catalog is absent.
  - [x] Cover transient failure and success-after-retry without adding new dependencies or sleep-heavy tests.
  - [x] Cover permanent failure and auth/throttle behavior at the narrowest meaningful seam; preserve existing bounded retry/concurrency semantics.
- [x] Make local availability outcomes observable without changing authority. (AC: 4)
  - [x] Cover missing local Block, missing `.idx`, and Block Quarantine/local metadata-loss cases as fail-closed outcomes.
  - [x] Preserve Local Block Lifecycle as per-Member filesystem evidence only; do not let it decide Document visibility or durable upload authority.
  - [x] Do not use Backend inventory/object existence as a hot-path consistency oracle.
  - [x] If a new observable status or health signal is needed, keep it bounded and avoid raw paths or Backend object keys.
- [x] Preserve package and authority boundaries. (AC: 1-5)
  - [x] Keep implementation in focused `internal/shard` files unless a stronger package-boundary case is proven.
  - [x] Do not create `internal/common`, `shared`, `util`, or generic helpers.
  - [x] Do not import `grpc/status` or `grpc/codes` into core storage packages.
  - [x] Do not change Block/Frame layout, Backend object key format, Raft command wire shape, or Confirmed Upload Catalog schema without ADR/proto scope expansion.
- [x] Run focused and regression verification. (AC: 1-5)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/index/... internal/shard/...`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make check`

## Dev Notes

### Current State

- Story 4.1 added `internal/shard/upload_outbox_events.go`, which introduces `blockSealedEvent`, `uploadConfirmedEvent`, and the `uploadOutbox` wrapper around `blockUploadLifecycle`.
- Shard seal/apply/pressure/authority paths now route through `uploadOutboxLocked()`: `RecordBlockSealed`, `ApplyBlockSealed`, `ApplyUploadConfirmed`, `ConfirmedUploadAuthority`, `RefreshPressure`, retry helpers, and obligation forget/count helpers.
- `upload_controller.go` still owns the upload processor goroutine, Backend PUT/HEAD verification, retry/backoff, adaptive concurrency, auth pause, and pressure cache. Story 4.3 owns deeper pressure/pause/scrub-gate ownership; do not absorb that work here.
- The controller seam no longer exposes `hasLocalBlock`, `blockPath`, or `idxPath`. It uses `localUploadSource(blockID) (uploadLocalSource, bool)` and `uploadLocalSource.Open(kind)` to stream `.blk` and `.idx`.
- Existing apply tests already cover stale generation, duplicate confirmation metadata update, stale duplicate generation, confirmed authority for rebuild, and restart survival of committed authority. Reuse those patterns instead of duplicating fixtures.
- Existing Shard tests cover successful upload, empty validation token, missing local Block, missing `.idx`, and reopen after transient Backend failure. Extend those tests toward interruption/replay outcomes.

### Implementation Guardrails

- Preserve ADR 0010: `SealBlock` and `ConfirmUpload` are Raft commands, and the Upload Outbox is derived from committed `SealBlock` without matching `ConfirmUpload`.
- Backend upload remains asynchronous and outside the write ACK path. No failed/successful Backend call may decide Document visibility.
- Backend stores opaque bytes only. Do not make `internal/backend` parse Upload Outbox, Confirmed Upload Catalog, envelope, or Local Block Lifecycle metadata.
- Pebble Projection remains derived. Do not make Pebble-only state the durable authority for upload completion.
- Do not use Backend listing, HEAD of arbitrary inventory, or object existence as a consistency oracle. Backend reads should follow confirmed Block metadata or explicit upload/restore workflows.
- Local Block Lifecycle remains per-Member filesystem evidence and must not become durable upload authority.
- Avoid logs or metrics containing raw `transaction_id`, `document_name`, Backend object keys, raw file paths, sensitive peer addresses, or unbounded provider errors. Existing tests may use deterministic test keys/paths, but deployed instrumentation must stay bounded.
- No new dependencies or assertion frameworks. Use Go `testing`, local fakes, `context`, and existing `backend.Backend`/`index`/`shard` patterns.

### Project Structure Notes

- Likely touched files:
  - `internal/shard/upload_outbox_events.go`
  - `internal/shard/upload.go`
  - `internal/shard/upload_controller.go`
  - `internal/shard/block_upload_lifecycle.go`
  - `internal/shard/upload_obligations.go`
  - `internal/shard/upload_outbox_boundary_test.go`
  - `internal/shard/upload_outbox_test.go`
  - `internal/shard/upload_apply_test.go`
  - `internal/shard/upload_retry_test.go`
  - `internal/shard/upload_pressure_test.go`
  - `internal/index/upload_outbox.go`
  - `internal/index/confirmed_upload_catalog.go`
- Prefer same-package `internal/shard` tests for private event-boundary and controller retry behavior. Use `shard_test` only for exported Shard behavior and domain-observable outcomes.
- Keep test doubles local and minimal. A fake `backend.Backend` is appropriate because Backend is a real boundary. A fake controller/core is acceptable only when it models the Upload Outbox seam and asserts domain outcomes or emitted proposals, not private method-call sequences.

### Testing Notes

- RED first: add failing tests that demonstrate missing replay/failure evidence before changing production behavior.
- Use bounded polling helpers already present in `upload_outbox_test.go` and `upload_pressure_test.go`; avoid unbounded sleeps. If a short observation window is unavoidable, pair it with a deterministic readiness signal from the fake Backend.
- Reuse existing helpers where possible: `openApplyTestIndex`, `shardForApplyTest`, `confirmedUploadForApplyTest`, `confirmUploadCommandForApplyTest`, `openUploadTestShard`, `waitPendingUploads`, `waitPendingUploadBlock`, and existing local Backend fakes.
- For cancellation, prefer context cancellation or fake Backend readiness channels over long wall-clock delays.
- For success-after-retry, model a Backend that fails the first relevant operation and succeeds later; assert final Confirmed Upload Catalog state and no duplicate pending entry.
- For replay after restart, use a real Shard close/reopen path when the AC is about startup recovery.
- Race-sensitive behavior is in scope because upload worker retries, cancellation, and concurrency are touched. Run the focused race gate.

### Previous Story Intelligence

- Story 4.1 committed as `e0c72ce feat(shard): add upload outbox event boundary`.
- Story 4.1 established the `uploadOutbox` wrapper in `internal/shard`, not a new package. Continue there unless implementation proves a stronger boundary is needed.
- Story 4.1 removed the old Shard-shaped controller fake from `rewrap_apply_test.go` and added Shard-level domain tests for missing local Block and missing `.idx`; do not reintroduce a single adapter that mirrors Shard internals.
- Story 4.1 full verification passed with `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- Existing uncommitted `_bmad-output/planning-artifacts/epics.md` changes were present before Story 4.2 creation. Do not stage or modify that file unless explicitly scoped.

### Latest Technical Information

- No new external library or package is required for this story. The relevant versions and APIs are repo-pinned in `go.mod`, `tools.go.mod`, and `_bmad-output/project-context.md`.
- Current local toolchain guidance: Go `1.26.4`, Pebble `v1.1.5`, etcd Raft `v3.6.0`, Buf `v1.70.0`, golangci-lint `v2.12.2`.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.2 source.
- `_bmad-output/planning-artifacts/architecture.md` - package ownership, authority model, and validation tier guidance.
- `_bmad-output/project-context.md` - repo rules, package boundaries, testing standards, and critical don't-miss rules.
- `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md` - previous story implementation notes and file list.
- `CONTEXT.md` - glossary and authority model.
- `docs/adr/0010-upload-outbox-via-raft.md` - Upload Outbox Raft authority contract.
- `docs/go-style-guide.md` - Go coding conventions.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- RED: `go test ./internal/shard -run TestUploadObjectVerificationErrorDoesNotLeakBackendKey -count=1` failed because upload verification errors leaked the raw Backend key.
- GREEN: `go test ./internal/shard -run 'TestUploadObjectVerificationErrorDoesNotLeakBackendKey|TestUploadAndConfirmWithRetryCancellationDoesNotProposeConfirm|TestUploadAndConfirmKeepsPendingWhenIndexVerificationFails|TestUploadAndConfirmRetriesAfterInterruptedConfirmProposal|TestUploadOutboxAppliesDuplicate|TestUploadOutboxRejectsStale|TestUploadOutboxProposesUploadConfirmedEvent' -count=1`.
- GREEN: `go test ./internal/shard -run 'TestShardUploadProcessorKeepsPendingUploadWhenIndexVerificationFails|TestShardUploadProcessorKeepsPendingUploadWhenIndexFileMissing|TestUploadAndConfirmKeepsPendingWhenIndexVerificationFails' -count=1`.
- Regression: `go test ./internal/index ./internal/shard`.
- Race: `go test -race ./internal/shard`.
- Boundary/lint: `make package-boundaries`; `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/shard`.
- Full gate: `make check`.
- Exact story gate: `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Added Upload Outbox replay coverage for duplicate sealed events, duplicate confirmed events, stale confirmation generation, and pressure dedupe.
- Added controller boundary coverage for Backend cancellation, partial `.blk` success plus `.idx` verification failure, interrupted `ConfirmUpload` proposal, and success after retry.
- Removed Backend object key leakage from upload verification mismatch and missing-validation-token errors while keeping Confirmed Upload Catalog metadata unchanged.
- Routed upload-controller confirmation proposals through `uploadConfirmedEvent` and `proposeUploadConfirmedEvent`, keeping `ConfirmUpload` as the single outbound Raft authority write.
- Reused existing real Shard reopen, missing local Block, missing `.idx`, retry, auth/throttle, and Local Block Lifecycle fail-closed tests for the broader recovery and local availability acceptance criteria.

### File List

- `_bmad-output/implementation-artifacts/4-2-replay-safe-upload-confirmation-and-backend-failure-handling.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/upload_controller.go`
- `internal/shard/upload_controller_boundary_test.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/shard/upload_outbox_test.go`
