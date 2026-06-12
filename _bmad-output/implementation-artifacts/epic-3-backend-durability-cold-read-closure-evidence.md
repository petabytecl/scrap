---
baseline_commit: b7aab530638d96cc5cf23903dfcc4deece8395b9
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

| Evidence item | Owner | Artifact | Current proof | Result |
| --- | --- | --- | --- | --- |
| Upload confirmation through committed metadata | Story 3.1 | `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` | Upload Outbox/Catalog tests, ACK independence test, split-success tests, `make check`, and Backend inventory authority scan. | PASS with CONCERNS for checked-but-skipped deployed E2E without `SCRAP_E2E=1`; real S3/IAM remains Story 6.6. |
| Upload pressure and safe admission | Story 3.2 | `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md` | Upload pressure, pressure recovery, telemetry bounding, rejected-write cleanup, race gate, and `make check`. | PASS with CONCERNS for checked-but-skipped deployed E2E without `SCRAP_E2E=1`; real S3/IAM remains Story 6.6. |
| Policy-gated local Block eviction | Story 3.3 | `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` | Dry-run/apply tests, retained `.idx`, metadata-only reads, Local Block Lifecycle tests, admin/scrapctl redaction tests, and `make check`. | PASS with CONCERNS for no deployed runtime eviction, no real S3/IAM, and no final release-readiness claim. |
| Restore-first all-local-copy read | Story 3.4 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` | `TestReadDocumentRestoresEvictedBlockFromBackend` starts with no local `.blk`, retained `.idx`, matching eviction marker, and committed confirmation. | PASS at local single-Member scope; CONCERNS for no deployed multi-Member all-copy proof. |
| Restore authority follows committed metadata | Story 3.4 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` | `TestReadDocumentRestoreRequiresCommittedConfirmUpload`, `TestReadDocumentRestoreRequiresMatchingEvictionMarker`, metadata-only local read tests, and current authority scan. | PASS. |
| Restore verifies before return and publishes no partial Block on failure | Story 3.4 / 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`; `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | Restore corruption, metadata mismatch, checksum mismatch, nil reader/metadata, no published `.blk`, and staging cleanup tests. | PASS. |
| Concurrent restore coalescing and cancellation/deadlines | Story 3.4 / 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`; `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | Same-Block singleflight, leader cancellation, waiter deadline, leader deadline, and metadata-read-while-download-blocked tests. | PASS with scoped concern that cross-Block global restore limiting remains outside Story 3.4. |
| Typed Backend failure mapping | Story 3.5 | `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` | Transient Backend `UNAVAILABLE`, missing/corrupt Backend object `DATA_LOSS`, retry budget, stream-read retry, server/store mapping, and public sanitization tests. | PASS. |
| Encryption interaction | Story 3.6 | `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md` | Encrypted restore, plaintext absence in Backend/restored Block bytes, key-service/key-version fail-closed cases, rewrapped envelope restore, and public crypto-unavailable sanitization tests. | PASS with CONCERNS for fixture-backed fake Transit only; production OpenBao proof remains Epic 4. |
| Redaction and leak checks | Stories 3.1-3.6; Story 3.7 | All Epic 3 evidence artifacts plus current Story 3.7 scans. | Prior story leak scans; current Story 3.7 credential and identifier scans over touched artifacts. | PASS. Current matches are allowlisted BMAD prose, story keys, local verification command paths, sprint tracker paths, and source/test identifiers only. |
| No Backend inventory authority | Stories 3.1, 3.4, 3.6, 3.7 | Upload, restore-first, encryption-restore, and this closure artifact. | Current authority scan plus tests proving restore GET uses committed key and zero HEAD/LIST restore discovery. | PASS. |

## P0 Cold-Read Matrix

| P0 item | Current evidence | Decision |
| --- | --- | --- |
| All-local-copy restore starts from no local `.blk` and retained `.idx`. | `TestReadDocumentRestoresEvictedBlockFromBackend` passed on 2026-06-12. Story 3.4 scopes this to a single-Member fixture. | PASS with CONCERNS. |
| Restore is allowed only from committed Confirmed Upload Catalog metadata. | `TestReadDocumentRestoreRequiresCommittedConfirmUpload` and `TestReadDocumentRestoreRequiresMatchingEvictionMarker` passed on 2026-06-12. | PASS. |
| Metadata-only reads stay local while Block bytes are evicted. | `TestMetadataReadsStayLocalForEvictedBlock` and `TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock` passed on 2026-06-12. | PASS. |
| Full Block is verified before Document bytes are returned. | Story 3.4 and Story 3.5 evidence cover retained `.idx`, committed metadata, Block header, Frame CRC, and Document SHA-256 failure paths; focused failure tests passed on 2026-06-12. | PASS. |
| Concurrent reads coalesce behind one same-Block restore. | `TestReadDocumentJoinsConcurrentBlockRestore` passed on 2026-06-12. | PASS. |
| Cancellation and deadlines return no partial bytes and keep restore bounded. | `TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation`, `TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore`, and `TestReadDocumentRestoreLeaderDeadlineFailsClosed` passed on 2026-06-12. | PASS. |
| Backend transient failures map to `UNAVAILABLE`. | `TestReadDocumentRestoreBackendTransientReturnsUnavailable`, `TestReadDocumentRestoreRetriesTransientBackendFailures`, and `TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed` passed on 2026-06-12. | PASS. |
| Missing/corrupt confirmed Backend objects map to `DATA_LOSS`. | `TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss`, `TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss`, and `TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss` passed on 2026-06-12. | PASS. |
| Encryption-compatible restore preserves ciphertext storage and normal envelope read path. | `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`, `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable`, `TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected`, and `TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope` passed on 2026-06-12. | PASS with CONCERNS for fixture-backed fake Transit; production OpenBao remains Epic 4. |
| Redaction/no raw identifiers or Backend keys in public/evidence output. | Prior Story 3.1-3.6 leak scans plus current Story 3.7 scans. | PASS. |

No P0 cold-read row is missing current evidence at the Epic 3 local/package
scope, so closure is not FAIL. The scoped limitations above keep the decision
at CONCERNS rather than PASS.

## Backend Authority Review

Backend inventory, list, and HEAD output exists in provider and evidence code,
but this closure review found no hot public read/write path treating it as
authority.

Current scan results:

- `rg -n 'HeadObject|ListObjects|ListObject|inventory|Backend inventory|GetObject|PutObject|ConfirmUpload|Confirmed Upload Catalog' internal/server internal/store --glob '!**/*_test.go'` returned no matches. This is PASS: the public transport and Store contract do not consume Backend discovery as authority.
- The same pattern over Shard hot-path files found only expected authority paths:
  - `internal/shard/apply.go` applies committed `ConfirmUpload` Raft commands.
  - `internal/shard/eviction_apply.go`, `internal/shard/read_lifecycle.go`, `internal/shard/restore.go`, and `internal/shard/shard.go` require committed Confirmed Upload authority before eviction/restore.
  - `internal/shard/upload_controller.go` uses Backend `PutObject` and `HeadObject` to verify an upload before proposing `ConfirmUpload`; this is upload verification, not read/write authority.
  - `internal/shard/restore.go` uses one full-object Backend `GetObject` from committed restore metadata; it does not list or discover Backend inventory for read authority.
- The broad scan over Shard tests, Backend providers, evidencebundle code, and E2E code returned 328 matches. They are allowed provider implementations, provider tests, Shard authority tests, restore/upload tests, and evidence/E2E inspection code, not public hot-path authority.

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
| Broad Backend discovery scan | 2026-06-12T00:15:10-04:00 | PASS with classification | 328 matches in provider, test, evidence, and e2e code; no public hot-path Backend inventory authority found. |
| Credential leak scan | 2026-06-12T00:16:10-04:00 | PASS with allowlisted matches | Matches are sprint authorization story names and Story 3.7 closure prose naming token/auth/key leak classes. No real secret material found. |
| Identifier leak scan | 2026-06-12T00:16:10-04:00 | PASS with allowlisted matches | Matches are BMAD prose, sprint tracker paths, local verification command paths, and source/test identifier names. No deployed public/log/metric/operator leak found. |
| Whitespace diff check | 2026-06-12T00:16:10-04:00 | PASS | `git diff --check`. |
| Full repository check gate | 2026-06-12T00:18:27-04:00 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build make check`; included format diff, package-boundary checks, buf lint/generate diff, golangci-lint, `go test ./...`, `go test -race ./...`, integration-tagged LocalStack/OpenBao tests, and `scrapd`/`scrapctl` builds. |

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
