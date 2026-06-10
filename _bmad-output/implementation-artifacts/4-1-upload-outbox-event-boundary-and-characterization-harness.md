---
baseline_commit: 166254624cd380b6d4646364b81d9c1f120a0d1e
---

# Story 4.1: Upload Outbox Event Boundary and Characterization Harness

Status: review

## Story

As a storage engineer,
I want Upload Outbox behavior characterized behind event-style inputs,
so that the refactor can remove Shard-internal seams without changing Block upload semantics.

## Traceability

- Source: Architecture review 2026-06-10 recommendation captured in `_bmad-output/planning-artifacts/epics.md`.
- Epic: Epic 4 - Upload Outbox Reliability and Shard Boundary Hardening.
- Requirements: NFR8, NFR12, NFR13.
- Governing ADR: ADR 0010 - Upload outbox via Raft commands.
- Related existing implementation: `internal/shard/upload.go`, `internal/shard/upload_controller.go`, `internal/shard/block_upload_lifecycle.go`, `internal/index/upload_outbox.go`, `internal/index/confirmed_upload_catalog.go`.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, either create/link an execution issue or explicitly cite this BMAD story plus ADR 0010 in the PR body.

## Acceptance Criteria

1. Given the current Upload Outbox behavior is exercised before refactoring, when a Block is sealed, an upload succeeds, an upload confirmation is replayed, or a Backend failure occurs, then characterization tests capture the current externally observable outcomes for pending upload state, Confirmed Upload Catalog state, retry eligibility, pressure behavior, and local Block availability, and the tests assert domain outcomes rather than Shard-internal method calls.
2. Given the new Upload Outbox module boundary is introduced, when Shard code needs to inform upload processing about lifecycle facts, then the boundary accepts event-style inputs for `Block sealed` and `upload confirmed`, and the only outbound authority dependency is to propose the Raft `ConfirmUpload` command.
3. Given existing tests mock Shard internals such as `pendingUploads`, `hasLocalBlock`, `blockPath`, `idxPath`, or `SetPressure`, when those tests are migrated, then they assert Upload Outbox state, Confirmed Upload Catalog behavior, Backend calls, or emitted proposal requests, and they no longer require a single-adapter interface that mirrors Shard internals method-for-method.
4. Given the Upload Outbox boundary is tested, when package-boundary and static checks run, then the new boundary does not introduce `internal/common`, generic helper packages, Backend authority drift, gRPC status mapping in core packages, or new dependencies on `internal/spike`, and any ADR-worthy ownership change is documented before implementation closure.

## Tasks / Subtasks

- [x] Add characterization coverage before moving behavior. (AC: 1)
  - [x] Cover Block sealed -> pending Upload Outbox materialization.
  - [x] Cover successful Backend `.blk` and `.idx` upload -> emitted or proposed `ConfirmUpload`.
  - [x] Cover duplicate or replayed upload confirmation -> stable Confirmed Upload Catalog outcome.
  - [x] Cover Backend failure -> retry eligibility or observable pending state without confirming upload.
  - [x] Cover missing local Block or `.idx` -> no false `ConfirmUpload` and no write ACK authority drift.
- [x] Introduce the event-style Upload Outbox boundary. (AC: 2, 4)
  - [x] Prefer a focused domain package or file set for Upload Outbox behavior; do not create `internal/common`, `util`, or generic helpers.
  - [x] Model inbound facts as domain events such as `Block sealed` and `upload confirmed`; do not expose Shard lock, Pebble handle, or file-path helpers as the public seam.
  - [x] Keep the only outbound authority write as a request to propose the Raft `ConfirmUpload` command.
- [x] Migrate controller-facing tests away from Shard-internal mocks. (AC: 1, 3)
  - [x] Replace method-call assertions against `pendingUploads`, `hasLocalBlock`, `blockPath`, `idxPath`, and `SetPressure` with domain outcome assertions.
  - [x] Keep retry, ordering, concurrency, and pressure tests focused on externally meaningful state.
- [x] Preserve existing authority boundaries. (AC: 2, 4)
  - [x] `SealBlock` and `ConfirmUpload` remain Raft commands; Pebble Projection remains derived.
  - [x] Backend upload stays outside the write ACK path.
  - [x] Backend inventory or object existence is not introduced as a hot-path consistency oracle.
  - [x] Local Block Lifecycle does not become Document visibility or durable upload authority.
- [x] Run focused and boundary verification. (AC: 1-4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/index/... internal/shard/...`
  - [x] Escalate to `make tier1-check` before broad review if the implementation changes package boundaries, generated proto, or storage semantics.

## Dev Notes

### Current State

- ADR 0010 defines the durable contract: a sealed Block creates a `SealBlock` Raft command, and successful verified upload creates a `ConfirmUpload` Raft command. The Upload Outbox is derived state: committed `SealBlock` without matching `ConfirmUpload`.
- `internal/index/upload_outbox.go` materializes pending uploads under the `pendingUploadPrefix` Pebble keyspace. `PendingUpload` currently carries `BlockID`, `ShardID`, `SealedSizeBytes`, `SealedAtUs`, and `UploadGeneration`.
- `internal/index/confirmed_upload_catalog.go` materializes confirmed upload metadata, including separate `.blk` and `.idx` Backend object metadata.
- `internal/shard/upload.go` owns Raft proposal and apply helpers today: `proposeSealBlock`, `applySealBlock`, `applyConfirmUpload`, generation validation, unsafe confirm handling, and upload pressure refresh.
- `internal/shard/upload_controller.go` owns the upload processor goroutine, Backend PUT/HEAD verification, adaptive retry/concurrency behavior, auth pause, and pressure cache. Its current `uploadCore` seam still exposes Shard-shaped methods: `pendingUploads`, `hasLocalBlock`, `blockPath`, `idxPath`, `retryUploadObligations`, and `SetPressure`.
- `internal/shard/block_upload_lifecycle.go` collects local seal obligations, committed confirm authority, pending outbox refresh, and Confirmed Upload Catalog writes. This is the most likely extraction nucleus, but preserve behavior before moving it.

### Implementation Guardrails

- This story is not the whole Epic 4 refactor. Keep it to characterization tests plus the first event boundary. Story 4.2 owns deeper replay/failure handling, and Story 4.3 owns pressure, pause gate, and scrub coordination ownership.
- Do not change Block/Frame layout, Backend object key format, Raft command wire shape, or Confirmed Upload Catalog semantics without an ADR/proto-aware scope expansion.
- Do not add any dependency or testing framework. Use Go `testing`, local fakes, and existing `backend.Backend`/`index`/`shard` test patterns.
- Do not put Backend logic into `internal/index`. Index packages may persist derived state, but Backend upload and verification stay outside Pebble Projection.
- Do not make `internal/backend` parse Upload Outbox or Confirmed Upload Catalog metadata. Backend stores opaque bytes and exposes provider metadata only.
- Do not import `grpc/status` or `grpc/codes` into core storage packages.
- Do not log raw `transaction_id`, `document_name`, Backend object keys, raw file paths, sensitive peer addresses, or unbounded provider errors in new evidence or logs.

### Project Structure Notes

- Likely touched files:
  - `internal/shard/upload.go`
  - `internal/shard/upload_controller.go`
  - `internal/shard/block_upload_lifecycle.go`
  - `internal/shard/upload_obligations.go`
  - `internal/shard/upload_outbox_test.go`
  - `internal/shard/upload_apply_test.go`
  - `internal/shard/upload_retry_test.go`
  - `internal/shard/upload_pressure_test.go`
  - `internal/index/upload_outbox.go`
  - `internal/index/confirmed_upload_catalog.go`
- Likely new file set, if extraction is warranted:
  - a focused upload-outbox boundary near the current owner. Prefer `internal/shard` until a package-boundary case is proven; introduce a new package only if it clearly reduces coupling and still respects ADR 0010.
- Avoid package names like `common`, `shared`, `util`, or `helpers`.

### Testing Notes

- Existing useful tests include:
  - `TestShardSealMaterializesPendingUpload`
  - `TestShardConfirmUploadCatalogsConfirmedUploadAndClearsPendingUpload`
  - `TestShardUploadProcessorUploadsSealedBlocks`
  - `TestShardUploadProcessorResumesPendingUploadAfterReopen`
  - `TestApplyConfirmUploadIgnoresStaleGenerationAndKeepsPending`
  - `TestApplyConfirmUploadUpdatesDuplicateConfirmationMetadata`
  - `TestApplySealBlockPreservesPendingRewrapGeneration`
  - upload retry, pressure, and OTel metric tests under `internal/shard`
- Add characterization tests before extraction, then keep them green through the boundary change. If a test has to change, the new assertion should be closer to the domain outcome, not closer to private implementation details.
- Race-sensitive behavior is in scope only where the boundary touches goroutine lifecycle, upload worker notification, retry state, or pressure state. Run the focused race gate if those parts move.

### Previous Work Intelligence

- There is no previous Epic 4 story file. Recent history shows Phase 4.5 security/encryption work already landed and should not be reopened in this story.
- Recent relevant commits:
  - `a1a16d0 feat(encryption): encrypt Document payload storage`
  - `00c1b0c feat(rewrap): add durable document envelope rewrap`
  - `4f16ef9 fix(rewrap): fence stale durable rewrap state (#428)`
  - `973b1a6 fix(evidence): require upload proof in production rehearsal (#424)`
- Lessons to carry forward:
  - Keep replay-safety explicit. Generation-aware pending/confirmed upload behavior already exists; do not flatten it away.
  - Treat evidence and production rehearsal requirements as acceptance artifacts, not informal logs.
  - Preserve fail-closed behavior and bounded output conventions from the Phase 4.5 security work.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.1 source.
- `_bmad-output/planning-artifacts/architecture.md` - 2026-06-10 Upload Outbox recommendation and package-boundary guidance.
- `_bmad-output/project-context.md` - package ownership, testing, security, and workflow rules.
- `CONTEXT.md` - glossary and authority model.
- `docs/adr/0010-upload-outbox-via-raft.md` - Upload Outbox Raft authority contract.
- `docs/go-style-guide.md` - Go coding conventions.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- 2026-06-10: RED phase confirmed with `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard` failing on missing Upload Outbox event-boundary symbols.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard` - PASS.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard` - PASS.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` - PASS.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/index/... internal/shard/...` - PASS after local lint fixes.
- 2026-06-10: `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS, including full go tests, full race tests, integration-tag tests, and command builds.

### Completion Notes List

- Added a focused Upload Outbox boundary in `internal/shard` with `Block sealed` and `upload confirmed` event inputs while keeping `SealBlock` and `ConfirmUpload` as Raft commands.
- Routed Shard seal/apply/pressure/authority paths through the Upload Outbox boundary without introducing a new package, generic helper layer, Backend inventory oracle, or `internal/spike` dependency.
- Replaced the controller seam's Shard-shaped local Block helpers with a local upload source abstraction so upload processing no longer depends on `hasLocalBlock`, `blockPath`, or `idxPath` methods.
- Added characterization coverage for event application/proposal, missing local Block, and missing `.idx` behavior; existing retry, duplicate-confirmation, pressure, and successful upload tests remain green.

### File List

- `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/blockfiles.go`
- `internal/shard/rewrap_apply_test.go`
- `internal/shard/shard.go`
- `internal/shard/upload.go`
- `internal/shard/upload_controller.go`
- `internal/shard/upload_outbox_boundary_test.go`
- `internal/shard/upload_outbox_events.go`
- `internal/shard/upload_outbox_test.go`
- `internal/shard/upload_pressure.go`
- `internal/shard/upload_pressure_test.go`

### Change Log

- 2026-06-10: Implemented Story 4.1 Upload Outbox event boundary and characterization harness; verified with focused, race, package-boundary, scoped lint, and full `make check` gates.
