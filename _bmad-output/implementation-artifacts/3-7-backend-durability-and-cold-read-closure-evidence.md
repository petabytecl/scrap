---
baseline_commit: 688b2095bc9554549f212d0e6ed7c52e00d76fa6
created: 2026-06-12T00:08:42-04:00
---

# Story 3.7: Backend Durability and Cold-Read Closure Evidence

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want Backend upload, eviction, restore, and failure evidence linked,
so that Epic 3 cannot close from a happy-path restore demo.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-6, FR-7, FR-8.
- Governing ADRs: ADR 0009, ADR 0010, ADR 0016, ADR 0017, ADR 0018, ADR 0020, ADR 0021, and ADR 0027.
- Prerequisites: Stories 3.1 through 3.6 are done and have dedicated evidence artifacts.
- Current evidence inventory:
  - Story 3.1: `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` is `done`. It records committed upload confirmation, ACK independence, split Backend success without committed confirmation, redaction, and no public Backend inventory authority. It records CONCERNS for a checked-but-skipped E2E target without `SCRAP_E2E=1` and defers real S3/IAM to Story 6.6.
  - Story 3.2: `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md` is `done`. It records upload pressure, safe rejection/recovery, bounded telemetry, no partial accepted write state, and redaction. It records CONCERNS for the checked-but-skipped E2E target without `SCRAP_E2E=1` and defers real S3/IAM to Story 6.6.
  - Story 3.3: `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` is `complete`. It records policy-gated eviction dry-run/apply, retained `.idx`, metadata-only reads while evicted, Local Block Lifecycle boundaries, fail-closed output, and redaction. It does not claim deployed runtime eviction, real S3/IAM, all-local-copy eviction, or release readiness.
  - Story 3.4: `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md` is `complete-with-concerns`. It records restore from committed Confirmed Upload Catalog metadata, retained `.idx` and Block/Frame/Document verification, no partial publish, same-Block restore singleflight, cancellation/deadline behavior, metadata-only local reads, and missing-restore-Backend fail-closed behavior. It explicitly scopes all-local-copy proof to a single-Member fixture and does not claim deployed multi-Member or production-profile restore evidence.
  - Story 3.5: `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md` is `complete`. It records typed restore outcomes for transient Backend failure, missing/corrupt Backend objects, checksum/metadata mismatch, retry exhaustion, cancellation/deadline behavior, no partial bytes, public status sanitization, and redaction.
  - Story 3.6: `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md` is `done`. It records fixture-backed encrypted restore, Backend/restored Block plaintext absence, full-object committed Backend GET with zero HEAD/LIST restore discovery, key-service and key-version fail-closed cases, rewrapped envelope restore, public crypto-unavailable sanitization, and redaction. It does not claim production OpenBao proof, real S3/IAM, direct Backend streaming, or Epic 3 closure.
- Current implementation intelligence: `internal/scrapctl/evidencebundle` already has a Tier 3/security-oriented `Gate` and `phase5_gate_recorded` check, but it is not an Epic 3 Backend durability closure model. Story 3.7 must not silently reuse that field as Epic 3 closure unless it introduces explicit names and tests for the Epic 3 evidence model.
- Non-goals: new Backend provider behavior, real S3/IAM production rehearsal, production OpenBao policy/token proof, direct Backend ciphertext streaming, range streaming, per-Frame remote reads, public/peer/admin proto changes, Block/Frame layout changes, Backend object key changes, storage identity changes, and final V2 release readiness.

## Acceptance Criteria

1. **AC-3.7.1 - Evidence inventory is linked to owners.** Given Epic 3 evidence is collected, when closure is evaluated, then upload confirmation, pressure, eviction, all-local-copy restore, concurrent restore, failure mapping, encryption interaction, cancellation, and redaction evidence are linked. Evidence records the artifact paths and owning stories.
2. **AC-3.7.2 - Backend inventory is not authority.** Given Backend inventory, list, or HEAD output exists, when evidence is reviewed, then no hot read/write path treats it as authority. Evidence proves Backend access follows committed metadata and explicit restore verification.
3. **AC-3.7.3 - P0 cold-read closure uses gate language.** Given Epic 3 closure is evaluated, when any P0 cold-read evidence is missing, then closure is FAIL, not deferred to Epic 6. Evidence records PASS, CONCERNS, or FAIL using V2 release gate language.

## Tasks / Subtasks

- [x] Create the Epic 3 closure artifact before behavior changes. (AC: 1-3)
  - [x] Create `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`.
  - [x] Record a closure decision using exactly one of `PASS`, `CONCERNS`, or `FAIL`.
  - [x] State that the story can complete with a `FAIL` closure decision if all evidence is linked and the missing P0 evidence is explicit. Do not mark Epic 3 itself as done from a failed closure result.
  - [x] Include the baseline commit, evaluation timestamp, story status inputs, and exact files reviewed.
- [x] Build the evidence inventory matrix. (AC: 1)
  - [x] For each required evidence item, record owning story, artifact path, current artifact status, proof command or test name, result, and concern/gap.
  - [x] Required rows: upload confirmation, upload pressure, policy-gated eviction, all-local-copy restore, concurrent restore/singleflight, restore failure mapping, encryption interaction, cancellation/deadline cleanup, redaction/leak scans, and no Backend inventory authority.
  - [x] Link all Story 3.1 through 3.6 evidence artifacts and do not summarize a PASS without naming the proof that supports it.
  - [x] Separate local/package proof, Tier 2/E2E proof, production OpenBao proof, and real S3/IAM proof. Do not blur these evidence classes.
- [x] Define and apply the P0 closure matrix. (AC: 3)
  - [x] Mark P0 cold-read items explicitly. At minimum: all-local-copy restore, restore-on-read from committed Confirmed Upload Catalog metadata, full-Block verification before return, concurrent same-Block restore singleflight, transient Backend failure, missing/corrupt Backend failure, cancellation/deadline behavior, encryption-compatible restore, and redaction/no raw identifier or Backend key leaks.
  - [x] If any P0 row lacks current evidence, set the closure decision to `FAIL` and name the owning missing evidence. Do not downgrade a P0 miss to `CONCERNS`.
  - [x] Use `CONCERNS` only for non-P0 limitations or scoped proof that is acceptable for Epic 3 but not final V2 release, such as skipped deployed E2E targets, real S3/IAM rehearsal, or production OpenBao proof.
  - [x] Use `PASS` only if every Epic 3 P0 row has current evidence and all remaining concerns are either not Epic 3 scope or explicitly accepted by the source documents.
- [x] Prove Backend inventory and discovery boundaries. (AC: 2)
  - [x] Reuse existing tests where possible: `TestReadDocumentRestoresEvictedBlockFromBackend`, `TestReadDocumentRestoreRequiresCommittedConfirmUpload`, `TestReadDocumentRestoreRequiresMatchingEvictionMarker`, `TestMetadataReadsStayLocalForEvictedBlock`, `TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock`, and `TestEncryptedReadDocumentRestoresThenUsesEnvelopePath`.
  - [x] Record that restore uses one full-object `GetObject` to the committed Backend key and does not use Backend HEAD/LIST/listing/inventory as restore authority.
  - [x] Record that Backend PUT/HEAD/list/object existence evidence may verify upload/diagnostics but does not decide public read/write routing, Document visibility, durable upload authority, Shard membership, or Local Block Lifecycle state.
  - [x] Run a focused source scan for Backend discovery terms across hot read/write paths and classify any matches as allowed provider/evidence/test code or a closure blocker.
- [x] Update executable closure support only if needed. (AC: 3)
  - [x] Inspect `internal/scrapctl/evidencebundle` before changing it. Current `Gate` fields are Tier 3/security gate fields, not an Epic 3 closure schema.
  - [x] If the closure artifact is intentionally document-only, record that decision in the story debug log and do not change code.
  - [x] If an executable Epic 3 closure helper is added, keep it separate from the existing security gate naming, add tests for missing P0 => FAIL, scoped non-P0 concerns => CONCERNS, and complete P0 evidence => PASS, and avoid changing the existing Tier 3/security `Gate` behavior unless tests prove the shared contract should change.
- [x] Preserve architecture, package, and evidence boundaries. (AC: 1-3)
  - [x] Keep restore orchestration in `internal/shard`, local lifecycle in `internal/localblock`, Backend object operations behind `internal/backend`, evidence bundle logic in `internal/scrapctl/evidencebundle`, and public status mapping in `internal/server`.
  - [x] Do not add direct Backend streaming, range reads, per-Frame remote reads, Backend inventory authority, or local-file authority.
  - [x] Do not introduce new runtime dependencies, assertion libraries, package-level globals, new production background workers, new public status details, or new telemetry labels for this closure story.
  - [x] Do not claim production OpenBao proof or real S3/IAM proof. Those remain Epic 4 and Story 6.6 unless another accepted story explicitly changes scope.
- [x] Run focused verification and record exact results. (AC: 1-3)
  - [x] Run focused Story 3.7 closure checks, including artifact validation, authority scans, and any evidencebundle tests if code is touched.
  - [x] Run the P0 evidence test suite listed in Testing Requirements or record any skipped command with an explicit closure impact.
  - [x] Run credential and identifier leak scans over the new closure artifact, this story, and any touched code.
  - [x] Run `git diff --check` and the narrowest relevant Go package tests. Run `make check` before review if production code, executable evidence-gate code, or broad closure claims changed.

## Dev Notes

### Current State

- `CONTEXT.md` defines Backend as opaque cold durability, not S3-compatible public behavior. Documents are immutable after ACK and addressed by `(transaction_id, document_name)`.
- PRD FR-6 requires asynchronous sealed Block upload through committed metadata. Backend upload is not in the ACK path, and Backend inventory/HEAD/list output is not a consistency oracle.
- PRD FR-7 requires policy-gated local Block eviction and full-Block restore before reads that need evicted bytes. Missing or corrupt confirmed Backend objects fail closed.
- PRD FR-8 and ADR 0027 require restore-first cold reads: if local `.blk` copies are evicted, `ReadDocument` restores the full Block, verifies it, publishes it locally, and then serves through the normal local Block reader. Direct Backend streaming, range streaming, and per-Frame remote reads are out of V2.
- ADR 0027 requires evidence for all-local-copy eviction, restore-on-read, concurrent read singleflight, Backend transient failure, Backend missing/corrupt failure, encryption interaction, and no raw identifier or Backend key leaks.
- Story 3.4 currently records `complete-with-concerns`, including single-Member all-local-copy proof only. Treat that scoped concern honestly when applying the P0 matrix.
- Story 3.6 closed fixture-backed encryption-compatible restore. Production OpenBao deployment, policy, token custody, and production rehearsal remain Epic 4 release evidence and must not be claimed by this story.
- Existing `test/e2e/security_evidence_e2e_test.go` can record encrypted restore concerns and points some restore evidence back to Epic 3 Stories 3.4 and 3.7. Do not mistake that concern path for Epic 3 closure.

### Existing Implementation To Reuse

- `internal/shard/restore_test.go`: restore authority, verification, same-Block concurrency, cancellation, fail-closed, and encryption-compatible restore tests. Key tests are listed in Tasks and Testing Requirements.
- `internal/shard/restore.go`: full-Block restore through committed Confirmed Upload Catalog metadata, staging, verification, atomic publish, and normal local read. Read before changing any restore behavior.
- `internal/shard/upload_outbox_test.go`, `internal/shard/upload_controller_boundary_test.go`, and `internal/index/confirmed_upload_catalog_test.go`: upload confirmation, split-success, and catalog authority evidence.
- `internal/shard/upload_pressure_test.go`, `internal/shard/upload_obligations_test.go`, and `internal/shard/upload_metrics_otel_test.go`: upload pressure, safe admission, and bounded telemetry evidence.
- `internal/shard/eviction_apply_test.go`, `internal/shard/read_lifecycle_test.go`, `internal/shard/find_documents_test.go`, `internal/localblock/transitions_test.go`, and `internal/localblock/lifecycle_test.go`: eviction, metadata-only read, and lifecycle proof.
- `internal/server/restore_unavailable_test.go`, `internal/server/server.go`, and `internal/store/errors.go`: public restore error mapping and sanitization.
- `internal/scrapctl/evidencebundle/types.go`, `internal/scrapctl/evidencebundle/gate.go`, `internal/scrapctl/evidencebundle/bundle.go`, and their tests: evidence bundle support. Current gate naming is security/Tier 3 oriented; add Epic 3 closure naming only if implementation needs executable closure support.
- `_bmad-output/implementation-artifacts/epic-3-*.md`: source evidence artifacts to link; do not rewrite prior story facts unless Story 3.7 discovers a concrete correction.

### Implementation Guidance

- Start with the closure artifact, not code. The highest-risk failure mode is declaring closure from intent or from a happy-path restore test.
- Treat evidence result semantics strictly:
  - `PASS`: current evidence satisfies the row at the required scope.
  - `CONCERNS`: evidence exists but scope is limited, non-P0, or explicitly deferred by source documents.
  - `FAIL`: P0 evidence is absent, stale, contradictory, or not attributable.
- If current evidence says a command skipped because `SCRAP_E2E=1` was not set, do not convert that to PASS. Decide whether it is non-P0 CONCERNS or P0 FAIL and explain why.
- If current evidence is single-Member only, do not call it deployed multi-Member evidence. Decide whether the P0 row accepts that scope for Epic 3 or fails closure.
- Keep real S3/IAM and production OpenBao separate:
  - Real S3/IAM production rehearsal is Story 6.6 / Epic 6 release evidence.
  - Production OpenBao proof is Epic 4 release evidence.
  - Neither can excuse a missing Epic 3 P0 cold-read proof.
- Prefer linking current artifacts and rerunning focused tests over adding new implementation. Add code only when a test or closure schema gap blocks AC-3.7.2 or AC-3.7.3.
- The closure artifact must be self-contained enough for a reviewer to see what evidence closes Epic 3 and what remains outside Epic 3.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-7-backend-durability-and-cold-read-closure-evidence.md` - story status, debug log, completion notes, file list, and review findings during dev.
- `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md` - closure decision, inventory matrix, P0 matrix, authority proof, verification log, leak scan allowlist, and remaining scope.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.
- `internal/scrapctl/evidencebundle/*` - only if Story 3.7 adds executable Epic 3 closure gate support.

Likely avoid:

- `proto/`, `gen/`, Block/Frame layout code, Backend object key construction, public/peer/admin wire contracts, Pebble key prefixes, routing/placement, production security policy, OpenBao policy/bootstrap code, real S3/IAM deployment evidence, and release closure docs.
- `internal/backend/*` unless a provider-neutral evidence bug blocks the no-inventory-authority proof.
- `internal/shard/restore.go` unless a focused test proves the existing restore authority or verification behavior is wrong.

### Testing Requirements

Run focused P0 evidence gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoresEvictedBlockFromBackend|TestReadDocumentRestoreRequiresCommittedConfirmUpload|TestReadDocumentRestoreRequiresMatchingEvictionMarker|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentJoinsConcurrentBlockRestore|TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation|TestReadDocumentRestoreWaiterDeadlineDoesNotCancelSharedRestore|TestReadDocumentRestoreLeaderDeadlineFailsClosed|TestReadDocumentRestoreDoesNotBlockMetadataReadsWhileDownloading' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestReadDocumentRestoreBackendTransientReturnsUnavailable|TestReadDocumentRestoreRetriesTransientBackendFailures|TestReadDocumentRestoreRetryBudgetExhaustedFailsClosed|TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss|TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestEncryptedReadDocumentRestoresThenUsesEnvelopePath|TestReadDocumentEncryptedRestoreFailsClosedWhenKeyMaterialUnavailable|TestReadDocumentEncryptedRestoreFailsClosedWhenKeyVersionRejected|TestReadDocumentEncryptedRestoreUsesRewrappedEnvelope' -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/localblock ./internal/server ./internal/store ./internal/eviction ./internal/backend ./internal/encryption ./internal/scrapctl/evidencebundle -count=1
```

If `internal/scrapctl/evidencebundle` changes, add and run focused gate tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle -run 'Test.*Epic3|Test.*Closure|TestEvaluateGate|TestGenerate' -count=1 -v
```

Run authority and leak scans. Keep patterns in shell variables so evidence files do not self-match credential-shaped terms copied into prose:

```bash
rg -n 'HeadObject|ListObjects|ListObject|inventory|Backend inventory|GetObject|PutObject|ConfirmUpload|Confirmed Upload Catalog' internal/shard internal/server internal/store internal/backend internal/localblock internal/eviction internal/scrapctl/evidencebundle test/e2e

cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-7-backend-durability-and-cold-read-closure-evidence.md _bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md internal/scrapctl/evidencebundle internal/shard internal/server internal/store internal/eviction internal/backend internal/localblock internal/encryption
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-7-backend-durability-and-cold-read-closure-evidence.md _bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md internal/scrapctl/evidencebundle internal/shard internal/server internal/store internal/eviction internal/backend internal/localblock internal/encryption
```

Run broad gates before review if code changed or closure claims changed broadly:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If a command is not run, record it as skipped with a reason and closure impact. Do not mark an AC or P0 row as pass from intent alone.

### Previous Story Intelligence

- Story 3.6 review found marker-level plaintext checks were necessary; closure redaction should cite both full payload and stable marker checks where relevant.
- Story 3.6 review also found public unavailable details need allowlisting. Story 3.7 should cite the allowlisted public reasons rather than raw dependency messages.
- Story 3.5 review found public data-loss messages can leak internal details if Store errors pass through directly. Closure evidence must name sanitized public tests, not just Shard errors.
- Story 3.4 scoped all-local-copy proof to a single-Member fixture and did not claim deployed multi-Member evidence. Story 3.7 must preserve or fail that scope honestly.
- Story 3.3 review emphasized redaction proof must cover operator-facing output, not only internal helper errors.
- Story 3.1 and Story 3.2 both checked E2E targets without `SCRAP_E2E=1`. Story 3.7 must classify those as CONCERNS or FAIL, never PASS.

### Latest Technical Information

- No external library, API, provider version, or package-registry research is required for this story. Story 3.7 is a local evidence and closure-gate story that must reuse repo evidence, accepted ADRs, and existing Go tests.
- Do not add runtime or test dependencies for closure classification. If executable support is needed, implement it with existing standard-library patterns in `internal/scrapctl/evidencebundle`.

### References

- `CONTEXT.md` - Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Upload Outbox, Confirmed Upload Catalog, Local Block Lifecycle, and OpenBao Transit glossary.
- `_bmad-output/project-context.md` - package boundaries, testing rules, closure rules, telemetry/redaction rules, and commit rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.7 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-6, FR-7, FR-8, DG-3, and acceptance/evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-3 cold-read decision, package ownership, and known readiness gaps.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - restore-first decision, authority model, error mapping, encryption interaction, and evidence requirements.
- `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md`
- `internal/shard/restore.go`
- `internal/shard/restore_test.go`
- `internal/scrapctl/evidencebundle/types.go`
- `internal/scrapctl/evidencebundle/gate.go`
- `internal/scrapctl/evidencebundle/bundle.go`

## Dev Agent Record

### Agent Model Used

GPT-5 Codex.

### Debug Log References

- CREATE-STORY: Resumed from clean `v2...origin/v2` after Story 3.6 code-review fixes were committed and pushed as `688b2095bc9554549f212d0e6ed7c52e00d76fa6`.
- CREATE-STORY: Loaded BMAD create-story workflow, customization block, `CONTEXT.md`, `_bmad-output/project-context.md`, sprint status, Epic 3, Story 3.7 ACs, FR-6, FR-7, FR-8, DG-3, ADR 0027, Story 3.6, current evidence artifacts, `internal/scrapctl/evidencebundle`, and recent git history.
- CREATE-STORY: No external research was needed because Story 3.7 must reuse local evidence and accepted ADRs and should not add dependencies.
- CREATE-STORY: Current baseline commit is `688b2095bc9554549f212d0e6ed7c52e00d76fa6`.
- DEV-STORY: Started implementation from clean `v2...origin/v2` after pushing story creation commit `b7aab530638d96cc5cf23903dfcc4deece8395b9`; preserved story baseline commit `688b2095bc9554549f212d0e6ed7c52e00d76fa6`.
- DEV-STORY: Created `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md` with closure decision `CONCERNS`, evidence inventory, P0 cold-read matrix, Backend authority review, executable-closure decision, verification log, leak-scan allowlist, and remaining scope.
- DEV-STORY: Determined Story 3.7 is document-only for executable closure support; `internal/scrapctl/evidencebundle` remains unchanged because the existing Tier 3/security gate schema should not be reused as an Epic 3 closure schema.
- DEV-STORY: Focused P0 restore authority, concurrency/cancellation, restore failure, and encrypted restore test commands passed.
- DEV-STORY: Affected package regression passed for `internal/shard`, `internal/localblock`, `internal/server`, `internal/store`, `internal/eviction`, `internal/backend`, `internal/encryption`, and `internal/scrapctl/evidencebundle`.
- DEV-STORY: Backend authority scans found no non-test `internal/server` or `internal/store` Backend discovery matches; Shard matches were committed ConfirmUpload authority, upload verification, or restore full-object GET from committed metadata.
- DEV-STORY: Credential and identifier leak scans over touched Story 3.7 artifacts passed with allowlisted BMAD prose, story keys, local command paths, sprint tracker paths, and source/test identifiers only.
- DEV-STORY: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed, including format diff, package-boundary checks, buf lint/generate diff, golangci-lint, all Go tests, race tests, integration-tagged LocalStack/OpenBao tests, and `scrapd`/`scrapctl` builds.
- DEV-STORY: Moved Story 3.7 and sprint status to `review` after all tasks, ACs, File List, and verification gates were complete.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Scoped Story 3.7 to Epic 3 evidence inventory, Backend authority proof, P0 cold-read closure classification, and explicit PASS/CONCERNS/FAIL gate language.
- Preserved non-goals for direct Backend streaming, production OpenBao proof, real S3/IAM production rehearsal, final V2 release readiness, and public/storage format changes.
- Identified current risk areas for implementation: scoped single-Member cold-read proof, skipped E2E targets without `SCRAP_E2E=1`, and current `internal/scrapctl/evidencebundle` gate naming not being an Epic 3 closure schema.
- Created Epic 3 closure evidence with decision `CONCERNS`: no missing P0 cold-read proof at the current Epic 3 local/package scope, but not enough deployed/production evidence to call the closure a PASS or V2 release-ready.
- Linked Story 3.1 through Story 3.6 evidence artifacts and separated local/package proof from Tier 2/E2E, production OpenBao, and real S3/IAM release evidence.
- Proved Backend inventory is not hot-path authority through current tests and source scans; restore remains a full-object GET from committed Confirmed Upload Catalog metadata.
- Left executable closure support document-only; no code change was needed and no `internal/scrapctl/evidencebundle` gate behavior changed.
- Completed focused P0 evidence tests, affected-package regression, Backend authority scans, leak scans, `git diff --check`, and full `make check`.

### File List

- `_bmad-output/implementation-artifacts/3-7-backend-durability-and-cold-read-closure-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

## Change Log

- 2026-06-12: Created Story 3.7 Backend Durability and Cold-Read Closure Evidence context and moved status to ready-for-dev.
- 2026-06-12: Started Story 3.7 implementation and moved status to in-progress.
- 2026-06-12: Completed Story 3.7 implementation with closure decision `CONCERNS` and moved status to review.
