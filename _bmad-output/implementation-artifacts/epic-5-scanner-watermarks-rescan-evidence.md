# Epic 5 Story 5.2 Evidence: Scanner Watermarks and Rescan Priority

Status: PASS

Baseline commit: `ccc2844bdcf07da30530706efaab34ad75274c0f`
Story: `_bmad-output/implementation-artifacts/5-2-scanner-watermarks-and-rescan-priority.md`

## Scope

Story 5.2 adds Shard-local Content Scanner progress evidence. Closure is limited to:

- Persisting scanner watermarks in the Shard Pebble Projection.
- Restart-safe scanner resume from persisted progress.
- Deterministic rescan priority when the bounded signature version changes.
- Duplicate-safe rollback and conflict behavior.

Out of scope for this evidence:

- `QuarantineDocument` Raft authority.
- Read denial, `scan_status`, or public metadata changes.
- Admin quarantine operations.
- `scrapctl` quarantine workflows.
- Epic 5 closure.

## Changed Boundaries

| Boundary | Change |
| --- | --- |
| `internal/index` | Add a focused scanner watermark Projection record. |
| `internal/avscan` | Add consumer-side progress and signature-version interfaces. |
| `internal/shard` | Wire Shard Projection-backed scanner progress into the scheduler. |
| BMAD artifacts | Track story evidence and local verification. |

## Persisted Projection Keys

| Key | Purpose |
| --- | --- |
| `\x00scanner-watermark\x00current` | Per-Shard scanner progress record containing `last_scanned_block_id` and `last_sig_version_scanned`. |

The scanner watermark is progress evidence only. It is not Document visibility authority and is not part of Transaction identity.

## Verification Plan

| Area | Command / Evidence | Result |
| --- | --- | --- |
| Index watermark tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index -run ScannerWatermark -count=1` | PASS |
| Scanner scheduler tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -run 'Progress|Signature|Watermark|Rollback|HigherPersistedFrontier' -count=1` | PASS |
| Shard wiring tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run Scanner -count=1` | PASS |
| Targeted packages | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1` | PASS |
| Static diff check | `git diff --check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches |
| Scanner-sensitive scan | `rg -n --pcre2 "$scanner_sensitive_pattern" $scan_scope` | PASS - no matches |

## Restart, Rescan, and Rollback Evidence

| Scenario | Evidence | Result |
| --- | --- | --- |
| Restart resumes from persisted frontier | `TestSchedulerResumesFromPersistedProgressAfterReconstruction`, `TestScannerCoordinatorPersistsProgressAcrossReconstruction` | PASS |
| Signature-version change resets scan priority | `TestSchedulerSignatureVersionChangeResetsProgressFromBeginning` | PASS |
| Lower persisted frontier duplicates work safely | `TestSchedulerResumesFromPersistedProgressAfterReconstruction` retries only Blocks above the persisted frontier. | PASS |
| Higher persisted frontier triggers duplicate-safe rescan without visibility changes | `TestSchedulerHigherPersistedFrontierRescansKnownBlocks` | PASS |
| Old signature version rescans from Shard beginning | `TestSchedulerSignatureVersionChangeResetsProgressFromBeginning` | PASS |

## Redaction Notes

Signature versions are bounded identifiers. Tests and scans must not persist or expose rule text, scanner dependency errors, file paths, Document identifiers, trace IDs, request IDs, raw signature names, or Backend object keys.

## Final Decision

| Acceptance Criterion | Decision | Evidence |
| --- | --- | --- |
| AC-5.2.1 | PASS | `internal/index/scanner_watermark.go`, `TestScannerWatermarkRoundTrip`, `TestScannerWatermarkParticipatesInStreamingHash` |
| AC-5.2.2 | PASS | `TestSchedulerResumesFromPersistedProgressAfterReconstruction`, `TestScannerCoordinatorPersistsProgressAcrossReconstruction`; watermarks remain progress-only and no read/public metadata path changed. |
| AC-5.2.3 | PASS | `TestSchedulerSignatureVersionChangeResetsProgressFromBeginning`; signature versions are bounded identifiers and redaction scans passed. |
| AC-5.2.4 | PASS | `TestSchedulerDoesNotPersistFrontierPastFailedLowerBlock`, `TestSchedulerHigherPersistedFrontierRescansKnownBlocks`, missing-progress zero fallback tests. |

## Code Review Follow-up Evidence

BMAD review found high-risk gaps in same-process signature updates, persisted frontier conflicts, and Projection rebuild handle swaps. Follow-up fixes added these regression tests:

- `TestSchedulerSignatureVersionChangeClearsProcessLocalDuplicates`
- `TestSchedulerHigherPersistedFrontierRescansKnownBlocks`
- `TestScannerProgressStoreUsesCurrentProjectionAfterSwap`
- `TestScannerWatermarkDoesNotAffectStreamingHash`
- `TestSchedulerNilSignatureProviderUsesProcessLocalProgress`
- `TestSchedulerDeduplicatesBlockListerDuplicates`
- `TestSchedulerProgressSaveCancellationIsCancellation`
- `TestSchedulerDoesNotPersistAcrossGapAboveFrontier`
- `TestSchedulerAdvancesFrontierThroughCompletedBlocksAfterGapFills`

Targeted follow-up gate:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1
```

Result: PASS.

Final post-review gates:

| Area | Command / Evidence | Result |
| --- | --- | --- |
| Static diff check | `git diff --check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches |
| Scanner-sensitive scan | `rg -n --pcre2 "$scanner_sensitive_pattern" $scan_scope` | PASS - no matches |
