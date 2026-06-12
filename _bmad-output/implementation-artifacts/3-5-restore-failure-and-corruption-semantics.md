---
baseline_commit: ff329492f4f198a949c5485e2dedb91cbfdda12c
created: 2026-06-11T23:02:12-04:00
---

# Story 3.5: Restore Failure and Corruption Semantics

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a storage operator,
I want Backend restore failures mapped to precise typed outcomes,
so that operators can distinguish transient dependency failures from data loss.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-8.
- Governing ADRs: ADR 0016, ADR 0020, and ADR 0027.
- Prerequisites: Stories 3.1 through 3.4 are done. Story 3.4 proved restore-first cold reads and deliberately left detailed failure taxonomy, retry-budget evidence, encrypted production restore evidence, real S3/IAM rehearsal, and final Epic 3 closure to later work.
- Current implementation intelligence: `internal/shard/restore.go` already maps transient Backend classes to `ErrUnavailable`, maps missing/corrupt/permanent Backend classes to `ErrDataLoss`, removes failed staging files, leaves eviction state in place, and records restore failure reasons. `internal/store/errors.go` currently has typed reasons only for unavailable/resource-exhausted errors; `ErrDataLoss` has no restore-specific public ErrorInfo reason yet.
- Non-goals: direct Backend streaming, range streaming, per-Frame remote reads, Block/Frame layout changes, Backend object key changes, Pebble schema changes, public/peer/admin proto changes, Story 3.6 encryption closure, Story 3.7 final durability closure, real S3/IAM production rehearsal, and V2 release readiness.

## Acceptance Criteria

1. **AC-3.5.1 - Transient Backend restore failure is public `UNAVAILABLE`.** Given Backend is transiently unavailable, when restore-first read runs, then the public operation maps to `UNAVAILABLE`, returns no reader or partial bytes, and records bounded non-sensitive evidence for `backend_restore_unavailable`.
2. **AC-3.5.2 - Missing or corrupt confirmed Backend object is public `DATA_LOSS`.** Given a confirmed Backend object is missing or corrupt, when restore verification runs, then the public operation maps to `DATA_LOSS`, publishes no partial local Block, leaves restore state repairable, and records a restore-specific data-loss reason without leaking Backend object keys or raw Document identifiers.
3. **AC-3.5.3 - Cancellation leaves no partial result and bounded lifecycle.** Given client cancellation occurs during restore, when the client stops waiting, then no partial bytes are returned and restore lifecycle remains bounded, observable, and cleaned up according to whether the canceled client was a waiter or the restore leader.
4. **AC-3.5.4 - Timeout, 404, checksum mismatch, and retry exhaustion have documented typed outcomes.** Given restore times out, Backend returns not-found, payload checksum mismatches, or retry budget is exhausted, when restore-first read runs, then the outcome maps to the documented typed failure and publishes no partial local Block. Evidence records timeout, 404, corrupt payload, checksum mismatch, and retry behavior.

## Tasks / Subtasks

- [x] Build the Story 3.5 evidence artifact before production-code changes. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` with AC rows, failure taxonomy table, exact commands, results, leak-scan allowlist, and remaining concerns.
  - [x] Record current restore authority path and failure points: `ReadDocument` -> Projection Resolution -> Local Block Lifecycle `evicted` -> Confirmed Upload Catalog -> Backend `GetObject` -> staging -> metadata validation -> copy -> Block/Frame/Document verification -> atomic publish.
  - [x] Record all non-goals above so evidence does not claim encryption closure, deployed production restore, final Epic 3 closure, or release readiness.
- [x] Add or verify public typed error reasons for restore failures. (AC: 1, 2, 4)
  - [x] Reuse existing `storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, ...)` for transient dependency failures unless retry exhaustion needs a narrower unavailable reason such as `backend_restore_retry_exhausted`.
  - [x] Add bounded data-loss reason support only if the current public surface cannot express restore-specific `DATA_LOSS` causes. Prefer a Store-layer pattern analogous to `UnavailableError` and `ResourceExhaustedError`, plus server `ErrorInfo` detail, without protobuf changes.
  - [x] Suggested data-loss reasons: `backend_restore_missing`, `backend_restore_corrupt`, `backend_restore_checksum_mismatch`, and `backend_restore_metadata_mismatch`. Collapse reasons only if tests prove the operator outcome remains precise and documented.
  - [x] Keep Store/core packages free of `grpc/status` and `codes`; transport mapping belongs in `internal/server`.
- [x] Prove transient Backend dependency failures fail closed. (AC: 1)
  - [x] Strengthen or reuse `TestReadDocumentRestoreBackendTransientReturnsUnavailable` and missing-Backend-config coverage.
  - [x] Cover provider-neutral transient classes that should stay `UNAVAILABLE`: `backend.ErrThrottled`, `backend.ErrTransient`, and `backend.ErrAuth`.
  - [x] Assert failed restore returns nil reader, zero metadata, no partial bytes, no restore marker, no published `.blk`, no leftover staging file, and an eviction marker that remains available for repair/operator intervention.
  - [x] Add public gRPC coverage in `internal/server` proving `ReadDocument` returns `codes.Unavailable` and `ErrorInfo.Reason == backend_restore_unavailable`.
- [x] Prove missing/corrupt confirmed Backend objects are `DATA_LOSS`. (AC: 2, 4)
  - [x] Reuse or strengthen tests for Backend not-found, corrupt object class, permanent/conflict class if they indicate confirmed Backend object invariant breakage, size mismatch, validation-token mismatch, Block header corruption, Frame CRC corruption, and Document SHA-256 mismatch.
  - [x] Add public gRPC coverage proving `ReadDocument` maps restore data loss to `codes.DataLoss` and carries a restore-specific ErrorInfo reason if data-loss reasons are added.
  - [x] Verify failed restore never atomically publishes staged bytes after metadata, checksum, header, Frame, or Document verification failure.
  - [x] Verify repair/health observability records `data_loss` without cardinality-heavy attributes.
- [x] Prove cancellation and timeout semantics. (AC: 3, 4)
  - [x] Reuse Story 3.4's bounded fake-Backend helpers (`releaseBackendOnce`, `waitRestoreBackendStarted`, `waitReadResult`, `waitErrorResult`) instead of unbounded channel receives.
  - [x] Keep waiter cancellation distinct from leader cancellation: a waiter cancellation returns no reader/metadata and must not cancel another in-flight restore; a leader deadline may fail the restore closed and leave the Block evicted.
  - [x] Add public server tests if current transport coverage does not prove `context.Canceled` and `context.DeadlineExceeded` survive restore-first `ReadDocument`.
  - [x] Assert cleanup is deterministic: no staging files remain, restore singleflight map entry is deleted, and metrics/health carry a bounded `canceled` or timeout-specific reason.
- [x] Add or document bounded retry-budget behavior. (AC: 4)
  - [x] Inventory existing retry behavior first. S3 SDK/provider retries may exist below `internal/backend`, but Story 3.5 needs Shard-visible evidence for the restore outcome and budget.
  - [x] If no explicit Shard-visible budget exists, add the smallest restore-local retry policy that preserves package boundaries. Retry only transient dependency classes; never retry not-found, corrupt, permanent, conflict, metadata mismatch, checksum mismatch, or verification failures.
  - [x] Bound retry attempts by context deadline/cancellation and a small config/default. Do not add unowned background goroutines, global retry state, cross-Shard coordination, sleeps in tests, or a new dependency.
  - [x] Prove exhausted retry budget returns a documented typed outcome, publishes no partial local Block, and records a bounded metric/health reason.
- [x] Preserve architecture and security boundaries. (AC: 1-4)
  - [x] Keep restore orchestration in `internal/shard`, Backend error classes in `internal/backend`, lifecycle marker transitions in `internal/localblock`, public mapping in `internal/store` and `internal/server`, and operator-facing health in `internal/eviction`.
  - [x] Do not create `internal/coldread`, `common`, `util`, a second read path, a new Backend wrapper package, new assertion/mocking libraries, or broad abstractions for one story.
  - [x] Do not use Backend list/HEAD/object existence as consistency authority. Restore must follow committed metadata and explicit verification only.
  - [x] Do not leak raw `transaction_id`, `document_name`, idempotency keys, Backend object keys, validation tokens, trace IDs, request IDs, auth claims, peer addresses, filesystem paths, or dependency error strings in public errors, deployed logs, metrics, traces, screenshots, or evidence.
- [x] Run focused and regression verification. (AC: 1-4)
  - [x] Run focused Shard restore failure/corruption/cancellation/retry tests.
  - [x] Run focused Store/server error reason tests.
  - [x] Run restore metric and eviction health tests.
  - [x] Run race coverage for restore cancellation/singleflight if production code changes concurrency or retry behavior.
  - [x] Run `make check` before BMAD code review unless a concrete blocker is recorded in the story.

## Dev Notes

### Current State

- `CONTEXT.md` defines Confirmed Upload Catalog as restore authority and Local Block Lifecycle as per-Member filesystem evidence only. Neither Backend inventory nor local marker presence alone is authoritative.
- PRD FR-8 requires restore-first cold reads: when all local `.blk` copies are evicted, `ReadDocument` restores the full Block from Backend, verifies it, and serves through the normal local read path.
- ADR 0016 requires transient Backend restore dependency failures to return gRPC `UNAVAILABLE` with a restore reason such as `backend_restore_unavailable`. It reserves `DATA_LOSS` for verified corrupt bytes, checksum failure, or committed metadata that cannot match the restored Block.
- ADR 0016 also says missing/corrupt confirmed Backend objects should return `DATA_LOSS` with restore-specific reasons such as `backend_restore_missing` or `backend_restore_corrupt`, must not publish staged bytes, and must leave the eviction marker for repair/operator intervention.
- ADR 0027 requires restore concurrency, timeout, retry, staging, and disk budget settings to be bounded and observable. Story 3.4 recorded current same-Block singleflight and timeout behavior, but did not close retry-budget evidence.
- ADR 0020 says encrypted Backend-resident Blocks remain ciphertext and normal read decrypts through the envelope path. Story 3.5 should not alter encryption behavior; Story 3.6 owns full encryption-compatible restore evidence.
- Existing `internal/server` maps context cancellation/deadlines before Store errors and maps read errors through `mapStoreErrorForRead`; preserve those paths when adding data-loss reason details.

### Existing Implementation To Reuse

- `internal/backend/errors.go`: provider-neutral Backend classes already exist: throttled, transient, auth, not-found, conflict, corrupt, and permanent.
- `internal/shard/restore.go`: `mapRestoreBackendError` already maps throttled/transient/auth to `ErrUnavailable` with `backend_restore_unavailable`; maps not-found/corrupt/permanent/conflict to `ErrDataLoss`; preserves context canceled/deadline errors; validates Backend object size and validation token; verifies restored Block header, Frame CRCs, and Document SHA-256; removes staging on failure.
- `internal/shard/restore.go`: `recordRestoreOutcome` and `restoreFailureReason` already emit bounded restore failure reasons: `backend_restore_unavailable`, `data_loss`, `canceled`, `unknown`, and `none`.
- `internal/store/errors.go`: typed reason helpers already exist for `ErrUnavailable` and `ErrResourceExhausted`. Mirror this style if restore-specific data-loss reasons are needed.
- `internal/server/server.go`: `unavailableStatus` and `resourceExhaustedStatus` already attach `errdetails.ErrorInfo`. Add data-loss ErrorInfo by following that transport-local pattern; do not import gRPC packages into Store or Shard.
- `internal/server/restore_unavailable_test.go`: existing public proof for `backend_restore_unavailable`.
- `internal/server/read_cancellation_test.go` and `internal/server/find_documents_internal_test.go`: existing context status preservation patterns.
- `internal/shard/restore_test.go`: existing restore tests cover transient unavailability, missing Backend config, missing Backend object, size mismatch, validation-token mismatch, corrupt Backend object, corrupt Block header, corrupt Frame header, corrupt Document SHA-256, committed-authority checks, concurrent restore coalescing, waiter deadline, leader deadline, and repair restore failure cleanup.
- `internal/shard/restore_test.go`: Story 3.4 review fixed unbounded channel waits. Preserve bounded helpers and once-guarded fake Backend release cleanup in new tests.
- `internal/shard/eviction_metrics_otel_test.go` and `internal/shard/eviction_apply_test.go`: existing restore metric and health snapshot patterns.

### Implementation Guidance

- Start with the evidence artifact and failing/strengthened tests. If existing tests already close an AC, record the exact focused command and avoid rewriting production code.
- Prefer targeted Store/server error-reason additions over public proto changes. `google.rpc.ErrorInfo` already carries bounded reasons on the gRPC surface.
- Public `DATA_LOSS` reason details must be stable, low-cardinality strings. Do not include Backend object keys, raw dependency messages, file paths, Transaction IDs, Document names, validation tokens, or free-form notes.
- Treat Backend not-found/corrupt/permanent/conflict after committed upload as an invariant break, not as transient outage. Do not retry these classes.
- Treat retry exhaustion from transient dependency classes as an unavailable dependency outcome unless implementation evidence proves a stronger typed distinction is required.
- Do not add sleeps as synchronization in tests. Use contexts, channels with bounded waits, fake Backend counters, or test-owned clocks.
- Do not broaden restore concurrency to a global cross-Block limiter unless the retry/budget work requires it and the change remains local to Shard configuration with clear tests. Story 3.4 explicitly scoped current evidence to per-Block same-Block coalescing.
- Avoid overclaiming. If a gate is not run, mark the evidence row as `CONCERNS` or `NOT RUN`, not `PASS`.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-5-restore-failure-and-corruption-semantics.md` - story status, debug log, completion notes, and file list during dev.
- `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` - Story 3.5 evidence table and verification log.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.
- `internal/store/errors.go` and `internal/store/errors_test.go` - only if adding typed data-loss or retry-exhausted reasons.
- `internal/server/server.go` plus focused server tests - public gRPC status/ErrorInfo mapping.
- `internal/shard/restore.go` and `internal/shard/restore_test.go` - restore failure taxonomy, cleanup assertions, cancellation/timeout, and retry-budget behavior.
- `internal/shard/eviction_metrics_otel_test.go`, `internal/shard/eviction_apply_test.go`, and/or `internal/eviction/types.go` - only if observability needs narrower failure reasons.

Likely avoid:

- `proto/`, `gen/`, Block/Frame layout code, Backend object key construction, Pebble key prefixes, public/peer/admin wire contracts, peer transfer code, routing/placement, production security policy, OpenBao policy, and release evidence docs.
- `internal/backend/*` unless a provider-neutral error-classification defect blocks restore evidence.
- New runtime dependencies, assertion libraries, mocking frameworks, global retry packages, or new background workers.

### Testing Requirements

Run focused gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable|TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss|TestReadDocumentRestoreSizeMismatchReturnsDataLoss|TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail|TestReadDocument.*DataLoss|TestReadDocument.*Canceled|TestReadDocument.*Deadline' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run 'TestUnavailable|TestDataLoss|TestResourceExhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEvictionOTelMetrics_RecordApplyAndRestore|TestEvictionHealthSnapshot.*Restore|Test.*RestoreFailure' -count=1 -v
```

Add retry-specific focused tests once the implementation names them:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestore.*Retry|TestReadDocumentRestore.*Budget|TestReadDocumentRestore.*Exhausted' -count=1 -v
```

Run regression gates before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/eviction ./internal/backend -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore|Test.*RestoreFailure|Test.*Retry' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Suggested leak scans. Keep patterns in shell variables so evidence files do not self-match credential-shaped terms copied into prose:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-5-restore-failure-and-corruption-semantics.md _bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md internal/shard internal/store internal/server internal/eviction internal/backend
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-5-restore-failure-and-corruption-semantics.md _bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md internal/shard internal/store internal/server internal/eviction internal/backend
```

If a command is not run, record it as skipped with a reason in the evidence artifact. Do not mark an AC as pass from intent alone.

### Previous Story Intelligence

- Story 3.4 moved restore-first `ReadDocument` behavior to done and left this story to close detailed failure taxonomy and retry-budget evidence.
- Story 3.4 evidence currently says AC-3.4.3 is PASS only for same-Block scope with scoped concerns. Do not use Story 3.5 to claim a global cross-Block limiter unless you add and verify one.
- Story 3.4 evidence says AC-3.4.4 is supporting pass/concerns because deployed production-profile restore was not run. Story 3.5 should use the same evidence honesty.
- Story 3.4 code review found unbounded channel waits and fake Backend release cleanup defects in tests. New restore concurrency tests must use bounded waits and once-guarded cleanup.
- Story 3.3 review found redaction proof must cover public apply/status/HTTP error output, not only internal helpers. Apply that lesson here: prove public gRPC error details and leak scans, not just private Shard errors.
- Story 3.2 review emphasized rejected/failed operations must leave no accepted partial state. For restore, that means nil reader, zero metadata, no published `.blk`, no restore marker, no leftover staging file, and eviction marker retained.
- Story 3.1 established that Backend success without committed `ConfirmUpload` is not committed upload authority. Preserve that rule when adding retry or data-loss handling.

### Latest Technical Information

- GitHub repo search for `Go object store restore retry data loss unavailable` returned no reusable implementation candidate for S.C.R.A.P.'s restore taxonomy.
- GitHub code search for `backend_restore_unavailable` in `petabytecl/scrap` found only this repo's local pattern.
- AWS S3 performance guidance emphasizes timeouts/retries and SDK-managed retry behavior for S3 calls. Use this only as retry-budget context; S.C.R.A.P. restore authority remains Confirmed Upload Catalog plus verification, not Backend inventory.
- AWS S3 restore-object documentation distinguishes capacity/transient restore failures from object availability and conflict outcomes. Do not import Glacier restore semantics into S.C.R.A.P.; only reuse the distinction between transient dependency failures and confirmed object/invariant failures.
- MinIO's Go retry helper is prior art for context-bound retry loops over retryable HTTP/S3 classes. Do not vendor it; if retry is needed, implement the smallest local restore retry loop with deterministic tests and no new dependency.

### References

- `CONTEXT.md` - Confirmed Upload Catalog, Local Block Lifecycle, Backend, Document, Block, Frame, Shard, Cell, and Member glossary.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.5 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-8.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-3, FR-8, boundaries, and evidence requirements.
- `_bmad-output/project-context.md` - package boundaries, testing rules, telemetry/redaction rules, and commit rules.
- `_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md` - previous story intelligence and review findings.
- `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` - current restore-first evidence and scoped concerns.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - restore failure semantics and Local Block Lifecycle rules.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - encrypted Backend-resident Block contract.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - governing cold-read decision and evidence gates.
- `internal/backend/errors.go`
- `internal/shard/restore.go`
- `internal/shard/restore_test.go`
- `internal/store/errors.go`
- `internal/server/server.go`
- `internal/server/restore_unavailable_test.go`
- `internal/server/read_cancellation_test.go`
- `internal/shard/eviction_metrics_otel_test.go`
- `https://docs.aws.amazon.com/whitepapers/latest/s3-optimizing-performance-best-practices/timeouts-and-retries-for-latency-sensitive-applications.html`
- `https://docs.aws.amazon.com/cli/latest/reference/s3api/restore-object.html`
- `https://github.com/minio/minio-go/blob/c693f76f982b49d47514163d7e182e0f2a41553a/retry.go`

## Dev Agent Record

### Agent Model Used

GPT-5 Codex.

### Debug Log References

- CREATE-STORY: Resumed after interruption from clean `v2...origin/v2` at `ff329492f4f198a949c5485e2dedb91cbfdda12c`; Story 3.4 review fixes were already committed and pushed.
- CREATE-STORY: Loaded BMAD create-story workflow, customization block, `CONTEXT.md`, `_bmad-output/project-context.md`, sprint status, Epic 3, Story 3.5 ACs, FR-8, architecture DG-3, ADR 0016, ADR 0020, ADR 0027, Story 3.4, current restore/error/server/metric code, and recent git history.
- CREATE-STORY: GitHub searches found no reusable candidate for the exact restore failure taxonomy; Exa research was used only for timeout/retry prior art and S3 error-classification context.
- CREATE-STORY: Current baseline commit is `ff329492f4f198a949c5485e2dedb91cbfdda12c`.
- DEV-STORY: Started implementation from clean `v2...origin/v2` after pushing story creation commit `ec6feedc39f2d24890d65dab55e062375959030a`; preserved story baseline commit `ff329492f4f198a949c5485e2dedb91cbfdda12c`.
- DEV-STORY: Created `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` before production-code changes with restore authority path, AC rows, failure taxonomy, boundary watchlist, planned verification commands, and leak-scan allowlist.
- DEV-STORY: Added Store `DataLossError` reason support and server `codes.DataLoss` ErrorInfo mapping for restore-specific reasons without proto or Store gRPC dependencies.
- DEV-STORY: Added restore-specific data-loss mapping in Shard for missing confirmed Backend objects, corrupt/permanent/conflict Backend classes, metadata mismatch, validation-token mismatch, and restored Block verification failures.
- DEV-STORY: Added bounded restore retry attempts around full restore-object download attempts; retries only `backend_restore_unavailable` outcomes and never retries missing/corrupt/permanent/conflict/metadata/checksum failures.
- DEV-STORY: Focused Story 3.5 Shard, Store, server, metric/health, package regression, Shard race, and `make check` gates passed.
- DEV-STORY: `git diff --check` passed; credential and identifier scans matched only allowlisted prose, fixtures, source identifiers, environment/cache paths, and redaction tests.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Scoped Story 3.5 to restore failure taxonomy, public typed outcomes, cancellation/timeout cleanup, retry-budget exhaustion evidence, and no-partial-publish proof.
- Identified existing implementation to reuse: `internal/shard/restore.go`, `internal/backend/errors.go`, `internal/store/errors.go`, `internal/server/server.go`, restore tests, restore metrics, and eviction health snapshots.
- Flagged likely gaps: public restore-specific `DATA_LOSS` reason details, provider-neutral transient-class coverage beyond one sentinel, explicit retry-budget exhaustion evidence, and public cancellation/timeout restore-first proof.
- Preserved non-goals for encryption-compatible restore, production OpenBao/S3/IAM rehearsal, final Epic 3 closure, and release readiness.
- Created the Story 3.5 restore failure evidence artifact before production-code changes and left all AC rows pending until verified.
- Implemented public restore-specific DATA_LOSS reasons with gRPC ErrorInfo details while preserving Store/server package boundaries.
- Implemented bounded Shard-visible restore retry budget for transient Backend restore failures and proved retry exhaustion fails closed with no partial publish.
- Proved transient, missing, corrupt, metadata mismatch, checksum mismatch, cancellation, timeout/deadline, retry, metric, and health evidence with focused tests and `make check`.
- Completed final whitespace and security/redaction checks; no real secrets or new deployed raw-identifier leaks were found.

### File List

- `_bmad-output/implementation-artifacts/3-5-restore-failure-and-corruption-semantics.md`
- `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/server/restore_unavailable_test.go`
- `internal/server/server.go`
- `internal/shard/restore.go`
- `internal/shard/restore_test.go`
- `internal/shard/upload.go`
- `internal/store/errors.go`
- `internal/store/errors_test.go`

## Change Log

- 2026-06-11: Created Story 3.5 Restore Failure and Corruption Semantics context and moved status to ready-for-dev.
- 2026-06-11: Started Story 3.5 implementation and created restore failure evidence artifact.
- 2026-06-11: Completed restore failure semantics, verification gates, and moved status to review.
