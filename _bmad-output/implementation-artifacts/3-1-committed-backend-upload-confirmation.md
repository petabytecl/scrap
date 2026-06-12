---
baseline_commit: 783d4da2a115b24d52c4a5342dbb58257e1757a9
---

# Story 3.1: Committed Backend Upload Confirmation

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a storage operator,
I want sealed Blocks uploaded and confirmed through committed metadata,
so that Backend durability is observable without entering the write ACK path.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-6.
- Governing ADRs: ADR 0010, ADR 0016, and ADR 0021.
- Prerequisites: Stories 1.1 through 2.6 are done. Epic 2 provides multi-Shard deployed evidence, non-zero Shard Backend observation, and redaction/authority scan patterns.
- Prior implementation intelligence: older upload-outbox stories `4-1-upload-outbox-event-boundary-and-characterization-harness` and `4-2-replay-safe-upload-confirmation-and-backend-failure-handling` already established the local Upload Outbox boundary, replay-safe confirmation handling, split `.blk`/`.idx` object metadata, generation fencing, and no-false-confirmation tests. This story should verify, close gaps, and produce Epic 3 evidence rather than rework that boundary.
- Non-goals: upload pressure policy closure is Story 3.2; policy-gated local eviction is Story 3.3; restore-first cold reads and restore failure semantics are Stories 3.4 through 3.7; production security and real S3/IAM rehearsal remain Stories 4.1 and 6.6.

## Acceptance Criteria

1. **AC-3.1.1 - Committed upload authority.** Given a sealed Block pending upload, when the Shard leader uploads it, then upload obligations and confirmations are recorded through committed metadata. Evidence identifies the Raft command, changed-boundary list, and Shard-local authority path.
2. **AC-3.1.2 - Write ACK independence.** Given Backend upload is delayed or fails transiently, when writes continue within the local durability runway, then write ACK behavior remains independent of Backend success. Evidence proves Backend upload is not in the ACK path.
3. **AC-3.1.3 - Redacted upload evidence.** Given upload evidence is emitted, when artifacts are captured, then Backend keys and raw identifiers are redacted. Evidence records the leak-scan command and result.
4. **AC-3.1.4 - Split-success fail-closed behavior.** Given Backend upload succeeds but committed upload-confirmation metadata does not commit, when recovery or evidence reads upload state, then S.C.R.A.P. does not report a false committed upload confirmation. Evidence records the split-success fixture and authority decision.

## Tasks / Subtasks

- [x] Reconcile the current implementation against the Epic 3 authority contract. (AC: 1, 4)
  - [x] Trace `SealBlock` -> pending Upload Outbox -> Backend PUT/HEAD verification -> proposed `ConfirmUpload` -> committed Confirmed Upload Catalog -> local `*.confirmed-upload.json` marker, and record the changed-boundary list in this story or an Epic 3 evidence artifact.
  - [x] Confirm the current `ConfirmUpload` proto still carries separate `.blk` and `.idx` Backend object metadata plus `upload_generation`; do not change proto or generated code unless a real authority gap is found.
  - [x] Confirm `ConfirmedUploadForTest`, restore reads, production rehearsal, and diagnostics read committed authority from Raft-derived state or the committed marker, not Backend inventory.
- [x] Strengthen committed-metadata authority tests where evidence is missing or stale. (AC: 1, 4)
  - [x] Add or update tests proving a committed `SealBlock` materializes one pending upload obligation and a committed `ConfirmUpload` clears it only after catalog/authority marker persistence succeeds.
  - [x] Add or update a split-success fixture where `.blk` and `.idx` Backend PUT/HEAD verification succeeds but `ConfirmUpload` proposal or commit is interrupted; assert pending upload remains and Confirmed Upload Catalog plus committed authority marker stay absent.
  - [x] Cover restart/reopen or replay for the split-success fixture if current unit coverage only proves the controller fake. Use a real Shard reopen path for recovery claims.
  - [x] Preserve generation fencing: stale generation confirmations must not clear a newer pending upload or overwrite newer confirmed authority.
- [x] Prove Backend upload remains outside the write ACK path. (AC: 2)
  - [x] Add or update a deterministic test with delayed, unavailable, or transiently failing Backend upload where `WriteDocument` ACK succeeds from local durability before upload confirmation.
  - [x] Keep this story to ACK independence and committed confirmation. Do not absorb Story 3.2 admission pressure policy, threshold tuning, or drain/rejection closure.
  - [x] Ensure failed Backend upload never changes Document visibility, public routing, Shard authority, or duplicate-write conflict behavior.
- [x] Produce committed upload evidence for operators. (AC: 1, 2, 4)
  - [x] Add or update a checked-in evidence artifact, likely `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md`, with rows for committed authority, ACK independence, split-success, and redaction.
  - [x] Each evidence row must include AC ID, command, artifact/test path, commit/ref, result `PASS`, `CONCERNS`, or `FAIL`, and a concise next action for non-PASS rows.
  - [x] Include production rehearsal context only as local production-mode evidence unless real S3/IAM is actually run; real S3/IAM closure remains Story 6.6.
- [x] Add redaction and authority-boundary scans. (AC: 3)
  - [x] Scan changed code and evidence for raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, file paths, validation tokens, trace IDs, request IDs, sensitive peer addresses, auth claims, gRPC metadata, and dependency errors that embed paths or object keys.
  - [x] Allow only bounded examples where they are explicitly evidence, such as object-key shape samples or fixture strings that are not production identifiers.
  - [x] Add or rerun an authority scan proving public API routing and Shard membership do not consume Backend keys, S3 listings, local files, pod names, certificates, or peer addresses as authority.
- [x] Preserve package and durable-format boundaries. (AC: 1-4)
  - [x] Keep implementation in `internal/shard`, `internal/index`, existing E2E helpers, scripts, or BMAD evidence artifacts unless a stronger package-boundary case is proven.
  - [x] Do not create `internal/common`, `shared`, `util`, a new Backend wrapper, or a new dependency.
  - [x] Do not change Block/Frame layout, Backend object key format, public/peer/admin wire contracts, Raft command shape, or Confirmed Upload Catalog schema without an ADR and migration/evidence scope.
  - [x] Do not make Backend HEAD/list observations, Local Block Lifecycle, Pebble-only state, or evidence artifacts durable upload authority.
- [x] Run focused and regression verification. (AC: 1-4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -run 'Test.*Upload|Test.*Confirm' -count=1`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -count=1`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/e2e -run 'TestE2EBackendUploadHappyPath|TestE2EBackendUploadLeaderChange|TestE2EBackendUploadAdmissionPressure|TestE2EMultiShardBackendUploadUsesNonZeroShard' -count=1 -v`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make check`
  - [x] If runtime evidence, production rehearsal, scripts, or deployment contracts change, run `env GOCACHE=/tmp/scrap-v2-go-build make tier2-e2e-up` or the narrowest documented Tier 2 target that exercises Backend upload confirmation.

## Dev Notes

### Current State

- `CONTEXT.md` defines Backend upload as asynchronous and outside the write ACK path. Upload Outbox is the durable per-Shard record for sealed Blocks pending Backend upload, and Confirmed Upload Catalog is derived from committed `ConfirmUpload`.
- `_bmad-output/project-context.md` forbids ACKing writes from Upload Outbox state or pending Backend upload, forbids Backend inventory as a hot read/write consistency oracle, and requires redacted evidence before closure.
- ADR 0010 defines `SealBlock` as the upload obligation and `ConfirmUpload` as the committed upload completion command after `.blk` and `.idx` upload plus HEAD verification.
- ADR 0016 requires committed `ConfirmUpload` before local eviction can consider a Block uploaded, and explicitly says Backend HEAD/list observations are not sufficient authority.
- ADR 0021 adds `upload_generation`; stale upload confirmations must not clear newer pending or confirmed generations.
- `proto/scrap/v1/raft.proto` already has `ConfirmUpload` with `block_object`, `index_object`, and `upload_generation`. Proto changes are not expected.
- `internal/shard/upload_controller.go` uploads `.blk` and `.idx`, verifies each with `HeadObject` size and ETag, then proposes `ConfirmUpload` through `proposeUploadConfirmedEvent`.
- `internal/shard/block_upload_lifecycle.go` applies committed seals and confirmations. Confirmation writes the committed authority marker and Confirmed Upload Catalog before deleting the pending upload.
- `internal/shard/confirmed_upload_authority.go` stores `*.confirmed-upload.json` markers with Block ID, Shard ID, sealed size, upload generation, and Backend object metadata. This marker is local committed authority cache, not Backend inventory.
- `internal/index/upload_outbox.go` and `internal/index/confirmed_upload_catalog.go` materialize the derived pending and confirmed Projection state.
- `scripts/production-rehearsal.sh` already waits for `*.confirmed-upload.json` and writes `upload-confirmation.json` with a count, without copying raw object keys into the report.
- Existing E2E upload coverage includes Backend happy path, leader change, admission pressure, and non-zero Shard Backend pair observation. Story 3.1 should use those as evidence only after checking they prove the specific ACs.

### Implementation Guidance

- Start by adding the red tests or evidence checks that fail on missing Story 3.1 proof, not by refactoring the upload path.
- Prefer same-package `internal/shard` tests for controller/lifecycle behavior and `shard_test` tests for externally observable Shard outcomes.
- For split-success, a fake Backend plus proposal/apply interruption is appropriate, but recovery claims should use a real Shard reopen path if possible.
- Keep Backend keys as private provider metadata. If an evidence artifact needs to mention object identity, use redacted counts or bounded shape samples, and separately prove public reads route by Transaction.
- Keep production rehearsal report language narrow: it can prove local production-mode committed upload confirmation, not final V2 release readiness or real S3/IAM durability unless Story 6.6 runs.
- If a code gap is found, prefer the smallest fix that preserves existing ADRs. Create an ADR only if storage format, wire protocol, dependency/runtime choice, security contract, or package ownership changes.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md` - story status and verification notes during dev.
- `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` - committed upload confirmation evidence table.
- `internal/shard/upload_controller_boundary_test.go` - split-success and proposal interruption fixture if existing coverage is not enough.
- `internal/shard/upload_outbox_test.go` or `internal/shard/upload_outbox_boundary_test.go` - real Shard pending/catalog/authority outcomes and generation fencing.
- `internal/shard/block_upload_lifecycle.go`, `internal/shard/upload.go`, or `internal/shard/upload_controller.go` - only if tests expose a real behavior gap.
- `test/e2e/upload_e2e_test.go` or `test/e2e/multishard_evidence_e2e_test.go` - only if runtime evidence needs a more precise committed-authority assertion.
- `scripts/production-rehearsal.sh` and `docs/production-rehearsal.md` - only if report semantics need tighter committed-confirmation wording or redaction.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - story status transitions.

Likely avoid:

- `proto/`, `gen/`, public `DocumentService`, peer `PeerService`, admin wire shapes, and Backend object key format.
- `internal/backend/*` unless a test fake or existing adapter bug is directly blocking confirmation evidence.
- `internal/routing/*`, `internal/cmd/public_store_router.go`, and placement logic except for authority scans.
- Eviction and restore implementation. Those are separate Epic 3 stories.
- New libraries, assertion frameworks, mocking frameworks, or package-level globals.

### Testing Notes

- Start narrow:
  - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestUploadAndConfirm|TestShard.*Upload|TestApplyConfirm' -count=1`
  - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index -run 'Test.*Upload|Test.*Confirm' -count=1`
- Use bounded readiness signals in fakes instead of sleep-only assertions.
- If tests touch goroutine lifecycle, upload worker retry, or cancellation, add a focused race run: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Upload|Test.*Confirm' -count=1`.
- For runtime proof, prefer the existing Tier 2 path only when local unit/integration coverage cannot prove the AC. Runtime evidence is expensive and should be used for deployed behavior, not as a substitute for deterministic unit tests.
- Before review, record every verification command, result, and skipped reason in the Dev Agent Record.

### Previous Story Intelligence

- Story 2.6 review fixed non-zero Shard Backend evidence to wait for the written Document's Backend index and to prove public read/head before and after Backend observation.
- Story 2.6 also kept multi-Shard peer scrub/rebuild fail-closed until peer scrub/rebuild RPCs carry Shard ID. Do not use scrub/rebuild success as upload confirmation evidence in this story.
- Older Story 4.1 established `uploadOutbox` in `internal/shard` with event-style inputs while keeping `SealBlock` and `ConfirmUpload` as Raft commands.
- Older Story 4.2 added coverage for duplicate/replayed seals, duplicate/replayed confirmations, stale generation, interrupted proposal, Backend cancellation, partial `.blk` success plus `.idx` verification failure, and no Backend-key leakage in upload verification errors.
- The current sprint tracker has been regenerated so Epic 3 is the live backlog. Treat old Story 4.x artifacts as implementation intelligence, not current tracker status.

### Technical Research Notes

- GitHub repo search `gh search repos 'raft upload outbox object storage Go' --limit 5` returned no reusable implementation candidates.
- GitHub code search `gh search code 'ConfirmUpload language:Go' --limit 5` returned unrelated generic upload code. No external implementation should be adopted for this story.
- The repo already pins AWS SDK for Go v2 modules in `go.mod`, including `github.com/aws/aws-sdk-go-v2/service/s3 v1.101.0`; no new package registry dependency is expected.
- AWS SDK for Go v2 S3 checksum docs say current S3 module versions add default CRC32 upload integrity when no algorithm is specified, and `GetObject` checksum validation requires checksum mode. Source: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/s3-checksums.html
- Amazon S3 `PutObject` docs say successful PUT stores the full object and describe checksum/ETag validation inputs. Source: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html
- AWS SDK for Go v2 `HeadObject` operation exposes object metadata such as ETag, size, and checksums when present, and the SDK includes `ObjectExistsWaiter` over `HeadObject`. Source: https://github.com/aws/aws-sdk-go-v2/blob/main/service/s3/api_op_HeadObject.go
- These provider facts support the existing PUT plus HEAD verification step, but they do not change S.C.R.A.P. authority: only committed `ConfirmUpload` metadata may report upload confirmation.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.1 acceptance criteria.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - V2 package ownership, evidence, and release-boundary rules.
- `_bmad-output/project-context.md` - critical authority, testing, security, evidence, and commit rules.
- `CONTEXT.md` - glossary definitions and Upload Outbox/Confirmed Upload Catalog domain language.
- `docs/adr/0010-upload-outbox-via-raft.md` - committed `SealBlock` and `ConfirmUpload` authority.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - committed upload confirmation as eviction prerequisite and Backend inventory as non-authority.
- `docs/adr/0021-durable-rewrap-raft-command.md` - `upload_generation` fencing for stale confirmations.
- `proto/scrap/v1/raft.proto` - current `ConfirmUpload` command shape.
- `internal/shard/upload_controller.go` - Backend PUT/HEAD verification and confirmation proposal path.
- `internal/shard/upload.go` - seal and confirmation proposal/apply helpers.
- `internal/shard/upload_outbox_events.go` - Upload Outbox event boundary.
- `internal/shard/block_upload_lifecycle.go` - committed seal/confirm lifecycle and catalog writes.
- `internal/shard/confirmed_upload_authority.go` - local committed `ConfirmUpload` marker.
- `internal/index/upload_outbox.go` and `internal/index/confirmed_upload_catalog.go` - pending and confirmed Projection state.
- `internal/shard/upload_controller_boundary_test.go`, `internal/shard/upload_outbox_test.go`, and `test/e2e/upload_e2e_test.go` - existing upload confirmation and Backend evidence tests.
- `scripts/production-rehearsal.sh` and `docs/production-rehearsal.md` - production-mode committed upload confirmation evidence.
- `_bmad-output/implementation-artifacts/2-6-multi-shard-evidence-closure.md` - latest non-zero Shard Backend evidence and redaction/authority patterns.
- `_bmad-output/implementation-artifacts/4-1-upload-outbox-event-boundary-and-characterization-harness.md` - prior upload-outbox boundary implementation intelligence.
- `_bmad-output/implementation-artifacts/4-2-replay-safe-upload-confirmation-and-backend-failure-handling.md` - prior replay/failure implementation intelligence.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- CREATE-STORY: `git status --short --branch` confirmed clean `v2...origin/v2` at baseline `783d4da2a115b24d52c4a5342dbb58257e1757a9`.
- CREATE-STORY: Loaded `CONTEXT.md`, `_bmad-output/project-context.md`, `_bmad-output/planning-artifacts/epics.md`, ADR 0010, ADR 0016, ADR 0021, `proto/scrap/v1/raft.proto`, current upload controller/lifecycle code, production rehearsal script, and prior upload-outbox story artifacts.
- RESEARCH: `gh search repos 'raft upload outbox object storage Go' --limit 5 --json fullName,url,description` returned no reusable implementation candidates.
- RESEARCH: `gh search code 'ConfirmUpload language:Go' --limit 5 --json repository,path,url` returned unrelated generic upload code.
- RESEARCH: Official AWS SDK for Go v2 and Amazon S3 docs reviewed for S3 PutObject integrity, HeadObject metadata/waiter behavior, and current checksum defaults.
- DEV-STORY: `git status --short --branch` resumed Story 3.1 on `v2...origin/v2` with story, sprint-status, evidence, and `internal/shard/upload_outbox_test.go` changes only.
- DEV-STORY: Added `TestWriteDocumentAckDoesNotWaitForBackendUpload` to block Backend `.blk` upload while a subsequent `WriteDocument` returns from local durability.
- DEV-STORY: Replaced the discarded canceled-proposal fixture with `TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen`, which leaves Backend objects present without committed confirmation, asserts pending Upload Outbox remains and confirmed catalog/marker are absent, then reopens and confirms through the Shard path.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestWriteDocumentAckDoesNotWaitForBackendUpload|TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -run 'Test.*Upload|Test.*Confirm' -count=1` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -count=1` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/e2e -run 'TestE2EBackendUploadHappyPath|TestE2EBackendUploadLeaderChange|TestE2EBackendUploadAdmissionPressure|TestE2EMultiShardBackendUploadUsesNonZeroShard' -count=1 -v` passed with all targeted E2E tests skipped because `SCRAP_E2E=1` was not set.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestWriteDocumentAckDoesNotWaitForBackendUpload|TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen' -count=10 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed.
- VERIFY: `git diff --check` passed.
- SECURITY: Secret keyword scan over changed Story 3.1 files found only expected story/status text and validation-token test fixtures; no secret-shaped values were introduced.
- REDACTION: Raw identifier/evidence scan over changed Story 3.1 files found only expected checklist/story text, sprint `story_location`, bounded Backend object shape tests, and provider-doc references.
- AUTHORITY: Public-routing Backend-inventory scan over `internal/cmd/public_store_router.go`, `internal/server`, `internal/cmd/app.go`, and `internal/cmd/public_store_router_test.go` returned no Backend key/list/head/get/S3/object matches. A wider membership scan found only existing server `member_id` log-field tests, not authority input.

### Completion Notes List

- Ultimate context engine analysis completed - Story 3.1 created with implementation intelligence from current code, current Epic 3 ACs, governing ADRs, and prior upload-outbox hardening stories.
- Scoped Story 3.1 to committed upload confirmation evidence, write ACK independence, redacted evidence, and split-success fail-closed behavior.
- Explicitly excluded upload pressure policy, local eviction, restore-first cold reads, production security startup, and real S3/IAM closure from this story.
- Added deterministic ACK-independence coverage proving a blocked Backend upload does not delay the next `WriteDocument` ACK.
- Added deterministic split-success recovery coverage proving Backend `.blk` and `.idx` objects alone do not create committed upload authority; pending upload remains until the real Shard reopen path commits `ConfirmUpload`.
- Added the Epic 3 upload confirmation evidence artifact with authority path, changed boundaries, focused test evidence, E2E skip limitation, package-boundary evidence, and `make check` evidence.
- No proto, generated code, storage format, Backend key format, production code, dependency, or wire-contract changes were made.

### File List

- `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md`
- `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/shard/upload_outbox_test.go`

## Change Log

- 2026-06-11: Created Story 3.1 Committed Backend Upload Confirmation context and moved status to ready-for-dev.
- 2026-06-11: Started Story 3.1 implementation and moved status to in-progress.
- 2026-06-11: Completed Story 3.1 evidence/tests and moved status to review.
