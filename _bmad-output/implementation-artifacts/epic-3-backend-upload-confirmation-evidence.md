# Epic 3 Backend Upload Confirmation Evidence

Status: review

Story: 3.1 - Committed Backend Upload Confirmation

Baseline: `a81323c docs: create story 3.1 upload confirmation`

## Scope

This artifact covers the Story 3.1 upload-confirmation evidence gate. It proves
sealed Blocks become Backend-confirmed only through committed metadata, and that
Backend upload success or failure does not enter the Document write ACK path.

It does not close upload pressure policy, local eviction, restore-first reads,
real S3/IAM rehearsal, or final V2 release readiness.

## Authority Path

1. `WriteDocument` seals a full Block through `sealAndOpenNew`.
2. The Shard records a local upload obligation and proposes `SealBlock`.
3. Committed `SealBlock` materializes a pending Upload Outbox row.
4. The Shard leader's upload controller reads pending uploads.
5. The controller uploads `.blk` and `.idx` Backend objects.
6. The controller verifies each object with `HeadObject` size and validation token.
7. Only after verification, the controller proposes `ConfirmUpload`.
8. Committed `ConfirmUpload` writes the local committed authority marker and Confirmed Upload Catalog row.
9. Pending Upload Outbox state is cleared only after committed confirmation state is persisted.

Backend PUT, HEAD, list, key shape, and Local Block Lifecycle markers are evidence
inputs only. They are not Document visibility, public routing, Shard membership,
or durable upload authority.

## Changed Boundaries

| Boundary | Change | Authority impact |
| --- | --- | --- |
| `internal/shard/upload_outbox_test.go` | Added ACK-independence and split-success recovery tests using local Backend fakes. | Test-only. No production authority change. |
| `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md` | Tracks Story 3.1 implementation and verification evidence. | BMAD tracking only. |
| `_bmad-output/implementation-artifacts/sprint-status.yaml` | Moves Story 3.1 through the BMAD workflow. | BMAD tracking only. |
| `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` | Adds this evidence table. | Evidence only. |

No proto, generated code, Backend key format, public API routing, peer/admin wire
shape, storage layout, dependency, or package ownership boundary changed.

## Evidence Rows

| AC | Claim | Command | Artifact or Test Path | Ref | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| AC-3.1.1 | `SealBlock` creates pending upload and committed `ConfirmUpload` clears it through catalog/authority state. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -run 'Test.*Upload\|Test.*Confirm' -count=1` | `internal/shard/upload_outbox_test.go`, `internal/shard/upload_apply_test.go`, `internal/index/confirmed_upload_catalog_test.go`, `internal/index/upload_outbox_test.go` | Story 3.1 pre-review working tree after `a81323c` | PASS | Focused upload/confirm package gate passed. Existing catalog, marker, duplicate, stale generation, and replay coverage remained green. |
| AC-3.1.2 | Document write ACK does not wait for Backend upload success. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestWriteDocumentAckDoesNotWaitForBackendUpload\|TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen' -count=1 -v` | `internal/shard/upload_outbox_test.go` | Story 3.1 pre-review working tree after `a81323c` | PASS | `TestWriteDocumentAckDoesNotWaitForBackendUpload` keeps Backend `.blk` upload blocked while the next `WriteDocument` returns from local durability. |
| AC-3.1.4 | Backend `.blk` and `.idx` PUT/HEAD success without committed `ConfirmUpload` does not create false committed upload state. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestWriteDocumentAckDoesNotWaitForBackendUpload\|TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen' -count=1 -v` | `internal/shard/upload_outbox_test.go` | Story 3.1 pre-review working tree after `a81323c` | PASS | `TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen` manually places Backend `.blk` and `.idx` objects without committed confirmation, asserts pending upload remains, catalog and marker stay absent, then reopens and confirms through the real Shard path. |
| AC-3.1.2, AC-3.1.4 | Race-sensitive upload worker coverage remains clean. | `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestWriteDocumentAckDoesNotWaitForBackendUpload\|TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen' -count=10 -v` | `internal/shard/upload_outbox_test.go` | Story 3.1 pre-review working tree after `a81323c` | PASS | The earlier canceled-proposal fixture was intentionally discarded as nondeterministic; the deterministic reopen fixture passed 10 race runs. |
| AC-3.1.1, AC-3.1.2, AC-3.1.4 | Existing Shard and Projection package behavior remains green after the Story 3.1 tests. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard -count=1` | `internal/index`, `internal/shard` | Story 3.1 pre-review working tree after `a81323c` | PASS | Full package-pair regression passed. |
| AC-3.1.1 - AC-3.1.4 | Documented Backend upload E2E target was checked. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/e2e -run 'TestE2EBackendUploadHappyPath\|TestE2EBackendUploadLeaderChange\|TestE2EBackendUploadAdmissionPressure\|TestE2EMultiShardBackendUploadUsesNonZeroShard' -count=1 -v` | `test/e2e/upload_e2e_test.go`, `test/e2e/multishard_evidence_e2e_test.go` | Story 3.1 pre-review working tree after `a81323c` | CONCERNS | Command passed but all targeted E2E tests skipped because `SCRAP_E2E=1` was not set. This story does not claim deployed runtime proof; real S3/IAM remains Story 6.6. |
| AC-3.1.1 - AC-3.1.4 | Package and durable-format boundaries stayed intact. | `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` | `scripts/check-package-boundaries.sh` | Story 3.1 pre-review working tree after `a81323c` | PASS | No production package-boundary changes were introduced. |
| AC-3.1.1 - AC-3.1.4 | Broad local regression gate passed. | `env GOCACHE=/tmp/scrap-v2-go-build make check` | repo root | Story 3.1 pre-review working tree after `a81323c` | PASS | Includes formatting diff check, package boundaries, buf lint/generate, generated-code diff check, golangci-lint, `go test ./...`, `go test -race ./...`, integration tests, and `scrapd`/`scrapctl` builds. |
| AC-3.1.3 | Changed Story 3.1 files do not contain secret-shaped values or raw deployed identifiers. | `rg -n --pcre2 '(?i)(api[_-]?key\|secret\|password\|token\|bearer\|authorization\|aws_access_key_id\|aws_secret_access_key\|private key\|AKIA[0-9A-Z]{16}\|ghp_[A-Za-z0-9_]{36,}\|xox[baprs]-)' <changed Story 3.1 files>` and `rg -n --pcre2 '(transaction_id\|document_name\|idempotency\|Backend key\|Backend object key\|validation token\|trace ID\|request ID\|gRPC metadata\|auth claims\|peer address\|certificate\|S3\|ListObjects\|HeadObject\|GetObject\|/shards/\|/tmp/\|/home/)' <changed Story 3.1 files>` | Story file, evidence artifact, sprint status, `internal/shard/upload_outbox_test.go` | Story 3.1 pre-review working tree after `a81323c` | PASS | Matches were expected checklist/story text, test-only validation-token fixtures, sprint story_location, bounded Backend object shape tests, and provider-doc references. No secret-shaped value or deployed raw identifier was introduced. |
| AC-3.1.1, AC-3.1.3 | Public API routing does not consume Backend inventory as authority. | `rg -n 'BackendKey\|ListObjects\|HeadObject\|GetObject\|/shards/\|S3\|backend object' internal/cmd/public_store_router.go internal/server internal/cmd/app.go internal/cmd/public_store_router_test.go` | Public composition, routing, and server surfaces | Story 3.1 pre-review working tree after `a81323c` | PASS | No matches. A wider scan that included `member_id` found only existing server log-field tests, not Backend authority or routing input. |

## Pending Evidence

- No pending Story 3.1 local evidence.
- Deployed E2E evidence is not claimed here because `SCRAP_E2E=1` was not set for the documented target.
- Real S3/IAM production rehearsal remains deferred to Story 6.6.
