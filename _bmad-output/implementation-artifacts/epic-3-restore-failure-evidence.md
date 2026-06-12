# Epic 3 Restore Failure and Corruption Evidence

Status: complete
Story: 3.5 - Restore Failure and Corruption Semantics
Baseline commit: ec6feedc39f2d24890d65dab55e062375959030a
Created: 2026-06-11T23:06:06-04:00

## Scope

This artifact records Story 3.5 evidence for FR-8 restore failure semantics:
transient Backend dependency failures, confirmed Backend object loss/corruption,
restore cancellation/timeout cleanup, and retry-budget exhaustion. It does not
claim Story 3.6 encryption-compatible restore closure, Story 3.7 Backend
durability closure, real S3/IAM production rehearsal, deployed
production-profile restore, or V2 release readiness.

## Restore Authority Path

`ReadDocument` resolves visible Document metadata through the Pebble Projection
and retained `.idx` metadata, observes Local Block Lifecycle state `evicted`,
loads committed Confirmed Upload Catalog metadata, uses Backend `GetObject` for
the committed full Block object, stages bytes locally, validates Backend metadata,
copies with bounded buffers, verifies Block header/Frame CRC/Document SHA-256,
and publishes atomically before the normal local read path returns bytes.

Backend list/HEAD/object existence is not read-path authority. A local eviction
marker without matching committed upload metadata is not restore authority.

## Acceptance Criteria Evidence

| AC | Status | Evidence |
| --- | --- | --- |
| AC-3.5.1 transient Backend failure maps `UNAVAILABLE` and returns no partial bytes | pass | Focused Shard tests cover throttled/transient/auth classes, missing Backend config, nil reader/zero metadata, no publish, no restore marker, staging cleanup, and retained eviction marker. Public gRPC test covers `codes.Unavailable` plus `ErrorInfo.Reason == backend_restore_unavailable`. |
| AC-3.5.2 confirmed missing/corrupt Backend object maps `DATA_LOSS` and publishes no partial local Block | pass | Focused Shard tests cover not-found, corrupt/permanent/conflict Backend classes, metadata mismatch, validation-token mismatch, corrupt payload/header/Frame/Document checksum failures, restore-specific data-loss reasons, and no partial publish. Public gRPC test covers `codes.DataLoss` plus restore-specific `ErrorInfo`. |
| AC-3.5.3 client cancellation returns no partial bytes and restore lifecycle remains bounded/observable | pass | Focused Shard tests cover leader cancellation, waiter deadline, leader deadline, bounded waits, no reader/metadata on cancellation, retained eviction state, and restore metrics/health bounded failure reasons. Public server cancellation tests preserve `CANCELED`. |
| AC-3.5.4 timeout, 404, corrupt payload, checksum mismatch, and retry exhaustion map to documented typed outcomes | pass | Focused Shard tests cover leader timeout/deadline, Backend 404/not-found, corrupt payload/checksum mismatch, and restore retry budget exhaustion with no partial publish. |

## Failure Taxonomy

| Failure | Expected public outcome | Expected reason | Publish behavior | Evidence status |
| --- | --- | --- | --- | --- |
| Backend throttled/transient/auth during restore | `UNAVAILABLE` | `backend_restore_unavailable` | no reader, zero metadata, no `.blk`, no restore marker, eviction marker remains | pass |
| Backend not-found for confirmed object | `DATA_LOSS` | `backend_restore_missing` | no published `.blk`, staging removed, eviction marker remains | pass |
| Backend corrupt/permanent/conflict class after committed upload | `DATA_LOSS` | `backend_restore_corrupt` | no published `.blk`, staging removed, eviction marker remains | pass |
| Backend metadata size mismatch | `DATA_LOSS` | `backend_restore_metadata_mismatch` | no published `.blk`, staging removed, eviction marker remains | pass |
| Validation token mismatch | `DATA_LOSS` | `backend_restore_metadata_mismatch` | no published `.blk`, staging removed, eviction marker remains | pass |
| Block header, Frame CRC, or Document SHA-256 verification failure | `DATA_LOSS` | `backend_restore_checksum_mismatch` | no published `.blk`, staging removed, eviction marker remains | pass |
| Waiting client cancellation | `CANCELED` | context status, no restore-specific public reason required | no reader or metadata for canceled client; shared restore may continue | pass |
| Restore leader deadline | `DEADLINE_EXCEEDED` | context status, no restore-specific public reason required | no published `.blk`, staging removed, eviction marker remains | pass |
| Retry budget exhausted after retryable Backend classes | `UNAVAILABLE` | `backend_restore_unavailable` | no published `.blk`, staging removed, eviction marker remains | pass |

## Changed Boundary Watchlist

- Allowed: `internal/store` typed Store errors, `internal/server` transport mapping,
  `internal/shard` restore classification/retry logic and tests, `internal/eviction`
  bounded restore failure reasons if needed, and BMAD evidence/story status files.
- Avoid: `proto/`, `gen/`, Block/Frame layout, Backend object key format, Pebble
  keys, public/peer/admin wire contracts, routing/placement, production security
  policy, OpenBao policy, and new runtime dependencies.

## Planned Verification Commands

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable|TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss|TestReadDocumentRestoreSizeMismatchReturnsDataLoss|TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail|TestReadDocument.*DataLoss|TestReadDocument.*Canceled|TestReadDocument.*Deadline' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run 'TestUnavailable|TestDataLoss|TestResourceExhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEvictionOTelMetrics_RecordApplyAndRestore|TestEvictionHealthSnapshot.*Restore|Test.*RestoreFailure' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestore.*Retry|TestReadDocumentRestore.*Budget|TestReadDocumentRestore.*Exhausted' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/eviction ./internal/backend -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore|Test.*RestoreFailure|Test.*Retry' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

## Leak Scan Allowlist

Expected matches include BMAD prose describing forbidden identifiers, test names,
source identifiers, environment variable names, local temp-cache paths, and
redaction tests. A pass requires no real credential material and no new deployed
public/log/metric surface that exposes raw identifiers or Backend object keys.

## Verification Log

- 2026-06-11: Evidence artifact created before production-code changes. No
  acceptance criteria are marked pass yet.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable|TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable|TestReadDocumentRestoreRetriesTransientBackendFailures|TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss|TestReadDocumentRestoreBackendInvariantFailuresReturnDataLoss|TestReadDocumentRestoreSizeMismatchReturnsDataLoss|TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail|TestReadDocumentRestoreDataLossReturnsErrorInfoDetail|TestReadDocument.*Canceled|TestReadDocument.*Deadline' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run 'TestUnavailable|TestDataLoss|TestResourceExhausted' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEvictionOTelMetrics_RecordApplyAndRestore|TestEvictionHealthSnapshot.*Restore|Test.*RestoreFailure' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/eviction ./internal/backend -count=1`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore|Test.*RestoreFailure|Test.*Retry' -count=1 -v`.
- 2026-06-11: PASS - `env GOCACHE=/tmp/scrap-v2-go-build make check`; included formatting diff check, package-boundary check, buf lint/generate diff check, golangci-lint, `go test ./...`, `go test -race ./...`, integration-tagged LocalStack/OpenBao Testcontainers tests, and `go build` for `cmd/scrapd` and `cmd/scrapctl`.
- 2026-06-11: PASS - `git diff --check`.
- 2026-06-11: PASS with allowlisted matches - credential and identifier scans over touched BMAD artifacts plus `internal/shard`, `internal/store`, and `internal/server` found only story/evidence prose, existing fixtures, source identifiers, environment/cache paths, and redaction tests; no real secret material or new deployed public/log/metric identifier leak was found.
