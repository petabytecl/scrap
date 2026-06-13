# Epic 5 Story 5.6 Evidence: `scrapctl` Quarantine Operator Workflow

Status: pass

## Scope

Story 5.6 proves the operator CLI can list, inspect, confirm, release, and render evidence for Content Quarantine through the existing admin HTTP surface. This artifact does not close scanner runtime behavior or Epic 5; Story 5.7 owns content-safety closure evidence.

## Baseline

- Baseline commit: `c8ac14e8a803ff08c4deb4af7596d1e91ead97d5`
- Previous story evidence: `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md`
- Admin HTTP endpoints:
  - `GET /admin/quarantine/documents`
  - `GET /admin/quarantine/document`
  - `POST /admin/quarantine/confirm`
  - `POST /admin/quarantine/release`

## Changed Boundaries

| Boundary | Change | Evidence |
| --- | --- | --- |
| `internal/scrapctl` | Added `scrapctl quarantine list|inspect|confirm|release|evidence`, redacted output DTOs, typed failure handling, and evidence report writing. | `internal/scrapctl/quarantine.go`, `internal/scrapctl/quarantine_test.go` |
| `cmd/scrapctl` | Entrypoint remained thin; help test updated for new command visibility. | `cmd/scrapctl/main_test.go` |
| `internal/admin` | No production admin handler change; CLI consumes existing Story 5.5 endpoints. | Touched-package tests passed. |
| `internal/shard` / `internal/index` / Raft | No change; authority remains behind admin HTTP and Shard/Raft from Story 5.5. | `make check` passed. |

## Acceptance Evidence

| AC | Evidence | Result |
| --- | --- | --- |
| AC-5.6.1 | `TestQuarantineListCallsAdminHTTPAndRedactsOutput`, `TestQuarantineInspectJSONRedactsRawIdentity`, `TestQuarantineTransportErrorRedactsIdentity`, changed-file leak scans. | PASS |
| AC-5.6.2 | `TestQuarantineConfirmPostsIdentityAndReportsCommittedOutcome`, `TestQuarantineReleaseReportsTypedHTTPFailureWithoutLeak`, `TestQuarantineHTTPFailureSanitizesRawBody`, `TestQuarantineHTTPFailureUnknownReasonIsBounded`, `TestQuarantineDecisionRejectsMalformedSuccessResponse`, `TestQuarantineRejectsAdminURLQueryFragmentOrCredentials`. | PASS |
| AC-5.6.3 | `TestQuarantineEvidenceWritesReportAndRedactionChecks`, `TestQuarantineEvidenceRejectsFilteredPathLeakWithNoRecords`, evidence file mode assertion, atomic evidence write path, report route proof, stdout/stderr/report redaction checks. | PASS |

## Verification Log

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Usage' -count=1` - PASS after implementation.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/quarantine ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Admin|Audit|Authorization|RateLimit|Evidence|Redact' -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/quarantine ./internal/scrapctl ./cmd/scrapctl -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Evidence|Redact|Usage' -count=1` - PASS after BMAD review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/quarantine ./internal/scrapctl ./cmd/scrapctl -run 'Quarantine|Admin|Audit|Authorization|RateLimit|Evidence|Redact' -count=1` - PASS after BMAD review fixes.
- `git diff --check` - PASS.
- `make proto-check` - PASS.
- `scripts/check-e2e-gates.sh` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after BMAD review fixes.

## Redaction Checks

- Credential-pattern scan over changed files - PASS, no matches.
- Quarantine-sensitive output pattern scan over changed files - PASS, no matches.
- CLI tests assert raw Transaction and Document identity values are present only in admin request inputs and absent from text, JSON, errors, and evidence reports.
- Post-review tests assert transport errors, fallback HTTP bodies, unknown admin reasons, malformed success responses, and filtered empty evidence runs do not leak raw Transaction or Document identity values.
- Evidence report includes redaction checks for stdout, stderr, and report surfaces.

## Final Gate

PASS - Story 5.6 passed BMAD code review after review fixes. This does not close Epic 5; Story 5.7 still owns content-safety closure evidence.
