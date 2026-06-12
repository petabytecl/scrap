---
baseline_commit: b7aab530638d96cc5cf23903dfcc4deece8395b9
baseline_scope: closure evaluation baseline after the Story 3.7 story file was created
story_creation_baseline_commit: 688b2095bc9554549f212d0e6ed7c52e00d76fa6
evaluated_at: 2026-06-12T00:15:51-04:00
story: 3.7
---

# Epic 3 Backend Durability and Cold-Read Closure Evidence

Status: complete-with-concerns

## Closure Decision

Decision: CONCERNS

Epic 3 has current, attributable evidence for the P0 restore-first cold-read
semantics required by Story 3.7: restore-on-read from committed Confirmed
Upload Catalog metadata, full-Block verification before return, same-Block
restore singleflight, typed Backend failure mapping, cancellation/deadline
behavior, fixture-backed encryption interaction, and redaction/no Backend
inventory authority evidence.

This is not a PASS because several evidence rows remain scoped to local,
single-Member, fixture, or checked-but-skipped deployed gates. These concerns do
not hide a missing Epic 3 P0 cold-read proof, but they also do not support final
V2 release readiness.

Epic 3 closure would be FAIL if any P0 row below lost current evidence. This
artifact does not mark `epic-3` done in `sprint-status.yaml`; it records the
closure evidence decision for Story 3.7.

## Evidence Inventory

| Evidence item | Owner | Artifact | Artifact status | Proof command or test name | Decision | Concern/gap |
| --- | --- | --- | --- | --- | --- | --- |
| Upload confirmation through committed metadata | Story 3.1 | `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` | `done` | `go test ./internal/index ./internal/shard -run Test.*Upload -count=1`; `TestWriteDocumentAckDoesNotWaitForBackendUpload`; `TestUploadAndConfirmRetriesAfterInterruptedConfirmProposal`; `TestShardUploadProcessorIgnoresBackendObjectsWithoutCommittedConfirmAfterReopen`; `make check`; public routing Backend-authority scan. | CONCERNS | Package proof is current. The documented E2E upload target was checked but skipped without `SCRAP_E2E=1`; real S3/IAM remains Story 6.6. |
| Upload pressure and safe admission | Story 3.2 | `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md` | `done` | `TestUploadOutboxRefreshPressureCombinesCommittedAndLocalObligations`; `TestUploadPressureRejectsWritesAndResumesAfterDrain`; `TestUploadOTelMetricsUsesBoundedAttributes`; `TestSealTriggeredUploadPressureRejectsCurrentWrite`; `go test -race ./internal/shard -run 'TestUploadPressure|TestSealTriggeredUploadPressure' -count=10`; `make check`. | CONCERNS | Package proof is current. The deployed upload-pressure E2E target was checked but skipped without `SCRAP_E2E=1`; real S3/IAM remains Story 6.6. |
| Policy-gated local Block eviction | Story 3.3 | `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` | `complete` | `TestCreateEvictionPlanDoesNotMutateLocalBlockState`; `TestApplyEvictionPlanPreservesIndexMetadataReads`; `TestPlanJSONOmitsBackendKeys`; `TestEvictionPlanPostsRequestAndPrintsEvidence`; `TestEvictionStatusPrintsFinalCampaignEvidence`; focused Shard/admin/scrapctl eviction gates; `make check`. | CONCERNS | Local/package proof is current. No deployed runtime eviction, real S3/IAM, or final release-readiness proof is claimed. |
| Restore-first all-local-copy read | Story 3.4 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` | `complete-with-concerns` | `TestReadDocumentRestoresEvictedBlockFromBackend` starts with no local `.blk`, retained `.idx`, matching eviction marker, committed confirmation, one full-object Backend `GetObject`, and zero Backend `HeadObject`/`ListObjects`. | CONCERNS | Epic 3 local/package P0 proof is current. No deployed multi-Member all-copy proof is claimed. |
| Restore authority follows committed metadata | Story 3.4 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` | `complete-with-concerns` | `TestReadDocumentRestoreRequiresCommittedConfirmUpload`; `TestReadDocumentRestoreRequiresMatchingEvictionMarker`; `TestMetadataReadsStayLocalForEvictedBlock`; `TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock`; current hot-path authority scan. | PASS | No gap at Epic 3 scope. |
| Restore verifies before return and publishes no partial Block on failure | Story 3.4 / 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`; `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | `complete-with-concerns`; `complete` | `TestReadDocumentRestoreSizeMismatchReturnsDataLoss`; `TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss`; `TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss`; `TestReadDocumentRestoreCorruptHeaderReturnsDataLoss`; `TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss`; `TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss`; Story 3.4 staging/no-publish assertions. | PASS | No gap at Epic 3 scope. |
| Concurrent restore coalescing and cancellation/deadlines | Story 3.4 / 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`; `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | `complete-with-concerns`; `complete` | `TestReadDocumentJoinsConcurrentBlockRestore`; `TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation`; `TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore`; `TestReadDocumentRestoreLeaderDeadlineFailsClosed`; `TestReadDocumentRestoreDoesNotBlockMetadataReadsWhileDownloading`; focused Story 3.5 retry/cancellation gates. | CONCERNS | Same-Block restore coalescing and deadline behavior are current. Cross-Block global restore limiting remains outside Story 3.4. |
| Typed Backend failure mapping | Story 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | `complete` | `TestReadDocumentRestoreBackendTransientReturnsUnavailable`; `TestReadDocumentRestoreRetriesTransientBackendFailures`; `TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed`; `TestReadDocumentRestoreRetriesTransientBackendReadFailure`; `TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss`; `TestReadDocumentRestoreDataLossReturnsErrorInfoDetail`; Store unavailable/data-loss/resource-exhausted tests. | PASS | No gap at Epic 3 scope. |
| Encryption interaction | Story 3.6 | `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md` | `done` | `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`; `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`; `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected`; `TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope`; `TestReadDocumentCryptoUnavailableReturnsSanitizedErrorInfoDetail`; `TestGenerateFailsWhenEncryptedRestoreProofIsMissing`. | CONCERNS | Fixture-backed fake Transit and OpenBao adapter tests are current. Production OpenBao proof remains Epic 4. |
| Redaction and leak checks | Stories 3.1-3.6; Story 3.7 | All Epic 3 evidence artifacts plus current Story 3.7 scans. | `done`; `done`; `complete`; `complete-with-concerns`; `complete`; `done`; `complete-with-concerns` | Story 3.1-3.6 leak-scan rows; Story 3.7 credential scan; Story 3.7 identifier scan; `make check`. | PASS | Current matches are allowlisted BMAD prose, story keys, local verification command paths, sprint tracker paths, and source/test identifiers only. |
| No Backend inventory authority | Stories 3.1, 3.4, 3.6, 3.7 | Upload, restore-first, encryption-restore, and this closure artifact. | `done`; `complete-with-concerns`; `done`; `complete-with-concerns` | Hot-path authority scan over `internal/server` and `internal/store`; Shard authority scan; `TestReadDocumentRestoresEvictedBlockFromBackend`; `TestReadDocumentRestoreRequiresCommittedConfirmUpload`; `TestReadDocumentRestoreRequiresMatchingEvictionMarker`; `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`. | PASS | Backend PUT/HEAD/list/inventory may exist in provider, upload verification, diagnostics, evidence, and tests, but it is not public read/write authority. |

## P0 Cold-Read Matrix

| P0 item | Current evidence | Decision | Concern/gap |
| --- | --- | --- | --- |
| All-local-copy restore starts from no local `.blk` and retained `.idx`. | `TestReadDocumentRestoresEvictedBlockFromBackend` passed on 2026-06-12. Story 3.4 scopes this to a single-Member fixture. | CONCERNS | Current Epic 3 local/package proof is present; deployed multi-Member all-copy proof is not claimed. |
| Restore is allowed only from committed Confirmed Upload Catalog metadata. | `TestReadDocumentRestoreRequiresCommittedConfirmUpload` and `TestReadDocumentRestoreRequiresMatchingEvictionMarker` passed on 2026-06-12. | PASS | No gap. |
| Metadata-only reads stay local while Block bytes are evicted. | `TestMetadataReadsStayLocalForEvictedBlock` and `TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock` passed on 2026-06-12. | PASS | No gap. |
| Full Block is verified before Document bytes are returned. | Story 3.4 and Story 3.5 evidence cover retained `.idx`, committed metadata, Block header, Frame CRC, and Document SHA-256 failure paths; focused failure tests passed on 2026-06-12. | PASS | No gap. |
| Concurrent reads coalesce behind one same-Block restore. | `TestReadDocumentJoinsConcurrentBlockRestore` passed on 2026-06-12. | PASS | No same-Block gap. |
| Cancellation and deadlines return no partial bytes and keep restore bounded. | `TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation`, `TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore`, and `TestReadDocumentRestoreLeaderDeadlineFailsClosed` passed on 2026-06-12. | PASS | No gap at Epic 3 same-Block scope. |
| Backend transient failures map to `UNAVAILABLE`. | `TestReadDocumentRestoreBackendTransientReturnsUnavailable`, `TestReadDocumentRestoreRetriesTransientBackendFailures`, and `TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed` passed on 2026-06-12. | PASS | No gap. |
| Missing/corrupt confirmed Backend objects map to `DATA_LOSS`. | `TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss`, `TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss`, and `TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss` passed on 2026-06-12. | PASS | No gap. |
| Encryption-compatible restore preserves ciphertext storage and normal envelope read path. | `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`, `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`, `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected`, and `TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope` passed on 2026-06-12. | CONCERNS | Fixture-backed fake Transit and adapter proof are current; production OpenBao remains Epic 4. |
| Redaction/no raw identifiers or Backend keys in public/evidence output. | Prior Story 3.1-3.6 leak scans plus current Story 3.7 scans. | PASS | No deployed public/log/metric/operator leak found. |

No P0 cold-read row is missing current evidence at the Epic 3 local/package
scope, so closure is not FAIL. The scoped limitations above keep the decision
at CONCERNS rather than PASS.

## Backend Authority Review

Backend inventory, list, and HEAD output exists in provider and evidence code,
but this closure review found no hot public read/write path treating it as
authority.

Current scan results:

- `rg -n 'HeadObject|ListObjects|ListObject|inventory|Backend inventory|GetObject|PutObject|ConfirmUpload|Confirmed Upload Catalog' internal/server internal/store --glob '!**/*_test.go'` returned no matches and exited 1. This is PASS: the public transport and Store contract do not consume Backend discovery as authority, and exit 1 is the expected `rg` no-match result.
- The same pattern over Shard hot-path files found only expected authority paths:
  - `internal/shard/apply.go` applies committed `ConfirmUpload` Raft commands.
  - `internal/shard/eviction_apply.go`, `internal/shard/read_lifecycle.go`, `internal/shard/restore.go`, and `internal/shard/shard.go` require committed Confirmed Upload authority before eviction/restore.
  - `internal/shard/upload_controller.go` uses Backend `PutObject` and `HeadObject` to verify an upload before proposing `ConfirmUpload`; this is upload verification, not read/write authority.
  - `internal/shard/restore.go` uses one full-object Backend `GetObject` from committed restore metadata; it does not list or discover Backend inventory for read authority.
- The broad scan over Shard, server, Store, Backend provider, local lifecycle,
  eviction, evidencebundle, and E2E paths returned 399 matches on
  2026-06-12T00:29:46-04:00. Category classification:

| Category | Match count | Files | Classification |
| --- | ---: | --- | --- |
| Backend provider implementation/tests | 158 | `internal/backend/backend.go`, `internal/backend/fs.go`, `internal/backend/fs_internal_test.go`, `internal/backend/fs_test.go`, `internal/backend/s3.go`, `internal/backend/s3_test.go` | Allowed provider API implementation and provider tests. |
| Shard source authority paths | 66 | `internal/shard/apply.go`, `internal/shard/block_trace.go`, `internal/shard/block_upload_lifecycle.go`, `internal/shard/confirmed_upload_authority.go`, `internal/shard/eviction_apply.go`, `internal/shard/projection_rebuilder.go`, `internal/shard/read_lifecycle.go`, `internal/shard/restore.go`, `internal/shard/shard.go`, `internal/shard/upload.go`, `internal/shard/upload_controller.go`, `internal/shard/upload_outbox_events.go` | Allowed committed ConfirmUpload authority, upload verification, restore full-object GET from committed metadata, and trace/event plumbing. |
| Shard authority/upload/restore tests | 166 | `internal/shard/*_test.go` | Allowed tests that prove committed authority, upload split-success behavior, restore behavior, and no Backend discovery authority. |
| Store contract tests | 5 | `internal/store/proto_raft_contract_test.go` | Allowed protocol-contract assertions for committed Backend object metadata. |
| E2E upload tests | 4 | `test/e2e/upload_e2e_test.go` | Allowed runtime evidence target; not public hot-path authority. |

Highest-volume files were `internal/backend/s3_test.go` with 79 matches,
`internal/backend/fs_test.go` with 54, `internal/shard/restore_test.go` with 38,
`internal/shard/upload_outbox_test.go` with 37,
`internal/shard/upload_apply_test.go` with 22, and
`internal/shard/upload_controller_boundary_test.go` with 19. None of these
files are public read/write authority paths.

## Executable Closure Support

No `internal/scrapctl/evidencebundle` code change was made for Story 3.7.

Reason: Story 3.7 requires an Epic 3 closure evidence artifact with explicit
PASS/CONCERNS/FAIL language. The existing `internal/scrapctl/evidencebundle`
`Gate` is a Tier 3/security gate with fields such as `security_mode_recorded`,
`authorization_denials_recorded`, `encryption_outcomes_recorded`, and
`phase5_gate_recorded`. Reusing that schema for Epic 3 closure would conflate
security/release gate evidence with Backend durability closure evidence. This
story records the closure decision document-only unless a later story creates a
named Epic 3 closure command/schema.

## Verification Log

| Check | Time | Result | Notes |
| --- | --- | --- | --- |
| Restore authority focused tests | 2026-06-12T00:14:44-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend|TestReadDocumentRestoreRequiresCommittedConfirmUpload|TestReadDocumentRestoreRequiresMatchingEvictionMarker|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock' -count=1 -v`. |
| Restore concurrency/cancellation focused tests | 2026-06-12T00:14:44-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed|TestReadDocumentRestoreDoesNotBlockMetadataReadsWhileDownloading' -count=1 -v`. |
| Restore failure focused tests | 2026-06-12T00:14:44-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable|TestReadDocumentRestoreRetriesTransientBackendFailures|TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss' -count=1 -v`. |
| Encrypted restore focused tests | 2026-06-12T00:14:44-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedReadDocumentRestoresThenUsesEnvelopePath|TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable|TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected|TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope' -count=1 -v`. |
| Affected package regression | 2026-06-12T00:15:10-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/eviction ./internal/backend ./internal/encryption ./internal/scrapctl/evidencebundle -count=1`. |
| Public/Store Backend authority scan | 2026-06-12T00:15:51-04:00 | PASS | No matches in non-test `internal/server` or `internal/store`. |
| Shard Backend authority scan | 2026-06-12T00:15:51-04:00 | PASS | Matches are committed ConfirmUpload authority, upload verification, or restore full-object GET from committed metadata. |
| Broad Backend discovery scan | 2026-06-12T00:29:46-04:00 | PASS with classification | 399 matches classified as Backend provider implementation/tests, Shard source authority paths, Shard authority/upload/restore tests, Store contract tests, and E2E upload tests; no public hot-path Backend inventory authority found. |
| Credential leak scan | 2026-06-12T00:29:46-04:00 | PASS with allowlisted matches | 171 matches after review-fix prose updates; matches were source/test identifiers, OpenBao configuration fields, local test fixture values, redaction tests, and BMAD evidence prose. No real secret material found. |
| Identifier leak scan | 2026-06-12T00:29:46-04:00 | PASS with allowlisted matches | 167 matches after review-fix prose updates; matches were BMAD prose, sprint tracker paths, local verification command paths, redaction tests, and source/test identifier names. No deployed public/log/metric/operator leak found. |
| Whitespace diff check | 2026-06-12T00:16:10-04:00 | PASS | `git diff --check`. |
| Full repository check gate | 2026-06-12T00:18:27-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build make check`; included format diff, package-boundary checks, buf lint/generate diff, golangci-lint, `go test ./...`, `go test -race ./...`, integration-tagged LocalStack/OpenBao tests, and `scrapd`/`scrapctl` builds. |
| Review-fix whitespace diff check | 2026-06-12T00:29:46-04:00 | PASS | `git diff --check`. |
| Review-fix full repository check gate | 2026-06-12T00:31:02-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build make check`; included format diff, package-boundary checks, buf lint/generate diff, golangci-lint, `go test ./...`, `go test -race ./...`, integration-tagged LocalStack/OpenBao tests, and `scrapd`/`scrapctl` builds. |

## Leak Scan Evidence

The Story 3.7 leak scans were run with shell variables for the credential and
identifier patterns to avoid copying detector-heavy regexes into evidence rows.
The broad scans include source and tests because Story 3.7 reviewed evidence
claims, not only deployed output paths.

| Scan | Match count | Highest-volume files | Classification |
| --- | ---: | --- | --- |
| Credential-shaped terms | 171 | `internal/encryption/openbao_test.go` (16), `internal/encryption/openbao.go` (14), `internal/backend/s3_test.go` (13), `internal/shard/eviction_apply_test.go` (9), `internal/shard/upload_apply_test.go` (8), `internal/server/route_unavailable_test.go` (8), closure artifact (7), `internal/shard/upload_outbox_test.go` (6), `internal/shard/restore_test.go` (6), `internal/server/restore_unavailable_test.go` (6) | OpenBao config field names, test fixture values, authorization-denial tests, validation-token marker fields, redaction tests, and BMAD evidence prose. No production secret value, bearer value, AWS key, GitHub token, or private key material was found. |
| Raw identifier / path terms | 167 | Story 3.7 story file (17), closure artifact (11), `internal/backend/fs_test.go` (10), `internal/eviction/redaction.go` (9), `internal/server/telemetry_test.go` (8), `internal/server/identifier_mode_test.go` (8), `internal/store/validation.go` (7), `internal/shard/eviction_apply_test.go` (7), `internal/server/restore_unavailable_test.go` (7), `internal/server/find_documents_test.go` (7) | BMAD evidence prose, local verification paths, test fixture Backend keys, redaction denylist entries, validation field names, and tests asserting public/log/metric absence. No deployed operator output, metric label, log field, public status detail, or admin response leak was found. |

## Leak Scan Allowlist

Current Story 3.7 leak scans may match only:

- BMAD story/evidence prose naming forbidden leak classes.
- Story keys and sprint tracker text.
- Local developer command paths such as `/tmp` or `/home` only when they are
  literal verification commands, not deployed output.
- Source or test identifiers whose names intentionally contain terms like
  `BackendKey`, `Authorization`, `Token`, or `document_name` to assert redaction
  or glossary behavior.

No deployed metric label, log field, public status detail, admin response, or
operator-facing output may expose raw Transaction IDs, Document names, Backend
object keys, key material, wrapped-key ciphertext, tokens, auth claims, gRPC
metadata, peer addresses, filesystem paths, or dependency detail strings.

## Remaining Scope

- Story 3.1 and Story 3.2 checked E2E targets without `SCRAP_E2E=1`; deployed
  E2E upload/upload-pressure proof is not claimed here.
- Story 3.4 all-local-copy restore proof is local single-Member fixture proof;
  deployed multi-Member all-copy evidence is not claimed here.
- Story 3.4 does not implement or claim cross-Block global restore concurrency
  limiting, restore queueing, retry/disk runway production health, or deployed
  production-profile restore evidence.
- Story 3.6 uses deterministic fake Transit and existing OpenBao adapter tests
  only. Production OpenBao policy, token custody, live service interaction, and
  production rehearsal remain Epic 4.
- Real S3/IAM production rehearsal remains Story 6.6 / Epic 6 release evidence.
- Final V2 release readiness remains Epic 6 and is not closed by this artifact.
