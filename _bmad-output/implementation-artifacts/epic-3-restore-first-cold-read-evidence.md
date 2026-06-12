# Epic 3 Restore-First Cold Read Evidence

Status: complete

Story: 3.4 - Restore-First Cold Read Path

Story status: review

Implementation baseline: `e28ec3cb7208c06338f40e36c51903dcd0bd8fef`

## Scope

This artifact covers the Story 3.4 restore-first cold-read evidence gate. It
proves that `ReadDocument` restores a confirmed, locally evicted Block from the
Backend to local staged storage, verifies the full Block, publishes only
verified bytes, and then serves through the normal local read path.

It does not close the full restore failure taxonomy, retry-budget design,
encryption-compatible restore evidence, production OpenBao proof, real S3/IAM
rehearsal, final Epic 3 closure, or V2 release readiness.

## Authority Path

1. `ReadDocument` resolves `(transaction_id, document_name)` through the Pebble
   Projection and retained local `.idx` metadata.
2. The Shard classifies the target Block with Local Block Lifecycle.
3. An `evicted` Block is restore-eligible only when the retained eviction marker
   matches committed Confirmed Upload Catalog metadata.
4. The Shard starts or joins the per-Block restore call.
5. Restore downloads the confirmed Backend `.blk` object to a local staging
   file using bounded copy buffers.
6. Restore validates Backend object metadata against committed upload metadata.
7. Restore verifies the staged `.blk` against retained `.idx`, Block header,
   Frame CRCs, and Document SHA-256 before publication.
8. Successful restore atomically publishes the local `.blk`, writes a restore
   marker, removes the eviction marker, records restore metrics, and serves
   bytes through the normal local Block reader.
9. Failed restore removes staging files, keeps the eviction marker, records a
   bounded failure reason, and returns no reader / no partial bytes.

Backend PUT, HEAD, list, object existence, Backend keys, local file presence,
hostnames, peer addresses, and Local Block Lifecycle markers are evidence inputs
only. They are not Document visibility authority, durable upload authority,
Shard membership authority, or public read/write routing authority.

## Changed Boundary List

- Story 3.4 may add or strengthen tests and evidence for existing restore,
  Local Block Lifecycle, Store/server error mapping, restore metrics, and
  production-profile startup gates.
- Story 3.4 may patch Shard restore/read hardening if tests expose a gap in
  authority, verification, concurrency, cancellation, backpressure, cleanup, or
  bounded public error behavior.
- Story 3.4 must not add direct Backend streaming, per-Frame remote reads, a new
  cold-read package, Block/Frame layout changes, Backend object key changes,
  public/peer/admin proto changes, Confirmed Upload Catalog schema changes,
  Pebble key-prefix changes, storage identity changes, or production security
  policy changes.

## Evidence Checklist

| AC | Required proof | Current coverage | Gap before closure |
| --- | --- | --- | --- |
| AC-3.4.1 | `ReadDocument` on an evicted confirmed Block restores the full Block from Backend using committed Confirmed Upload Catalog authority only. | PASS: `TestReadDocumentRestoresEvictedBlockFromBackend` now starts with no local `.blk`, retained `.idx`, matching eviction marker, then asserts one full-object `GetObject` to the committed Backend key and zero Backend `HeadObject`/`ListObjects`; `TestReadDocumentRestoreRequiresCommittedConfirmUpload` and `TestReadDocumentRestoreRequiresMatchingEvictionMarker` fail closed before Backend GET when authority is absent or stale; metadata-only tests prove no restore. | Single-Member fixture only; no deployed multi-Member all-copy eviction evidence claimed. |
| AC-3.4.2 | Restore verifies retained `.idx`, committed metadata, Block header, Frame CRCs, and Document SHA-256 before return; failed restore publishes no partial bytes. | PASS: restore tests now cover Backend size mismatch, validation-token mismatch, missing object, corrupt object, corrupt Block header, corrupt Frame header, corrupt Document SHA-256, nil reader / zero metadata on failed restore, no published `.blk`, retained eviction marker, no restore marker, and no staging-file leftovers; successful restore verifies the published Block, records backend/read restore marker, removes eviction marker, classifies hot/serving-allowed, and reads through the normal local path. `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath` proves the restored encrypted Block still uses the envelope path with fake Transit. | Production OpenBao/encryption-compatible closure remains Story 3.6 scope; no deployed encryption restore evidence claimed. |
| AC-3.4.3 | Concurrent reads for the same Block coalesce behind one restore and cancellation/timeout/backpressure behavior is bounded. | PASS: focused tests prove five same-Block readers make one Backend `GetObject`, leader cancellation returns no reader/metadata without canceling a follower, waiter deadline returns no reader/metadata without canceling the leader restore, leader deadline fails closed without publishing, and metadata reads proceed while Backend download is blocked. Current backpressure scope is per-Block coalescing plus caller cancellation/deadlines; no global restore pool was added. | Cross-Block global restore concurrency limiting is not implemented or claimed; Story 3.4 evidence is limited to same-Block restore coalescing and deadline behavior required by ADR 0027. |
| AC-3.4.4 | Production-profile missing prerequisites fail closed without debug/local fallbacks. | PASS with CONCERNS: `TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable` proves a committed, locally evicted Block with nil restore Backend returns `backend_restore_unavailable` and publishes no local fallback; server/store tests prove public `UNAVAILABLE` ErrorInfo mapping; security/cmd tests prove production TLS, role policy, peer identity, Transit, audit, rate-limit, test-hook, pprof, fake-Transit, routing, and startup gates fail closed. | Deployed production-profile restore evidence was not run; no real S3/IAM/OpenBao production rehearsal is claimed. |

## Planned Verification

Command A:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend|TestReadDocumentRestoreRequiresCommittedConfirmUpload|TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestore.*|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock|TestMissingIndexFailsClosedWithoutAutomaticRestore' -count=1 -v
```

Command B:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/localblock -run 'TestPublishRestoredBlockRecordsLifecycleTransition|TestClassifyLifecycle|TestMalformedMarkersFailClosed' -count=1 -v
```

Command C:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run TestUnavailable -count=1 -v
```

Command D:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEvictionOTelMetrics_RecordApplyAndRestore -count=1 -v
```

Command E:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd -run 'Test.*Production|Test.*Startup|TestLoadConfig|TestValidateStartupGates' -count=1 -v
```

Command F:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/security ./internal/cmd -count=1
```

Command G:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore|Test.*Eviction|Test.*Restore' -count=1 -v
```

Command H:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Command I:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
scan_paths='_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md _bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/shard internal/localblock internal/server internal/store internal/security internal/cmd'
rg -n --pcre2 "$cred_pattern" $scan_paths
```

Command J:

```bash
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
scan_paths='_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md _bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/shard internal/localblock internal/server internal/store internal/security internal/cmd'
rg -n --pcre2 "$identifier_pattern" $scan_paths
```

## Verification Log

| Command | Ran at | Exit | Result | Notes |
| --- | --- | --- | --- | --- |
| Evidence artifact creation | 2026-06-11T22:24:28-04:00 | 0 | PASS | Story 3.4 evidence artifact created before production-code changes. |
| AC-3.4.1 focused Shard authority tests | 2026-06-11T22:29:46-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend\|TestReadDocumentRestoreRequiresCommittedConfirmUpload\|TestReadDocumentRestoreRequiresMatchingEvictionMarker\|TestMetadataReadsStayLocalForEvictedBlock\|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock' -count=1 -v` passed. |
| AC-3.4.2 focused Shard verification tests | 2026-06-11T22:33:43-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable\|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss\|TestReadDocumentRestoreSizeMismatchReturnsDataLoss\|TestReadDocumentRestoreValidationTokenMismatchReturnsDataLoss\|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss\|TestReadDocumentRestoreCorruptHeaderReturnsDataLoss\|TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss\|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss\|TestEncryptedReadDocumentRestoresThenUsesEnvelopePath\|TestReadDocumentCorruptBlockPayloadFailsClosedWithoutReader\|TestReadDocumentCorruptBlockHeaderFailsClosedWithoutReader\|TestHeadAndReadDocumentCorruptIndexFailClosed' -count=1 -v` passed. |
| AC-3.4.2 Local Block Lifecycle publish tests | 2026-06-11T22:33:45-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/localblock -run 'TestPublishRestoredBlockRecordsLifecycleTransition\|TestClassifyLifecycle\|TestMalformedMarkersFailClosed' -count=1 -v` passed. |
| AC-3.4.3 focused restore concurrency tests | 2026-06-11T22:36:11-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentJoinsConcurrentBlockRestore\|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation\|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore\|TestReadDocumentRestoreLeaderDeadlineFailsClosed\|TestReadDocumentRestoreDoesNotBlockMetadataReadsWhileDownloading' -count=1 -v` passed. |
| AC-3.4.4 missing restore Backend test | 2026-06-11T22:38:23-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestReadDocumentRestoreMissingBackendConfigReturnsUnavailable -count=1 -v` passed; result reason was `backend_restore_unavailable` and no local Block was published. |
| AC-3.4.4 public restore error mapping | 2026-06-11T22:38:23-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail -count=1 -v` passed. |
| AC-3.4.4 Store unavailable reason | 2026-06-11T22:38:23-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run TestUnavailable -count=1 -v` passed. |
| AC-3.4.4 production startup/security gates | 2026-06-11T22:38:23-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd -run 'Test.*Production\|Test.*Startup\|TestLoadConfig\|TestValidateStartupGates' -count=1 -v` passed; covered production gate classes, test hooks, pprof, fake Transit, TLS, role policy, peer identity, audit, rate limits, routing, and startup rejection. |
| Final focused Shard restore/read lifecycle tests | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend\|TestReadDocumentRestore.*\|TestReadDocumentJoinsConcurrentBlockRestore\|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation\|TestEncryptedReadDocumentRestoresThenUsesEnvelopePath\|TestMetadataReadsStayLocalForEvictedBlock\|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock\|TestMissingIndexFailsClosedWithoutAutomaticRestore' -count=1 -v` passed. |
| Final Local Block Lifecycle tests | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/localblock -run 'TestPublishRestoredBlockRecordsLifecycleTransition\|TestClassifyLifecycle\|TestMalformedMarkersFailClosed' -count=1 -v` passed. |
| Final public/store restore mapping tests | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail -count=1 -v` and `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run TestUnavailable -count=1 -v` passed. |
| Final restore metric test | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEvictionOTelMetrics_RecordApplyAndRestore -count=1 -v` passed. |
| Package regression gate | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/security ./internal/cmd -count=1` passed. |
| Focused Shard race gate | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore\|Test.*Eviction\|Test.*Restore' -count=1 -v` passed. |
| Full repository check gate | 2026-06-11T22:44:44-04:00 | 0 | PASS | `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after lint cleanup; included lint, `go test ./...`, `go test -race ./...`, integration tests with LocalStack/OpenBao Testcontainers, and `go build` for `cmd/scrapd` and `cmd/scrapctl`. |
| Credential leak scan | 2026-06-11T22:44:44-04:00 | 0 | PASS | Command I matched only allowlisted story/evidence prose, sprint tracker prose, environment variable names, test fixture values, source identifiers, and test PEM type strings; no real secret material, bearer value, AWS key, GitHub token, or private key material was found. |
| Identifier leak scan | 2026-06-11T22:44:44-04:00 | 0 | PASS | Command J matched only allowlisted story/evidence prose, command examples, test fixture Backend keys, `/tmp` test paths, source field names, and tests asserting redaction; no new deployed log, metric, or public error identifier leak was found. |

## Leak Scan Allowlist

- Command I may match only story keys, sprint tracker prose, redaction
  instructions, test fixture identifiers, and security-gate names. No key
  material, password, bearer value, AWS access key, GitHub token, or private key
  is allowed.
- Command J may match only story/spec prose, command examples, sprint tracker
  paths, bounded test fixture identifiers, security-gate names, or source
  comments that explicitly describe redaction requirements. No deployed metric
  label, log field, public error detail, trace field, request metadata, Backend
  object key, validation token, auth claim, certificate material, peer address,
  filesystem path, or raw Document identity may leak through operator-facing or
  public output.

## Evidence Rows

| AC | Claim | Command | Artifact or Test Path | Ref | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| AC-3.4.1 | Cold read restores from committed authority only. | AC-3.4.1 focused Shard authority tests | `internal/shard/restore_test.go`; `internal/shard/read_lifecycle_test.go`; `internal/shard/find_documents_test.go` | Story 3.4 working tree | PASS | Happy path proves no local `.blk`, retained `.idx`, matching eviction marker, one full-object Backend `GetObject` to the committed key, and zero Backend discovery calls. Missing committed ConfirmUpload and stale eviction marker fail closed before Backend GET. Metadata-only reads stay local. This is single-Member fixture evidence, not deployed multi-Member all-copy proof. |
| AC-3.4.2 | Restore verifies before return and publishes no partial bytes on failure. | AC-3.4.2 focused Shard verification tests; AC-3.4.2 Local Block Lifecycle publish tests | `internal/shard/restore_test.go`; `internal/shard/read_verification_test.go`; `internal/localblock/transitions_test.go`; `internal/localblock/lifecycle_test.go` | Story 3.4 working tree | PASS | Restore failure cases return nil reader / zero metadata, leave no published `.blk`, keep the eviction marker, leave no restore marker, and remove staging files. Successful restore verifies the published Block, records backend/read restore marker, removes the eviction marker, classifies hot/serving-allowed, and then serves through the normal local reader. Fake-Transit encrypted restore proves the envelope path is preserved without claiming Story 3.6 production encryption closure. |
| AC-3.4.3 | Concurrent reads coalesce safely with bounded cancellation/timeout/backpressure behavior. | AC-3.4.3 focused restore concurrency tests | `internal/shard/restore.go`; `internal/shard/restore_test.go` | Story 3.4 working tree | PASS | Same-Block readers coalesce to one Backend GET; canceled/deadline waiters return no reader or metadata without canceling the shared restore; leader deadline fails closed; metadata reads continue while Backend download is blocked, proving restore does not hold `Shard.mu` across Backend I/O. Restore copy remains bounded by `restoreCopyBufferSize`; current backpressure scope is per-Block coalescing plus caller deadlines, not a global cross-Block restore limiter. |
| AC-3.4.4 | Production-profile prerequisites fail closed without debug fallback. | AC-3.4.4 missing restore Backend test; AC-3.4.4 public restore error mapping; AC-3.4.4 Store unavailable reason; AC-3.4.4 production startup/security gates | `internal/shard/restore_test.go`; `internal/server/restore_unavailable_test.go`; `internal/store/errors_test.go`; `internal/security/*_test.go`; `internal/cmd/*_test.go` | Story 3.4 working tree | PASS / CONCERNS | Local focused evidence proves nil restore Backend fails closed as `backend_restore_unavailable` with no local/debug fallback; public mapping and production startup/security gates pass. CONCERNS: no deployed production-profile restore, real S3/IAM, or OpenBao rehearsal was run in this story. |

## Pending Evidence

- Focused AC evidence, regression gates, leak scans, and `make check` are
  complete. Deployed production-profile restore evidence remains CONCERNS
  because it was not run in this story.
