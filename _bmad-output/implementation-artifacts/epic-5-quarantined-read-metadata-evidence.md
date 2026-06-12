# Epic 5 Story 5.4 Evidence: Quarantined Read Denial and Metadata Reconciliation

Status: PASS

Baseline commit: `672d7d7738d74b1e4bd7c317e3497e2ca08fa05d`
Story: `_bmad-output/implementation-artifacts/5-4-quarantined-read-denial-and-metadata-reconciliation.md`

## Scope

Story 5.4 adds the public read and metadata behavior for committed Content
Quarantine state. Closure is limited to:

- Denying `ReadDocument` bytes for quarantined Documents.
- Returning bounded `FAILED_PRECONDITION` status with reason `QUARANTINED_AV`.
- Exposing bounded `scan_status` metadata on `HeadDocument` and `FindDocuments`.
- Proving reads fail closed across quarantine races and replay/reconciliation.

Out of scope for this evidence:

- Admin HTTP quarantine list, inspect, confirm, or release.
- `scrapctl` quarantine operator UX.
- Scanner engine runtime dependencies.
- Block Quarantine, Deep Scrub repair, `.blk`/`.idx` mutation, or Backend object mutation.
- Epic 5 closure.

## Changed Boundaries

| Boundary | Change |
| --- | --- |
| `proto/scrap/v1/document.proto` | Add public scan-status metadata fields. |
| `gen/go/scrap/v1/document.pb.go` | Regenerated from proto source. |
| `internal/store` | Add scan-status metadata and quarantine precondition error. |
| `internal/server` | Map scan status and FailedPrecondition read denial. |
| `internal/shard` | Gate reads and reconcile metadata from Content Quarantine Projection. |
| `internal/index` | Add narrow corruption helper for fail-closed read-gate tests. |
| BMAD artifacts | Track story evidence and local verification. |

## Public Contract Summary

Expected public behavior:

- `ReadDocument` for a quarantined Document returns gRPC `FAILED_PRECONDITION`.
- The bounded public reason is `QUARANTINED_AV`.
- No read metadata message and no chunk message is sent before denial.
- `HeadDocument` and `FindDocuments` continue returning bounded metadata.
- Quarantined metadata includes scan status `QUARANTINED`.
- Non-quarantined existing Documents surface as `UNSCANNED` until a committed clean-state authority exists.

## Verification Plan

| Area | Command / Evidence | Result |
| --- | --- | --- |
| Proto compatibility | `make proto-check` | PASS |
| Store/server/shard focused tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard -run 'Quarantine|ScanStatus|ReadDocument|HeadDocument|FindDocuments|Precondition' -count=1` | PASS |
| Index/shard corrupt-state focused tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -run 'ContentQuarantine|Quarantine|ReadDocument' -count=1` | PASS |
| Targeted packages | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard ./internal/index ./internal/admin ./internal/cmd ./internal/scrapctl -count=1` | PASS |
| Static diff check | `git diff --check`; `git diff --cached --check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches |
| Quarantine-sensitive scan | `rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope` | PASS - no matches |

## Race and Replay Evidence

| Scenario | Evidence | Result |
| --- | --- | --- |
| Quarantine before read denies bytes | `TestReadDocumentDeniesQuarantinedDocument`, `TestReadDocumentQuarantinedReturnsFailedPreconditionWithoutSend`, `TestGRPCQuarantinedReadDeniedAndMetadataExposesScanStatus` | PASS |
| Metadata remains available with scan status | `TestQuarantineMetadataScanStatusStaysAvailable`, `TestGRPCQuarantinedReadDeniedAndMetadataExposesScanStatus` | PASS |
| Read/quarantine race fails closed | Shard read gate checks Content Quarantine under the Projection lock before `ensureReadableBlockLockedForReason`; `ReportDetections` waits for committed local apply from Story 5.3. | PASS |
| Replayed quarantine Projection state denies reads | `TestReadDocumentDeniedAfterQuarantineRaftReplay` | PASS |
| Corrupt quarantine state does not serve bytes | `TestReadDocumentFailsClosedForCorruptQuarantineState` | PASS |

## Redaction Notes

Story 5.4 may expose bounded scan status and bounded precondition reason. It must
not expose Document bytes, scanner rule text, raw signature names, clamd/YARA
dependency logs, filesystem paths, Backend keys, trace IDs, request IDs, gRPC
metadata, or unbounded scanner payloads.

## Final Decision

| Acceptance Criterion | Decision | Evidence |
| --- | --- | --- |
| AC-5.4.1 | PASS | `TestReadDocumentDeniesQuarantinedDocument`, `TestReadDocumentQuarantinedReturnsFailedPreconditionWithoutSend`, `TestGRPCQuarantinedReadDeniedAndMetadataExposesScanStatus` |
| AC-5.4.2 | PASS | `TestQuarantineMetadataScanStatusStaysAvailable`, `TestDocumentMetadataScanStatusRoundTrip`, `TestGRPCQuarantinedReadDeniedAndMetadataExposesScanStatus` |
| AC-5.4.3 | PASS | Shard read gate before Block restore/open/decrypt; `TestReadDocumentFailsClosedForCorruptQuarantineState`; final `make check` |
| AC-5.4.4 | PASS | `TestReadDocumentDeniedAfterQuarantineRaftReplay`, `TestQuarantineMetadataScanStatusStaysAvailable`, final `make check` |
