---
baseline_commit: 954bfda6ce0c1c2a4653090dc20fd57508e2e636
---

# Story 4.3: Upload Pressure, Pause Gate, and Scrub Coordination Ownership

Status: review

## Story

As a storage engineer,
I want Upload Outbox pressure and pause behavior owned behind one module boundary,
so that upload retry, scrub gating, and admission pressure do not share mutable state across Shard internals.

## Traceability

- Source: Epic 4 in `_bmad-output/planning-artifacts/epics.md`.
- Previous stories:
  - `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md`
  - `_bmad-output/implementation-artifacts/4-2-replay-safe-upload-confirmation-and-backend-failure-handling.md`
- Requirements: Architecture review 2026-06-10; NFR7, NFR8, NFR12, NFR13.
- Governing ADR: `docs/adr/0010-upload-outbox-via-raft.md`.
- Related implementation: `internal/shard/upload_pressure.go`, `internal/shard/upload_controller.go`, `internal/shard/upload.go`, `internal/shard/upload_outbox_events.go`, `internal/shard/upload_obligations.go`, `internal/shard/scrub_coordinator.go`, `internal/shard/scrub_dependencies.go`, `internal/scrub/deep.go`, `internal/scrub/block_repair.go`, `internal/localblock/lifecycle.go`.
- GitHub issue: `#432` Upload pressure pause gate and scrub coordination ownership.

## Acceptance Criteria

1. Given pending upload bytes, Backend failures, or retry backlog exceed the configured upload-pressure budget, when Upload Outbox processing updates pressure state, then admission pressure is computed and exposed through one owner, and Shard apply paths, upload workers, and scrub/deep-scrub code do not mutate the same pause gate independently.
2. Given Deep Scrub, Block Quarantine, repair, or local lifecycle classification makes a Block unsafe for upload, when Upload Outbox processing evaluates upload eligibility, then the Block is skipped, paused, or retried according to explicit Local Block Lifecycle and scrub-gate state, and the outcome is observable without treating Local Block Lifecycle as Document visibility or durable upload authority.
3. Given upload pressure changes while scrub coordination is active, when Upload Outbox emits pressure or pause events, then scrub coordination receives bounded events rather than sharing writable gate state, and no apply-loop callback after stop can panic, block forever, or close a channel another lifecycle owner may still write to.
4. Given pressure, pause, scrub-gate, and upload worker tests run with race-sensitive paths, when tests exercise concurrent upload, pause, retry, stop, cancellation, and scrub interaction, then they prove single-writer ownership, cancellation cleanup, bounded worker shutdown, and no duplicate `ConfirmUpload` proposals, and verification includes the narrowest relevant race or concurrency gate.

## Tasks / Subtasks

- [x] Add ownership-focused RED tests before production changes. (AC: 1, 3, 4)
  - [x] Cover upload pressure transitions driving scrub pause/resume through one owner, with scrub/deep-scrub receiving only the `scrub.PauseController` wait surface.
  - [x] Cover concurrent pressure transitions and wait cancellation so pause state cannot double-close or block forever.
  - [x] Cover `Notify`/apply-loop style calls after upload controller stop so no channel is closed by the wrong lifecycle owner.
- [x] Make pressure/pause ownership explicit behind the Upload Outbox pressure boundary. (AC: 1, 3)
  - [x] Replace raw writable scrub-gate sharing with a named pressure coordination owner.
  - [x] Keep Shard apply paths limited to pressure stat refresh and upload wakeups; they do not mutate scrub pause state directly.
  - [x] Keep scrub/deep-scrub and repair code on the wait-only `scrub.PauseController` contract.
- [x] Make local upload eligibility outcomes explicit and observable. (AC: 2)
  - [x] Evaluate Local Block Lifecycle before enqueueing upload work and skip unsafe states such as metadata loss, unexpected loss, quarantined, or evicted evidence without treating that classifier as durable upload authority.
  - [x] Preserve retry semantics for Blocks that may become locally uploadable later.
  - [x] Avoid Backend inventory, raw object keys, raw filesystem paths, or Document identifiers in emitted evidence.
- [x] Preserve authority and package boundaries. (AC: 1-4)
  - [x] Keep `ConfirmUpload` as the only outbound Raft authority write for upload completion.
  - [x] Do not change Block/Frame layout, Backend object key format, Raft command wire shape, or Confirmed Upload Catalog schema.
  - [x] Do not introduce `internal/common`, `shared`, `util`, new dependencies, or generic helpers.
- [x] Run focused and regression verification. (AC: 1-4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'UploadPressure|UploadController|LocalUpload|ScrubCoordinator' -count=1`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'UploadPressure|UploadController|LocalUpload' -count=1`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make check`

## Dev Notes

### Current State

- Story 4.1 established `uploadOutbox` as the event boundary around `blockUploadLifecycle`.
- Story 4.2 hardened duplicate/replayed seal and confirm handling, Backend failure handling, bounded upload verification errors, and `ConfirmUpload` proposal routing through `uploadConfirmedEvent`.
- `Shard.refreshUploadPressureLocked()` currently delegates to `uploadOutboxLocked().RefreshPressure(s.uploads)`, so Shard apply paths push aggregate stats but do not compute pressure levels.
- `uploadController.SetPressure` currently computes level, updates adaptive concurrency, records metrics, and toggles the scrub pause gate on Critical pressure.
- `scrubCoordinator`, `scrub.Deep`, and `scrub.BlockRepairer` consume a `scrub.PauseController` wait surface; they should not own writable upload pressure state.
- `pressurePauseGate` already avoids channel closure in `Stop`/`Notify`; `Notify` sends on a buffered, never-closed channel. Story 4.3 must preserve that property while making ownership clearer.
- `localblock.Classify` already exposes Local Block Lifecycle states: `hot`, `hot_cleanup_needed`, `evicted`, `metadata_loss`, and `unexpected_loss`.
- `Shard.localUploadSource` currently checks only `.blk` existence before returning a file-backed upload source; missing `.idx` is discovered later by `uploadLocalSource.Open(idx)`.

### Implementation Guardrails

- Preserve ADR 0010: Upload Outbox is derived from committed `SealBlock` without matching `ConfirmUpload`; `ConfirmUpload` remains the Raft authority event for upload completion.
- Backend upload remains asynchronous and outside the write ACK path. Backend success or failure must not decide Document visibility.
- Local Block Lifecycle is per-Member filesystem evidence only. It may gate local upload eligibility but must not become Document visibility authority or durable upload completion authority.
- Backend stores opaque bytes only. Do not make Backend parse Upload Outbox, Confirmed Upload Catalog, envelope, or Local Block Lifecycle metadata.
- Do not use Backend listing, arbitrary HEAD, or object existence as a hot-path consistency oracle.
- Avoid logs or metrics containing raw `transaction_id`, `document_name`, Backend object keys, raw file paths, sensitive peer addresses, or unbounded provider errors.
- Use small focused files and same-package tests where private ownership behavior needs coverage.

### Testing Notes

- Prefer deterministic tests using `pressurePauseGate`, controller/core fakes, `localblock.Classify`, and existing Shard upload helpers.
- Use bounded contexts and polling helpers; avoid sleep-heavy concurrency assertions.
- For local eligibility, model unsafe filesystem states with controlled `.blk`, `.idx`, eviction markers, or quarantine suffixes in a temp blocks directory.
- Race-sensitive behavior is in scope because pressure, pause, upload worker, cancellation, and stop paths share concurrency surfaces.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.3 source.
- `_bmad-output/planning-artifacts/architecture.md` - package ownership, authority model, and validation tier guidance.
- `_bmad-output/project-context.md` - repo rules, package boundaries, testing standards, and critical don't-miss rules.
- `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md` - previous story implementation notes.
- `_bmad-output/implementation-artifacts/4-2-replay-safe-upload-confirmation-and-backend-failure-handling.md` - previous story implementation notes.
- `CONTEXT.md` - glossary and authority model.
- `docs/adr/0010-upload-outbox-via-raft.md` - Upload Outbox Raft authority contract.
- `docs/go-style-guide.md` - Go coding conventions.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'UploadPressure|UploadController|LocalUpload|ScrubCoordinator' -count=1`.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'UploadPressure|UploadController|LocalUpload' -count=1`.
- Regression: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard`.
- Race: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard`.
- Boundary/lint: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`; `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/shard`.
- Full gate: `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Added `uploadPressureCoordinator` so Shard holds a named pressure owner and scrub/deep-scrub receive only the wait-side pause controller.
- Routed upload pressure level changes through the coordinator instead of exposing a raw writable pause gate on `Shard`.
- Made local upload eligibility explicit with bounded statuses for ready, quarantined, evicted, metadata loss, unexpected loss, and classification failure.
- Changed upload enqueue/object-open paths to skip unsafe local lifecycle states before Backend PUT and keep the pending Upload Outbox entry available for later retry.
- Added ownership, cancellation, Notify-after-stop, local lifecycle, and no-Backend-PUT tests for the new pressure/pause/eligibility contract.

### File List

- `_bmad-output/implementation-artifacts/4-3-upload-pressure-pause-gate-and-scrub-coordination-ownership.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/eviction_apply_test.go`
- `internal/shard/shard.go`
- `internal/shard/upload.go`
- `internal/shard/upload_controller.go`
- `internal/shard/upload_controller_boundary_test.go`
- `internal/shard/upload_pressure.go`
- `internal/shard/upload_pressure_ownership_test.go`
