---
baseline_commit: e28ec3cb7208c06338f40e36c51903dcd0bd8fef
created: 2026-06-11T22:21:02-04:00
---

# Story 3.4: Restore-First Cold Read Path

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want cold reads to restore and verify the full Block before serving bytes,
so that reads preserve all-or-error behavior after all local `.blk` copies are
evicted.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-8.
- Governing ADRs: ADR 0016, ADR 0019, ADR 0020, and ADR 0027.
- Prerequisites: Stories 3.1, 3.2, and 3.3 are done. Story 3.3 proves policy-gated local eviction, retained `.idx` metadata, and redacted eviction output, but explicitly leaves restore-first cold reads to this story.
- Current implementation intelligence: `internal/shard/restore.go` already owns committed-metadata restore, per-Block restore coalescing, staged download, verification, atomic publish, restore markers, and restore metrics. `internal/shard/shard.go` already routes `ReadDocument` through `ensureReadableBlockLockedForReason`. This story should verify and close gaps, not create a parallel Backend streaming read path.
- Non-goals: detailed restore failure taxonomy closure, retry-budget design, encrypted restore evidence, real OpenBao production proof, final Epic 3 closure, real S3/IAM production rehearsal, and V2 release readiness. Those remain Stories 3.5, 3.6, 3.7, Epic 4, and Epic 6 scope.

## Acceptance Criteria

1. **AC-3.4.1 - Cold read restores from committed authority only.** Given all local `.blk` copies for a confirmed uploaded Block are evicted, when `ReadDocument` is called, then the Shard restores the full Block from Backend to staged local storage before serving bytes. Evidence proves restore is allowed only from committed Confirmed Upload Catalog metadata.
2. **AC-3.4.2 - Restore verifies before return.** Given restore succeeds, when bytes are published, then the restored Block is verified against retained `.idx`, committed metadata, Frame CRCs, and Document SHA-256 before return. Evidence proves no partial or unverified bytes are returned.
3. **AC-3.4.3 - Concurrent reads coalesce safely.** Given restore is in progress, when concurrent reads need the same Block, then they coalesce behind one per-Block restore instead of duplicate Backend downloads. Evidence records bounded concurrency, timeout, cancellation, and backpressure behavior.
4. **AC-3.4.4 - Production-profile prerequisites fail closed.** Given restore-first read runs under production profile, when restore prerequisites or security gates are not satisfied, then the read fails closed instead of using a local/debug fallback. Evidence records the production-profile failure result.

## Tasks / Subtasks

- [ ] Build the Story 3.4 evidence checklist before code changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` with AC rows, authority path, changed-boundary list, exact commands, results, and remaining runtime evidence gaps.
  - [ ] Record the restore authority path: `ReadDocument` -> Pebble Projection / `.idx` resolution -> Local Block Lifecycle `evicted` state -> committed Confirmed Upload Catalog -> Backend `GetObject` -> staged `.blk` -> verification -> atomic local publish -> normal local Block reader.
  - [ ] Record changed boundaries and non-goals: no direct Backend streaming, no Backend list/HEAD authority, no Block/Frame format change, no public/peer/admin proto contract change, no encryption closure, no production release closure.
- [ ] Prove restore-first read from an all-local-cold Block. (AC: 1)
  - [ ] Strengthen or reuse `TestReadDocumentRestoresEvictedBlockFromBackend` so the fixture represents the serving Member with no local `.blk` copy and a retained `.idx` plus eviction marker.
  - [ ] Assert restore reads only the committed `ConfirmedUpload` metadata and matching local eviction marker; stale or missing committed authority must fail closed.
  - [ ] Assert restore does not use Backend `ListObjects`, Backend inventory, object discovery, local file presence, hostname, peer address, or Local Block Lifecycle marker alone as authority.
  - [ ] If current tests only cover a single-Member simulation, say so in the evidence artifact and do not overclaim multi-Member deployed all-copy evidence.
- [ ] Prove restored bytes are verified before publication and return. (AC: 2)
  - [ ] Use or strengthen existing restore tests for size mismatch, validation-token mismatch, Block header corruption, Frame CRC corruption, corrupt Document SHA-256, and missing Backend object.
  - [ ] Assert failed restore leaves no published `.blk`, leaves the eviction marker in place, removes staging files, and returns no reader / no partial bytes.
  - [ ] Assert successful restore writes a restore marker with source `backend` and reason `read`, removes the eviction marker, classifies the Block as hot/serving-allowed, and then serves through the normal local Block reader.
  - [ ] Preserve encryption hooks: encrypted Blocks still read through the existing envelope path after restore; do not add a direct ciphertext streaming path.
- [ ] Prove per-Block coalescing, timeout, cancellation, and backpressure behavior. (AC: 3)
  - [ ] Strengthen `TestReadDocumentJoinsConcurrentBlockRestore` to prove concurrent reads for the same Block make one Backend `GetObject` call and all waiters receive verified bytes.
  - [ ] Strengthen `TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation` to prove the first waiting client can cancel without canceling a restore that later satisfies another reader.
  - [ ] Add timeout/deadline evidence for a waiter and for the restore leader if current coverage does not prove both cases.
  - [ ] Review restore concurrency/backpressure configuration. If current behavior is only per-Block coalescing with caller deadlines, document that scope explicitly; add minimal bounded restore settings only if required to satisfy AC-3.4.3 without broadening package boundaries.
  - [ ] Ensure restore never holds `Shard.mu` while downloading from Backend and never buffers full Blocks or Documents in memory beyond existing bounded copy/read contracts.
- [ ] Prove production-profile fail-closed behavior. (AC: 4)
  - [ ] Cover missing Backend restore configuration as `UNAVAILABLE` with reason `backend_restore_unavailable`; no local/debug Backend fallback is allowed.
  - [ ] Cover production security gate failure through existing `internal/security` / `internal/cmd` startup-gate tests or add a focused test if the restore story touches production-mode config.
  - [ ] Verify `SCRAP_TEST_HOOKS`, pprof, fake Transit, missing TLS, missing role policy, missing peer identity policy, and missing audit/rate-limit policy remain startup-gate failures in production mode; do not claim production readiness from development-mode fixtures.
  - [ ] If deployed production-profile restore evidence is not run, mark it CONCERNS in the evidence artifact rather than PASS.
- [ ] Preserve package and architecture boundaries. (AC: 1-4)
  - [ ] Keep restore orchestration in `internal/shard`, lifecycle markers/transitions in `internal/localblock`, Backend object access in `internal/backend`, public error mapping in `internal/server` / `internal/store`, and operator evidence outside the public read path.
  - [ ] Do not create `internal/coldread`, `common`, `shared`, a second read implementation, a new Backend wrapper, new assertion libraries, or a new mocking framework unless an ADR-level decision changes the package map.
  - [ ] Do not change Block/Frame layout, Backend object key format, public/peer/admin proto contracts, Pebble key prefixes, Confirmed Upload Catalog schema, storage identity, or production security policy.
- [ ] Run focused and regression verification. (AC: 1-4)
  - [ ] Run focused Shard restore/read lifecycle tests.
  - [ ] Run focused public error mapping, Local Block Lifecycle, and restore metric tests.
  - [ ] Run a focused Shard race gate for restore singleflight / lifecycle mutation behavior.
  - [ ] Run `make check` before BMAD code-review handoff unless a narrower blocker is documented in the story.

## Dev Notes

### Current State

- `CONTEXT.md` defines Confirmed Upload Catalog as the committed restore authority for sealed Blocks and Local Block Lifecycle as per-Member filesystem evidence only.
- PRD FR-8 requires restore-first cold reads: when all local `.blk` copies are evicted, `ReadDocument` restores the full Block from the Backend, verifies it, and serves through the normal local path.
- ADR 0027 rejects direct Backend ciphertext streaming, range streaming, and per-Frame remote reads for V2. Do not add a second read path.
- ADR 0027 requires restore from committed Confirmed Upload Catalog metadata only, staged local restore, retained `.idx` verification, Block header verification, Frame CRC verification, Document SHA-256 verification, atomic publish, restore marker evidence, per-Block singleflight, and no restore for metadata-only reads.
- ADR 0016 already implemented Phase 4 restore-before-eviction mechanics. This story promotes that path into FR-8 cold-read evidence and hardens any missing production/concurrency proof.
- ADR 0019 says Phase 5 starts after explicit production security gates. Production-mode evidence must not use development security fallbacks.
- ADR 0020 says encrypted Backend-resident Blocks remain ciphertext; restore downloads ciphertext and the normal read path decrypts and verifies plaintext. Story 3.6 owns full encryption-compatible restore evidence.
- Story 3.3 moved local eviction to done and deliberately left all-local-copy eviction, restore-first cold reads, restore failure taxonomy, encryption-compatible restore, and final Epic 3 closure outside its scope.

### Existing Implementation To Reuse

- `internal/shard/shard.go`: `ReadDocument` calls `readDocumentFromProjection`, which resolves the Document, calls `ensureReadableBlockLockedForReason`, and then reads from the local Block path.
- `internal/shard/restore.go`: owns `ensureReadableBlockLockedForReason`, `restoreEvictedBlockForReason`, `beginRestore`, `waitRestore`, `restoreInput`, `downloadVerifyAndPublishRestore`, `verifyRestoredBlock`, `publishVerifiedRestore`, `downloadRestoreObject`, and Backend error mapping.
- `internal/shard/restore.go` already releases `Shard.mu` before Backend download, uses a per-Block in-memory call map to coalesce concurrent restores, downloads through a staging file, verifies before publish, and records restore outcomes.
- `internal/localblock/transitions.go`: `PublishRestoredBlock` atomically renames the staged `.blk`, syncs the directory, writes the restore marker, and removes the eviction marker.
- `internal/localblock/lifecycle.go`: classifies `hot`, `evicted`, `hot_cleanup_needed`, `metadata_loss`, and `unexpected_loss`; metadata loss and unexpected loss fail closed.
- `internal/shard/read_lifecycle.go`: keeps `HeadDocument` and `FindDocuments` local and ensures evicted metadata has committed restore authority without restoring `.blk`.
- `internal/shard/restore_test.go`: already covers happy-path restore, transient Backend unavailable, missing Backend object, corrupt Backend object, corrupt header, corrupt Frame header, corrupt Document SHA-256, missing committed ConfirmUpload, concurrent restore coalescing, first-reader cancellation, and repair restore.
- `internal/shard/read_lifecycle_test.go` and `internal/shard/find_documents_test.go`: already prove metadata-only reads do not call Backend for evicted confirmed Blocks.
- `internal/server/restore_unavailable_test.go` and `internal/store/errors.go`: already cover public gRPC `UNAVAILABLE` ErrorInfo reason for Backend restore unavailability.
- `internal/shard/eviction_metrics_otel_test.go`: already covers restore metric attributes and stale restore-failure gauge cleanup.
- `internal/security/startup_gate.go`, `internal/security/mode_test.go`, and `internal/cmd/config_test.go`: own production-mode security gate validation. Reuse them for AC-3.4.4 rather than adding restore-specific security policy in Shard.

### Implementation Guidance

- Start with tests and evidence. If existing tests already close an AC, record the exact command and path in the evidence artifact instead of rewriting production code.
- Treat `ReadDocument` as the only public path that may restore `.blk` bytes. `HeadDocument` and `FindDocuments` must continue to use retained `.idx` metadata only.
- Restore eligibility starts from committed Confirmed Upload Catalog metadata and a matching local eviction marker. Backend list/HEAD, object existence, object key shape, local marker presence, or local file absence alone is not authority.
- Restore must stage bytes under the Shard `blocksDir`, validate Backend metadata, copy with bounded buffers, sync the staging file, verify against retained `.idx`, and publish atomically. Failed restore must remove staging files and keep the eviction marker.
- After successful restore, ordinary `ReadDocument` should use the same local Block reader and encryption/decryption path as hot reads. Do not special-case restored Documents in public server code.
- Do not import `golang.org/x/sync/singleflight` only for convenience. The module is present indirectly in `tools.go.mod`, not as a runtime dependency. The existing per-Block restore call map is the local pattern unless tests prove it is inadequate and a dependency decision is made.
- Do not add direct Backend streaming, per-Document range GET, per-Frame remote reads, CDN-style caching, or stale-while-refresh behavior. These conflict with ADR 0027's V2 boundary.
- Keep public errors bounded. Backend object keys, validation tokens, raw `transaction_id`, raw `document_name`, request IDs, trace IDs, filesystem paths, peer addresses, auth claims, gRPC metadata, certificates, Transit policy detail, and dependency error strings must not leak into deployed logs, metrics, public errors, or evidence.
- Story 3.5 owns exhaustive restore failure taxonomy and retry-budget evidence. This story may reuse existing failure tests to prove no partial bytes, but do not claim Story 3.5 closure.
- Story 3.6 owns encryption-compatible restore evidence. This story should preserve the existing encryption path and may reference encryption tests, but do not claim production OpenBao proof.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md` - story status, debug log, completion notes, and file list during dev.
- `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` - Story 3.4 evidence table and verification log.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - story status transitions.
- `internal/shard/restore.go` and `internal/shard/restore_test.go` - likely focused restore-first read evidence and any missing hardening.
- `internal/shard/shard.go` - only if the read path needs a narrow change; avoid public server changes for restore mechanics.
- `internal/shard/read_lifecycle.go`, `internal/shard/read_lifecycle_test.go`, and `internal/shard/find_documents_test.go` - metadata-only non-restore guardrails.
- `internal/localblock/transitions.go`, `internal/localblock/transitions_test.go`, `internal/localblock/lifecycle.go`, and `internal/localblock/lifecycle_test.go` - only if marker publish/classification gaps are found.
- `internal/server/restore_unavailable_test.go`, `internal/store/errors.go`, and `internal/store/errors_test.go` - public error mapping evidence if needed.
- `internal/shard/eviction_metrics_otel_test.go` - restore metrics/redaction evidence if needed.
- `internal/security/*_test.go` and `internal/cmd/*_test.go` - production-profile fail-closed evidence if needed.

Likely avoid:

- `proto/`, `gen/`, Block/Frame layout code, Backend object key construction, public `DocumentService` wire contracts, peer `PeerService`, routing, placement, production security policy, OpenBao policy, and release evidence docs.
- `internal/backend/*` unless a Backend fake or error-classification defect directly blocks restore verification.
- New dependencies, assertion libraries, mocking frameworks, or a new `internal/coldread` package.

### Testing Requirements

Run focused gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend|TestReadDocumentRestoreRequiresCommittedConfirmUpload|TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestore.*|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock|TestMissingIndexFailsClosedWithoutAutomaticRestore' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/localblock -run 'TestPublishRestoredBlockRecordsLifecycleTransition|TestClassifyLifecycle|TestMalformedMarkersFailClosed' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store -run TestUnavailable -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestEvictionOTelMetrics_RecordApplyAndRestore -count=1 -v
```

Run security/config focused gates for AC-3.4.4 if production-profile evidence is touched:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd -run 'Test.*Production|Test.*Startup|TestLoadConfig|TestValidateStartupGates' -count=1 -v
```

Run regression gates before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/security ./internal/cmd -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'TestReadDocument.*Restore|Test.*Eviction|Test.*Restore' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Suggested leak scans. Store patterns in shell variables so the story file does not self-match credential-shaped words when copied into evidence:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md _bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md internal/shard internal/localblock internal/server internal/store internal/security internal/cmd
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md _bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md internal/shard internal/localblock internal/server internal/store internal/security internal/cmd
```

Runtime evidence is not required to create the story. If deployed restore evidence is claimed during dev, record the exact target, environment, commit, and pass/fail result. If not run, mark it CONCERNS rather than PASS.

### Previous Story Intelligence

- Story 3.3 closed policy-gated local eviction and retained metadata. It explicitly did not close restore-first cold reads, all-local-copy eviction, restore failure taxonomy, encryption-compatible restore, production security closure, real S3/IAM rehearsal, or final V2 release readiness.
- Story 3.3 review found that dry-run evidence must prove Document visibility and that redaction must cover apply/status/HTTP error output, not only plan output. Apply that lesson to restore-first evidence: prove the public `ReadDocument` surface, not just private restore helpers, and scan public errors/metrics/logs for sensitive fields.
- Story 3.3 added `internal/eviction/redaction.go` for operator-facing eviction output. Do not route public `ReadDocument` errors through operator redaction if the correct fix is bounded Store/server error mapping.
- Story 3.3 evidence style: AC rows, exact commands, current commit/ref, changed-boundary list, verification log, leak-scan allowlist, and explicit CONCERNS for runtime evidence not actually run.
- Story 3.2 review fixes matter here: rejected/failed operations must leave no accepted partial state, metrics must use bounded attributes, and cleanup assertions must target only files this workflow owns.
- Story 3.1 established the authority model: Backend success without committed `ConfirmUpload` is not committed upload authority. Restore-first reads must preserve that rule.

### Latest Technical Information

- GitHub repo search for `restore first cold read Go object storage singleflight` returned no reusable implementation candidates.
- GitHub code search for `"restore-first" "cold read" language:Go` returned no reusable implementation candidates.
- Exa research found AWS Storage Gateway archived-object restore patterns where applications may time out while restore completes. Use that only as a general timeout/cancellation caution; do not import AWS File Gateway semantics into S.C.R.A.P.
- Exa research found recent object-storage/cache read designs and singleflight prior art. The relevant reusable pattern is deduplicating concurrent fetches per key; this repo already implements that with `restoreMu` and per-Block `blockRestoreCall`.
- `golang.org/x/sync v0.20.0` is present only indirectly in `tools.go.mod`; current runtime module versions relevant to this story are `github.com/cockroachdb/pebble v1.1.5`, `go.etcd.io/raft/v3 v3.6.0`, `google.golang.org/grpc v1.81.1`, and `go.opentelemetry.io/otel v1.44.0`.

### References

- `CONTEXT.md` - Confirmed Upload Catalog, Local Block Lifecycle, Backend, Document, Block, Frame, Shard, Cell, and Member glossary.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.4 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-8.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-3, FR-8, boundary map, and evidence requirements.
- `_bmad-output/project-context.md` - package boundaries, testing rules, telemetry/redaction rules, and commit rules.
- `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md` - committed upload authority guardrails.
- `_bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md` - upload pressure and no partial accepted state lessons.
- `_bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md` - previous story intelligence and review findings.
- `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` - previous evidence style and open non-goals.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - restore-before-eviction mechanics and failure rules.
- `docs/adr/0019-production-security-boundary.md` - Phase 5 production security entry and fail-closed gates.
- `docs/adr/0020-openbao-envelope-encryption-contract.md` - encrypted Backend-resident Block contract and direct streaming deferral.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - governing cold-read decision.
- `docs/phase-4-eviction-implementation-slices.md` - Phase 4 restore/read validation slice notes.
- `internal/shard/shard.go`
- `internal/shard/restore.go`
- `internal/shard/restore_test.go`
- `internal/shard/read_lifecycle.go`
- `internal/shard/read_lifecycle_test.go`
- `internal/shard/find_documents_test.go`
- `internal/localblock/lifecycle.go`
- `internal/localblock/transitions.go`
- `internal/server/restore_unavailable_test.go`
- `internal/store/errors.go`
- `internal/security/startup_gate.go`
- `internal/cmd/config.go`
- `https://aws.amazon.com/blogs/storage/automate-restore-of-archived-objects-through-aws-storage-gateway/`
- `https://github.com/golang/sync/blob/master/singleflight/singleflight.go`

## Dev Agent Record

### Agent Model Used

TBD by dev-story.

### Debug Log References

- CREATE-STORY: `git status --short --branch` confirmed clean `v2...origin/v2` after pushing Story 3.3 review fix commit `e28ec3cb7208c06338f40e36c51903dcd0bd8fef`.
- CREATE-STORY: Loaded `CONTEXT.md`, `_bmad-output/project-context.md`, sprint status, Epic 3, FR-8, architecture DG-3/FR-8, ADR 0016, ADR 0019, ADR 0020, ADR 0027, Story 3.3, and current restore/read/lifecycle/security code.
- CREATE-STORY: GitHub repo and code searches found no reusable restore-first cold-read implementation candidate.
- CREATE-STORY: Exa research was used only for timeout/singleflight prior-art context; repo-local restore architecture remains authoritative.
- CREATE-STORY: Current baseline commit is `e28ec3cb7208c06338f40e36c51903dcd0bd8fef`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Scoped Story 3.4 to restore-first `ReadDocument` evidence and hardening, with detailed failure taxonomy, encryption-compatible restore, final Epic 3 closure, and production release evidence left to later stories.
- Identified existing implementation to reuse: `internal/shard/restore.go`, `internal/shard/shard.go`, `internal/localblock`, Store/server error mapping, restore metrics, and production startup gates.
- Flagged likely evidence gaps around all-local-copy wording in a single-Member fixture, explicit timeout/deadline proof, restore backpressure scope, and production-profile fail-closed proof.

### File List

- `_bmad-output/implementation-artifacts/3-4-restore-first-cold-read-path.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Change Log

- 2026-06-11: Created Story 3.4 Restore-First Cold Read Path context and moved status to ready-for-dev.
