# Epic 5 Story 5.3 Evidence: QuarantineDocument Raft Command and Projection State

Status: PASS

Baseline commit: `b4d43cdbe54004e2234266af9bf7cafa65deea14`
Story: `_bmad-output/implementation-artifacts/5-3-quarantinedocument-raft-command-and-projection-state.md`

## Scope

Story 5.3 adds Raft-owned Content Quarantine metadata. Closure is limited to:

- Adding a metadata-only `QuarantineDocument` Raft command.
- Materializing sparse Content Quarantine state in the Shard Pebble Projection.
- Keeping `internal/avscan` as detection reporting, not quarantine authority.
- Proving committed quarantine metadata survives replay/restart without scanner memory.

Out of scope for this evidence:

- `ReadDocument` denial and public `scan_status` fields.
- Admin HTTP list, inspect, confirm, or release.
- `scrapctl` quarantine operator UX.
- Epic 5 closure.

## Changed Boundaries

| Boundary | Change |
| --- | --- |
| `proto/scrap/v1/raft.proto` | Add metadata-only `QuarantineDocument` command. |
| `gen/go/scrap/v1/raft.pb.go` | Regenerated from proto source. |
| `internal/index` | Add sparse Content Quarantine Projection state. |
| `internal/avscan` | Report bounded Document detections without owning authority. |
| `internal/shard` | Propose and apply quarantine metadata through Raft authority. |
| BMAD artifacts | Track story evidence and local verification. |

## Raft Command Summary

`proto/scrap/v1/raft.proto` adds:

- `RaftCommand.quarantine_doc = 8`;
- `QuarantineDocument` with `transaction_id`, `document_name`, `block_id`,
  `detected_at_us`, `scan_type`, and `reason`;
- `QuarantineScanType` enum with `INITIAL` and `RESCAN`; and
- `QuarantineReason` enum with `SCANNER_DETECTION`.

The command carries metadata only. It does not carry Document bytes, scanner
payload bytes, rule text, signature names, dependency logs, file paths, Backend
keys, trace IDs, request IDs, or gRPC metadata.

## Projection Keys

ADR 0008 names the sparse key shape:

| Key | Purpose |
| --- | --- |
| `q\x01<transaction_id>\x00<document_name>` | Per-Document Content Quarantine metadata. |

Unlike scanner watermarks, Content Quarantine keys are replicated read-side authority
and must participate in Projection consistency hashing.

The stored value is a compact versioned record containing `block_id`,
`detected_at_us`, `scan_type`, and `reason`. The key contains the authoritative
Document identity.

## Verification Plan

| Area | Command / Evidence | Result |
| --- | --- | --- |
| Proto compatibility | `make proto-check` | PASS |
| Index quarantine tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index -run ContentQuarantine -count=1` | PASS |
| Scanner detection tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/avscan -run 'Detection|Quarantine' -count=1` | PASS |
| Shard Raft/apply tests | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Quarantine|ApplySpan' -count=1` | PASS |
| Targeted packages | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/avscan ./internal/shard ./internal/admin ./internal/cmd ./internal/scrapctl -count=1` | PASS |
| Static diff check | `git diff --check` | PASS |
| E2E gate policy | `scripts/check-e2e-gates.sh` | PASS |
| Broad local gate | `env GOCACHE=/tmp/scrap-v2-go-build make check` | PASS |
| Secret shape scan | `rg -n --pcre2 "$secret_shape_pattern" $scan_scope` | PASS - no matches |
| Quarantine-sensitive scan | `rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope` | PASS - no matches |

## Replay and Restart Evidence

| Scenario | Evidence | Result |
| --- | --- | --- |
| Duplicate quarantine command is idempotent | `TestApplyQuarantineDocumentIsDuplicateSafe`, `TestContentQuarantineDuplicatePutIsIdempotent` | PASS |
| Committed quarantine state survives Projection reopen/restart | `TestApplyQuarantineDocumentSurvivesProjectionReopen` | PASS |
| Scanner memory absent after restart does not remove quarantine state | `TestApplyQuarantineDocumentSurvivesProjectionReopen` reopens Projection from disk without scanner memory. | PASS |
| Corrupt quarantine value fails closed at quarantine lookup boundary | `TestContentQuarantineRejectsCorruptValues` | PASS |

## Redaction Notes

Quarantine evidence must not expose Document bytes, scanner rule text, raw signature
names, clamd/YARA dependency logs, filesystem paths, Backend keys, trace IDs,
request IDs, gRPC metadata, or unbounded scanner payloads. Persisted quarantine
metadata may contain `(transaction_id, document_name)` because it is authoritative
Projection identity; logs, metrics, spans, and external evidence must still use
bounded or hashed identifiers by default.

## Final Decision

| Acceptance Criterion | Decision | Evidence |
| --- | --- | --- |
| AC-5.3.1 | PASS | `proto/scrap/v1/raft.proto`, `TestShardReportDetectionsProposesQuarantineCommand`, `make proto-check` |
| AC-5.3.2 | PASS | `internal/index/content_quarantine.go`, `TestContentQuarantineRoundTrip`, `TestContentQuarantineAffectsStreamingHash` |
| AC-5.3.3 | PASS | `internal/avscan` detection reporter boundary, `internal/shard/content_quarantine.go`, `TestSchedulerReportsDetectionsBeforePersistingProgress` |
| AC-5.3.4 | PASS | `TestApplyQuarantineDocumentSurvivesProjectionReopen`, `TestApplyQuarantineDocumentIsDuplicateSafe`, `TestContentQuarantineRejectsCorruptValues` |
