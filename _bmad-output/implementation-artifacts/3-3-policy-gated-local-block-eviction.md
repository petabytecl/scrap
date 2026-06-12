---
baseline_commit: 39efc4565bb56f52d50d2d6222c6a5ee2567c2d2
created: 2026-06-11T21:36:36-04:00
---

# Story 3.3: Policy-Gated Local Block Eviction

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a storage operator,
I want eligible local `.blk` copies evicted only under committed policy gates,
so that local disk can be reclaimed without losing authority or metadata.

## Traceability

- Epic: Epic 3 - Operators Can Prove Backend Durability and Restore-First Cold Reads.
- Requirements: FR-7.
- Governing ADRs: ADR 0016, ADR 0017, ADR 0018, ADR 0019, and ADR 0027.
- Prerequisites: Stories 3.1 and 3.2 are done. Story 3.1 proves committed upload confirmation authority. Story 3.2 proves upload pressure and safe admission evidence.
- Current implementation intelligence: `internal/eviction`, `internal/localblock`, `internal/shard/eviction_*.go`, `internal/shard/restore.go`, `internal/admin/eviction.go`, and `internal/scrapctl/eviction.go` already implement most Phase 4 campaign, marker, restore, health, admin, and CLI machinery. This story should verify and close gaps, not recreate those modules.
- Non-goals: all-local-copy eviction, leader cold-read policy, restore-first cold reads, restore failure taxonomy closure, encryption-compatible restore evidence, production security gate closure, real S3/IAM rehearsal, and final V2 release readiness.

## Acceptance Criteria

1. **AC-3.3.1 - Dry-run reports eligibility without mutation.** Given a sealed Block has committed upload confirmation, when eviction dry-run executes, then eligibility is reported without mutating local state. Evidence identifies the dry-run command and changed boundary.
2. **AC-3.3.2 - Apply preserves metadata and local authority boundaries.** Given eviction apply executes for an eligible copy, when local lifecycle markers update, then `.idx` metadata remains available for metadata-only reads. Evidence proves Local Block Lifecycle remains per-Member filesystem evidence only.
3. **AC-3.3.3 - Ineligible eviction fails closed with non-sensitive output.** Given a Block is ineligible, when eviction is requested, then the request fails closed with actionable, non-sensitive output. Evidence records the failure command and redaction proof.

## Tasks / Subtasks

- [x] Build the Story 3.3 evidence checklist before code changes. (AC: 1-3)
  - [x] Create or update `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` with AC rows, commands, results, and remaining runtime evidence gaps.
  - [x] Record the current authority path: committed `ConfirmUpload` -> Confirmed Upload Catalog / committed marker -> candidate dry-run -> in-memory plan token -> apply revalidation -> local eviction marker -> `.blk` unlink.
  - [x] Record changed boundaries and non-goals: no Raft command changes, no Pebble key-prefix changes, no Backend inventory authority, no API-visible Document semantics change.
- [x] Prove dry-run reports eligible Blocks without mutating local state. (AC: 1)
  - [x] Add or strengthen a focused test around `Shard.CreateEvictionPlan` showing dry-run reads committed Confirmed Upload Catalog state and local lifecycle state, returns selected/skipped candidates, and does not create an eviction marker, remove `.blk`, remove `.idx`, or alter Document visibility.
  - [x] Verify dry-run plan output includes bounded plan ID, member identity, Shard ID, Block IDs, bytes, eligibility timestamps, active config, and skip counts.
  - [x] Verify dry-run excludes pending replacement uploads and skips leader-local hot copies in Phase 4.
- [x] Prove eviction apply mutates only Local Block Lifecycle state and preserves metadata-only reads. (AC: 2)
  - [x] Add or strengthen a Shard-level test that applies a stored plan to an eligible follower-local Block with validation sampling disabled or bounded so the Block remains evicted after apply.
  - [x] Assert the eviction marker exists, `.blk` is absent, `.idx` remains present, and `localblock.Classify` reports `evicted`.
  - [x] Assert `HeadDocument` and `FindDocuments` remain served from retained `.idx` / Projection Resolution without restoring `.blk`.
  - [x] Assert Raft metadata, Pebble visibility, Upload Outbox, and Confirmed Upload Catalog authority are unchanged by local eviction.
  - [x] If validation sampling is exercised, prove it restores through the existing full-Block restore path and records validation separately from apply success.
- [x] Prove fail-closed behavior for ineligible or stale eviction. (AC: 3)
  - [x] Cover at least these cases with focused tests or existing-test evidence: missing committed `ConfirmUpload`, pending replacement upload, disabled apply, current leader hot-copy requirement, hot residency window not elapsed, stale member identity, expired/unknown plan, foreign Shard selection, malformed marker, missing `.idx`, unexpected local loss, and Backend restore unavailable for sampled validation.
  - [x] Verify apply revalidates selected Blocks and reports per-Block skip/failure reasons instead of silently recomputing a new candidate set.
  - [x] Verify admin HTTP maps precondition failures to `412`, in-progress apply to `409`, transient restore/backend unavailability to `503`, and invalid requests to `400`.
- [x] Prove operator output and evidence are redacted. (AC: 3)
  - [x] Review `internal/scrapctl/eviction.go` and `internal/admin/eviction.go` for raw Backend object keys, validation tokens, raw Document identifiers, filesystem paths, auth claims, peer addresses, or dependency error strings in operator-facing text, JSON, errors, and evidence.
  - [x] If current output exposes `backend_key` or validation-token data, patch the operator-facing output or replace it with bounded metadata such as counts, sizes, and Block IDs. Do not weaken the internal marker expectation needed for restore authority.
  - [x] Add or update tests proving failure output is actionable but does not leak Backend keys, validation tokens, raw `transaction_id`, raw `document_name`, idempotency keys, file paths, request IDs, trace IDs, certificates, or auth claims.
  - [x] Add rerunnable leak-scan commands to the evidence artifact with an explicit allowlist for story prose and local test fixtures only.
- [x] Preserve package and architecture boundaries. (AC: 1-3)
  - [x] Keep campaign lifecycle logic in `internal/eviction`, marker/classification transitions in `internal/localblock`, Shard authority and side effects in `internal/shard`, HTTP operator plumbing in `internal/admin`, and human workflow rendering in `internal/scrapctl`.
  - [x] Do not create `common`, `shared`, `util`, a new eviction package, a new Backend wrapper, new assertion libraries, or new mocking frameworks.
  - [x] Do not change Block/Frame layout, Backend object key format, public/peer/admin proto contracts, Raft command shape, Confirmed Upload Catalog schema, Pebble key prefixes, or storage identity.
- [x] Run focused and regression verification. (AC: 1-3)
  - [x] Run focused package tests for eviction, local lifecycle, Shard apply/restore, admin HTTP, and `scrapctl`.
  - [x] Run a focused race gate for Shard eviction apply/restore singleflight or lifecycle mutation behavior if any shared state changes.
  - [x] Run `make check` before BMAD code-review handoff unless a narrower blocker is documented in the story.

## Dev Notes

### Current State

- `CONTEXT.md` defines Confirmed Upload Catalog as a derived per-Shard record of sealed Blocks whose Backend upload was confirmed by committed Raft state. It is not eviction state and not Backend inventory.
- `CONTEXT.md` defines Local Block Lifecycle as per-Member filesystem classification for one local Block copy. It must not decide Document visibility, durable upload authority, Shard membership, or client read availability policy.
- PRD FR-7 requires partial local eviction and full-Block restore. `.idx` metadata remains local for `HeadDocument` and `FindDocuments`; direct Backend streaming is not part of this story.
- Epic 3 says Story 3.3 closes policy-gated local eviction only. Stories 3.4 through 3.7 own all-local-copy eviction, restore-first cold reads, restore failure/corruption semantics, encryption-compatible restore, and broader durability closure.
- ADR 0016 limits Phase 4 to follower-local `.blk` eviction after committed upload confirmation, hot residency policy, local lifecycle checks, no quarantine/repair/open-reader conflict, and operator-gated campaign approval.
- ADR 0016 requires dry-run plan tokens, bounded candidate selection, apply-time revalidation, marker-before-unlink ordering, `.idx` retention, restart classification, sampled validation, and health/telemetry evidence.
- ADR 0017 assigns marker JSON, startup classification, and local filesystem transitions to `internal/localblock`. Shard supplies authority facts; Local Block Lifecycle is not durable upload or visibility authority.
- ADR 0018 assigns in-memory campaign plan/running/completed result lifecycle to `internal/eviction`. Shard remains the adapter for Confirmed Upload Catalog, leadership, Backend restore, health, metrics, and local marker/unlink side effects.
- ADR 0019 treats eviction apply as a dangerous admin operation requiring admin operator authorization and audit in production security work. This story can exercise existing authorization boundaries, but production security closure remains later.
- ADR 0027 is a future boundary: all-local-copy eviction and restore-first cold reads are not Story 3.3 scope.

### Existing Implementation To Reuse

- `internal/eviction/types.go` defines plan/apply/status JSON contracts, bounded reason/status values, skip reasons, restore failure reasons, and health snapshot fields.
- `internal/eviction/planner.go` builds deterministic oldest-first bounded plans, applies hot residency windows, rejects expanded caps, skips leader hot copies, and records selected/skipped candidates.
- `internal/eviction/apply.go` executes selected Blocks, counts evicted/skipped/failed outcomes, stops at first failure, and computes final apply status.
- `internal/eviction/campaigns.go` owns in-memory plan TTL, running apply state, cached completed results, stale member validation, and status reporting.
- `internal/localblock/lifecycle.go` owns versioned eviction/restore markers, strict JSON decoding, classification states `hot`, `evicted`, `hot_cleanup_needed`, `metadata_loss`, and `unexpected_loss`, and stale hot marker cleanup.
- `internal/localblock/transitions.go` owns marker expectation validation, marker removal, `.blk` unlink, restored Block publication, and restore marker recording.
- `internal/shard/confirmed_upload_authority.go` writes and reads committed `ConfirmUpload` authority marker JSON from committed upload metadata. This is local committed authority cache, not Backend inventory.
- `internal/shard/eviction_config.go` parses `SCRAP_EVICTION_*` config and fails invalid explicit values clearly.
- `internal/shard/eviction_planner.go` adapts Confirmed Upload Catalog entries and Local Block Lifecycle classification into `eviction.PlanCandidate`.
- `internal/shard/eviction_apply.go` gates apply on `SCRAP_EVICTION_ENABLED`, rebuild state, stable leadership, plan identity, committed Confirmed Upload authority, current local lifecycle, hot residency, and leader hot-copy requirement before marker/unlink.
- `internal/shard/eviction_validation.go` samples evidence-run validations by restoring and reading the first Document from the retained `.idx` through the normal read path.
- `internal/shard/restore.go` restores evicted Blocks from committed Confirmed Upload metadata only, validates marker expectation, downloads/stages/verifies full `.blk`, publishes atomically, records restore markers, and maps Backend failures.
- `internal/shard/eviction_health.go` separates evicted, hot cleanup, metadata loss, unexpected loss, quarantine, and restore failure health.
- `internal/admin/eviction.go` exposes `POST /admin/eviction/plans`, `POST /admin/eviction/plans/{plan_id}/apply`, and `GET /admin/eviction/plans/{plan_id}` with admin reader/operator authorization.
- `internal/scrapctl/eviction.go` owns the human operator workflow for `scrapctl eviction plan|apply|status`.

### Implementation Guidance

- Start by running the focused tests that already exist. If the current code already satisfies an AC, capture that proof in the evidence artifact instead of rewriting the implementation.
- Add tests before production-code changes when a gap is found. Expected likely gaps are evidence completeness, metadata-only read proof after eviction, and redaction of operator output.
- Keep dry-run side effects limited to in-memory plan storage and metrics. Dry-run must not write markers, unlink `.blk`, mutate `.idx`, mutate Raft/Pebble authority, or restore from Backend.
- For AC-3.3.2, avoid a validation sample restoring the only evicted Block before assertions. Use `MaxValidateSamples=0` or a non-restoring focused fixture for the metadata-only read proof.
- `HeadDocument` and `FindDocuments` must not restore `.blk` bytes. If a test observes Backend access from metadata-only reads, treat it as a defect.
- Apply should continue through per-Block drift skips but stop on systemic failure. Do not silently recompute selected candidates during apply.
- If changing output fields, preserve internal restore authority data. The eviction marker and Shard restore path still need Backend key, size, and validation token expectations from committed metadata.
- Treat `backend_key` in `scrapctl` text or admin/evidence output as a redaction risk. Operator-facing output should prefer Block ID, Shard ID, sizes, eligibility timestamps, state, and bounded reason/status values.
- Do not use Backend `List`, object existence, HEAD-only observations, local files, or Local Block Lifecycle marker presence as upload confirmation authority. Eviction eligibility starts from committed Confirmed Upload Catalog state.
- Do not broaden reason values casually. `evidence_run` is the currently supported plan reason; new low-cardinality reasons need explicit tests and docs.

### Previous Story Intelligence

- Story 3.1 established the authority model: Backend `.blk` and `.idx` success without committed `ConfirmUpload` must not create committed upload authority.
- Story 3.1 review fixes showed that tests must wait for real state transitions before asserting evidence, must use actual pending/confirmed Block IDs, and must not overclaim skipped E2E runs.
- Story 3.2 established the evidence style: AC rows, exact commands, verification log, leak-scan allowlist, and explicit CONCERNS for runtime evidence that was not actually run.
- Story 3.2 review fixes matter here: bound all metric status values, assert every collected metric has expected bounded attributes, scope filesystem cleanup assertions to the intended file classes, and prove rejected/failed operations leave no accepted partial state.
- Older Story 4.3 established that Local Block Lifecycle may gate local upload eligibility but must not become Document visibility or durable upload authority.

### Project Structure Notes

Likely update:

- `_bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md` - story status, debug log, completion notes, and file list during dev.
- `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` - Story 3.3 evidence table and verification log.
- `internal/eviction/*_test.go` - only if planner/campaign status gaps are found.
- `internal/localblock/*_test.go` - only if marker/classification transition gaps are found.
- `internal/shard/eviction_planner_test.go`, `internal/shard/eviction_planner_internal_test.go`, `internal/shard/eviction_apply_test.go`, `internal/shard/restore_test.go`, `internal/shard/eviction_metrics_otel_test.go` - likely focused Shard evidence tests.
- `internal/admin/eviction_test.go` - HTTP status and authorization/error mapping evidence.
- `internal/scrapctl/eviction.go` and `internal/scrapctl/eviction_test.go` - likely redaction and human output evidence.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - story status transitions.

Likely avoid:

- `proto/`, `gen/`, Block/Frame format code, Backend object key construction, public `DocumentService`, peer `PeerService`, routing, placement, production security policy, and OpenBao/encryption code.
- `internal/backend/*` unless an existing fake or adapter defect directly blocks validation.
- New dependencies, assertion libraries, mocking frameworks, or package-level globals.

### Testing Requirements

Run focused gates first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestServer_.*Eviction' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEviction' -count=1 -v
```

Run regression gates before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock ./internal/shard ./internal/admin ./internal/scrapctl -count=1
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Suggested leak scans. Store patterns in shell variables so the story file does not self-match credential-shaped words when copied into evidence:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md _bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md internal/eviction internal/localblock internal/shard internal/admin internal/scrapctl
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md _bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md internal/eviction internal/localblock internal/shard internal/admin internal/scrapctl
```

Runtime evidence is not required to create the story. If deployed eviction evidence is claimed during dev, record the exact target, environment, commit, and pass/fail result. If not run, mark it CONCERNS rather than PASS.

### Latest Technical Information

- GitHub repo search for `policy gated local block eviction storage gateway Go confirmed upload` returned no reusable implementation candidates.
- GitHub code search for `"Confirmed Upload Catalog" "eviction" language:Go` returned no reusable implementation candidates.
- Current module versions relevant to this story are `google.golang.org/grpc v1.81.1`, `go.opentelemetry.io/otel v1.44.0`, `go.opentelemetry.io/otel/sdk/metric v1.44.0`, `github.com/cockroachdb/pebble v1.1.5`, and `go.etcd.io/raft/v3 v3.6.0`.
- External storage gateway prior art distinguishes upload-buffer/cache storage, pending-upload durability, local cache for low-latency reads, and evicting cache only when safe. Use this only as general context; do not import AWS Storage Gateway product semantics into S.C.R.A.P.

### References

- `CONTEXT.md` - Confirmed Upload Catalog and Local Block Lifecycle glossary.
- `_bmad-output/planning-artifacts/epics.md` - Epic 3 and Story 3.3 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-7.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-3, authority patterns, evidence patterns, and project structure.
- `_bmad-output/project-context.md` - package boundaries, telemetry/redaction, testing, and commit rules.
- `_bmad-output/implementation-artifacts/3-1-committed-backend-upload-confirmation.md` - committed upload authority guardrails.
- `_bmad-output/implementation-artifacts/epic-3-backend-upload-confirmation-evidence.md` - Story 3.1 evidence style and authority path.
- `_bmad-output/implementation-artifacts/3-2-upload-pressure-and-safe-admission-evidence.md` - prior story evidence style and review-fix learnings.
- `_bmad-output/implementation-artifacts/epic-3-upload-pressure-evidence.md` - verification log and leak-scan allowlist pattern.
- `_bmad-output/implementation-artifacts/4-3-upload-pressure-pause-gate-and-scrub-coordination-ownership.md` - Local Block Lifecycle ownership guidance.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - Phase 4 partial eviction policy and evidence contract.
- `docs/adr/0017-local-block-lifecycle-module.md` - `internal/localblock` ownership.
- `docs/adr/0018-eviction-campaign-module.md` - `internal/eviction` ownership.
- `docs/adr/0019-production-security-boundary.md` - admin operation sensitivity and future production security gate.
- `docs/adr/0027-phase-5-restore-first-cold-reads.md` - later all-local-copy eviction and restore-first read boundary.
- `internal/eviction/types.go`
- `internal/eviction/planner.go`
- `internal/eviction/apply.go`
- `internal/eviction/campaigns.go`
- `internal/localblock/lifecycle.go`
- `internal/localblock/transitions.go`
- `internal/shard/confirmed_upload_authority.go`
- `internal/shard/eviction_config.go`
- `internal/shard/eviction_planner.go`
- `internal/shard/eviction_apply.go`
- `internal/shard/eviction_validation.go`
- `internal/shard/eviction_health.go`
- `internal/shard/restore.go`
- `internal/admin/eviction.go`
- `internal/scrapctl/eviction.go`
- `https://docs.aws.amazon.com/storagegateway/latest/vgw/decide-local-disks-and-sizes.html`
- `https://aws.amazon.com/storagegateway/faqs/`

## Dev Agent Record

### Agent Model Used

TBD by dev-story.

### Debug Log References

- CREATE-STORY: `git status --short --branch` confirmed clean `v2...origin/v2`.
- CREATE-STORY: Loaded `CONTEXT.md`, `_bmad-output/project-context.md`, sprint status, Epic 3, FR-7, architecture DG-3/authority patterns, ADR 0016, ADR 0017, ADR 0018, ADR 0019, ADR 0027, Story 3.1, Story 3.2, and existing eviction/localblock/shard/admin/scrapctl code.
- CREATE-STORY: Current baseline commit is `39efc4565bb56f52d50d2d6222c6a5ee2567c2d2`.
- RESEARCH: `gh search repos "policy gated local block eviction storage gateway Go confirmed upload" --limit 5 --json fullName,url,description` returned no reusable implementation candidates.
- RESEARCH: `gh search code '"Confirmed Upload Catalog" "eviction" language:Go' --limit 5 --json repository,path,url` returned no reusable implementation candidates.
- RESEARCH: `go list -m google.golang.org/grpc go.opentelemetry.io/otel go.opentelemetry.io/otel/sdk/metric github.com/cockroachdb/pebble go.etcd.io/raft/v3` confirmed current pinned module versions.
- RESEARCH: External AWS Storage Gateway docs reviewed only for general cache/upload-buffer prior art; no AWS-specific product semantics adopted.
- DEV-STORY: Started implementation from clean `v2...origin/v2` after pushing story creation commit `14513092a4f87513b1460a97f8148092ce38b51f`; preserved story baseline commit `39efc4565bb56f52d50d2d6222c6a5ee2567c2d2`.
- DEV-STORY: Created `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md` before behavior changes with authority path, changed-boundary list, AC coverage gaps, planned commands, leak-scan allowlist, and pending evidence rows.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestCreateEvictionPlanDoesNotMutateLocalBlockState -count=1 -v` failed to compile because the new test treated `stageHotConfirmedBlockForEvictionApply` as returning a value.
- GREEN: Fixed the test to read committed upload state from the existing index, then `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestCreateEvictionPlanDoesNotMutateLocalBlockState -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestCreateEvictionPlan|TestEvictionCandidatesExcludePendingReplacementUploads' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction -run 'TestBuildPlan' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestShardCreateEvictionPlanStoresTokenAndSkipsLeaderBlocks -count=1 -v` passed.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestApplyEvictionPlanPreservesIndexMetadataReads -count=1 -v` failed to compile because the new test used the helper overload that does not accept a validation sample count.
- GREEN: Switched to `storeEvictionApplyPlanForConfirmedUploads` with `MaxValidateSamples=0`, then `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run TestApplyEvictionPlanPreservesIndexMetadataReads -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestApplyEvictionPlanPreservesIndexMetadataReads|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock|TestApplyEvictionPlanValidatesEvidenceRunSample' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/localblock -run 'TestUnlinkBlockDataRemovesOnlyBlockFile|TestClassifyLifecycle|TestPublishRestoredBlockRecordsLifecycleTransition' -count=1 -v` passed.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction -run TestPlanJSONOmitsBackendKeys -count=1 -v` failed because `PlanBlock` JSON exposed `backend_key`.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEvictionPlanPostsRequestAndPrintsEvidence|TestEvictionStatusPrintsFinalCampaignEvidence' -count=1 -v` failed because `scrapctl` text rendered `backend_key`.
- GREEN: Changed `eviction.PlanBlock.BackendKey` to `json:"-"` and removed `backend_key` from `scrapctl` text block rendering.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction -run TestPlanJSONOmitsBackendKeys -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEvictionPlanPostsRequestAndPrintsEvidence|TestEvictionStatusPrintsFinalCampaignEvidence' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestApplyEvictionPlanRequiresCommittedAuthorityBeforeUnlink|TestApplyEvictionPlanRejectsMissingRestoreBackend|TestApplyEvictionPlanStopsWhenDisabled|TestApplyEvictionPlanSkipsFreshlyRestoredBlock|TestApplyEvictionPlanRejectsInvalidPlanState|TestApplyEvictionPlanRejectsRebuildInProgress|TestApplyEvictionPlanFailsBlockWhenConfirmationDrifts|TestApplyEvictionPlanReportsCompletedWithSkipsForDrift|TestEvictionHealthRebuildFailsClosedForMalformedMarker|TestApplyEvictionPlanDoesNotRecordHealthForUnconfirmedSelectedBlock|TestApplyEvictionPlanDoesNotRecordHealthForForeignShardSelectedBlock' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestServer_CreateEvictionPlanTargetMismatchReturnsPreconditionFailed|TestServer_CreateEvictionPlanRejectsInvalidJSON|TestServer_ApplyEvictionPlanNotFoundReturnsPreconditionFailed|TestServer_ApplyEvictionPlanMapsErrors|TestServer_ApplyEvictionPlanRejectsInvalidRequest' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEvictionPlanPostsRequestAndPrintsEvidence|TestEvictionApplyPrintsSkipAndFailureDetails|TestEvictionApplyReturnsErrorForFailedResult|TestEvictionApplyReportsHTTPError|TestEvictionStatusPrintsFinalCampaignEvidence|TestEvictionApplyRequiresConfirm|TestEvictionApplyRequiresPlanID' -count=1 -v` passed.
- SECURITY: Focused credential and raw-identifier scans over changed files found only story/evidence prose and internal test fixture metadata covered by the allowlist; operator-facing JSON/text redaction is enforced by tests.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestServer_.*Eviction' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEviction' -count=1 -v` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock ./internal/shard ./internal/admin ./internal/scrapctl -count=1` passed.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v` passed.
- LINT-FIX: Initial `env GOCACHE=/tmp/scrap-v2-go-build make check` failed on cyclomatic complexity and context argument order in the two new Shard tests; extracted focused assertion helpers without changing behavior.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'TestApplyEvictionPlanPreservesIndexMetadataReads|TestCreateEvictionPlanDoesNotMutateLocalBlockState' -count=1 -v` passed after the helper extraction.
- VERIFY: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after the test-only helper extraction, covering format, package boundaries, proto generation diff, lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds.
- SECURITY: `git diff --check` passed.
- SECURITY: Corrected credential and raw-identifier scans over changed files passed with only allowlisted story/tracker prose and internal test fixture metadata; the broader attempted scan was discarded because the shell expanded an empty pattern before `rg`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Scoped Story 3.3 to policy-gated Phase 4 local `.blk` eviction, dry-run evidence, metadata retention, fail-closed ineligible behavior, and redacted operator output.
- Identified existing implementation modules to reuse rather than recreate: `internal/eviction`, `internal/localblock`, Shard eviction/restore adapters, admin HTTP, and `scrapctl eviction`.
- Explicitly excluded all-local-copy eviction, restore-first cold reads, restore failure/corruption semantics closure, encryption, production security closure, and final release evidence.
- Flagged likely redaction review area around `backend_key` in operator-facing eviction output.
- Created the Story 3.3 local eviction evidence artifact before code changes, including authority path, changed boundaries, AC coverage map, planned verification commands, and leak-scan allowlist.
- Added `TestCreateEvictionPlanDoesNotMutateLocalBlockState` to prove dry-run plan creation selects eligible follower-local Blocks from committed upload state without writing an eviction marker, removing `.blk`, or mutating `.idx`.
- Verified existing planner coverage for bounded selection, restore-time hot residency, cap rejection, pending replacement upload exclusion, and leader-local hot-copy skips.
- Added `TestApplyEvictionPlanPreservesIndexMetadataReads` to prove apply-driven local eviction writes the lifecycle marker, unlinks only `.blk`, leaves `.idx`, retains committed Confirmed Upload authority, leaves Upload Outbox empty, and preserves `HeadDocument`/`FindDocuments` without restore.
- Verified existing metadata-read and validation-sample coverage for manually evicted confirmed Blocks, no Backend discovery on `FindDocuments`, and full-Block restore validation behavior.
- Added `TestPlanJSONOmitsBackendKeys` and strengthened `scrapctl eviction plan/status` tests so operator JSON/text cannot expose raw Backend keys.
- Patched `PlanBlock.BackendKey` to remain in-process only and removed `backend_key` from `scrapctl` text rendering.
- Verified existing fail-closed coverage for disabled apply, missing committed authority, missing restore Backend, hot residency, stale/expired/running plans, rebuild in progress, malformed markers, drifted confirmation, unconfirmed selections, foreign Shard selections, admin HTTP status mapping, and CLI failure output.
- Preserved package boundaries: campaign logic stayed in `internal/eviction`, lifecycle transitions in `internal/localblock`, authority and side effects in `internal/shard`, admin HTTP in `internal/admin`, and operator rendering in `internal/scrapctl`.
- Completed focused, package, Shard race, broad `make check`, whitespace, and corrected leak-scan verification for Story 3.3.
- Moved Story 3.3 to review with all tasks and subtasks complete.

### File List

- `_bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md`
- `_bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/eviction/planner_test.go`
- `internal/eviction/types.go`
- `internal/shard/eviction_apply_test.go`
- `internal/scrapctl/eviction.go`
- `internal/scrapctl/eviction_test.go`

## Change Log

- 2026-06-11: Created Story 3.3 Policy-Gated Local Block Eviction context and moved status to ready-for-dev.
- 2026-06-11: Started Story 3.3 implementation and moved status to in-progress.
- 2026-06-11: Completed Story 3.3 implementation, evidence, and verification; moved status to review.
