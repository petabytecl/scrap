# Epic 3 Local Eviction Evidence

Status: complete

Story: 3.3 - Policy-Gated Local Block Eviction

Story status: done

Implementation baseline: `14513092a4f87513b1460a97f8148092ce38b51f`

## Scope

This artifact covers the Story 3.3 local eviction evidence gate. It proves that
eligible follower-local `.blk` copies can be planned and evicted only after
committed upload confirmation and policy gates, while retained `.idx` metadata
continues to support metadata-only reads.

It does not close all-local-copy eviction, restore-first cold reads, restore
failure and corruption semantics, encryption-compatible restore, production
security startup, real S3/IAM rehearsal, or final V2 release readiness.

## Authority Path

1. A sealed Block is uploaded and confirmed through committed `ConfirmUpload`
   metadata.
2. Committed upload state materializes the derived Confirmed Upload Catalog and
   local committed confirmation marker.
3. Eviction dry-run scans committed confirmed uploads and local lifecycle state.
4. The dry-run stores a bounded in-memory plan token with selected and skipped
   Blocks.
5. Apply validates the plan token, member identity, Shard ID, local lifecycle,
   leadership, hot residency, rebuild state, and committed upload authority.
6. Apply writes and fsyncs the local eviction marker before unlinking the local
   `.blk`.
7. Apply keeps `.idx`, Raft metadata, Pebble visibility, Upload Outbox, and
   Confirmed Upload Catalog state unchanged.
8. Metadata-only reads use retained `.idx` data and must not restore `.blk`.

Backend PUT, HEAD, list, object existence, Backend keys, local file presence,
and Local Block Lifecycle markers are evidence inputs only. They are not
Document visibility authority, durable upload authority, Shard membership
authority, or public read/write routing authority.

## Changed Boundary List

- Story 3.3 may add or strengthen tests and evidence for existing eviction,
  Local Block Lifecycle, Shard, admin, and `scrapctl` paths.
- Story 3.3 may patch operator-facing output if raw Backend metadata leaks into
  human or evidence surfaces.
- Story 3.3 must not change Block/Frame layout, Backend object key format,
  public/peer/admin proto contracts, Raft command shape, Confirmed Upload
  Catalog schema, Pebble key prefixes, storage identity, or production security
  policy.

## Evidence Checklist

| AC | Required proof | Current coverage | Gap before closure |
| --- | --- | --- | --- |
| AC-3.3.1 | Dry-run reports eligible/skipped Blocks from committed upload and local lifecycle state without marker/unlink/index mutation. | `internal/eviction/planner_test.go`; `internal/shard/eviction_planner_test.go`; `internal/shard/eviction_planner_internal_test.go`; `internal/shard/eviction_apply_test.go`; `internal/scrapctl/eviction_test.go`. | Closed by `TestCreateEvictionPlanDoesNotMutateLocalBlockState` plus existing planner, leader-skip, and pending replacement upload coverage. |
| AC-3.3.2 | Apply writes lifecycle marker, unlinks only `.blk`, keeps `.idx`, and leaves metadata-only reads available without restoring bytes. | `internal/localblock/transitions_test.go`; `internal/shard/eviction_apply_test.go`; `internal/shard/read_lifecycle_test.go`; `internal/shard/find_documents_test.go`; `internal/shard/restore_test.go`. | Closed by `TestApplyEvictionPlanPreservesIndexMetadataReads` plus existing metadata-read, no-restore, and validation-sample coverage. |
| AC-3.3.3 | Ineligible or stale eviction fails closed with actionable non-sensitive admin/CLI output. | `internal/shard/eviction_apply_test.go`; `internal/admin/eviction_test.go`; `internal/scrapctl/eviction_test.go`; `internal/eviction/planner_test.go`; `internal/eviction/redaction.go`; `internal/shard/eviction_metrics_otel_test.go`. | Closed by fail-closed Shard/admin/CLI coverage, Backend key redaction tests for JSON/text output, and apply/status/HTTP error redaction tests. |

## Planned Verification

Command A:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock -count=1 -v
```

Command B:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v
```

Command C:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestServer_.*Eviction' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestEviction' -count=1 -v
```

Command D:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/eviction ./internal/localblock ./internal/shard ./internal/admin ./internal/scrapctl -count=1
```

Command E:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run 'Test.*Eviction|Test.*Restore' -count=1 -v
```

Command F:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Command G:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
scan_paths='_bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md _bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/admin/eviction.go internal/admin/eviction_test.go internal/eviction/planner_test.go internal/eviction/redaction.go internal/eviction/redaction_test.go internal/eviction/types.go internal/scrapctl/eviction.go internal/scrapctl/eviction_test.go internal/shard/eviction_apply_test.go'
rg -n --pcre2 "$cred_pattern" $scan_paths
```

Command H:

```bash
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|validation [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
scan_paths='_bmad-output/implementation-artifacts/3-3-policy-gated-local-block-eviction.md _bmad-output/implementation-artifacts/epic-3-local-eviction-evidence.md _bmad-output/implementation-artifacts/sprint-status.yaml internal/admin/eviction.go internal/admin/eviction_test.go internal/eviction/planner_test.go internal/eviction/redaction.go internal/eviction/redaction_test.go internal/eviction/types.go internal/scrapctl/eviction.go internal/scrapctl/eviction_test.go internal/shard/eviction_apply_test.go'
rg -n --pcre2 "$identifier_pattern" $scan_paths
```

## Verification Log

| Command | Ran at | Exit | Result | Notes |
| --- | --- | --- | --- | --- |
| Command A | 2026-06-11T21:52:17-04:00 | 0 | PASS | `go test ./internal/eviction ./internal/localblock -count=1 -v` passed campaign, planner, JSON redaction, and lifecycle transition coverage. |
| Command B | 2026-06-11T21:52:20-04:00 | 0 | PASS | `go test ./internal/shard -run 'Test.*Eviction\|Test.*Restore' -count=1 -v` passed Shard eviction, restore, health, metrics, metadata, and fail-closed coverage. |
| Command C | 2026-06-11T21:52:17-04:00 | 0 | PASS | `go test ./internal/admin -run 'TestServer_.*Eviction' -count=1 -v` and `go test ./internal/scrapctl -run 'TestEviction' -count=1 -v` passed admin HTTP and operator workflow coverage. |
| Command D | 2026-06-11T21:52:30-04:00 | 0 | PASS | `go test ./internal/eviction ./internal/localblock ./internal/shard ./internal/admin ./internal/scrapctl -count=1` passed package regression. |
| Command E | 2026-06-11T21:52:34-04:00 | 0 | PASS | `go test -race ./internal/shard -run 'Test.*Eviction\|Test.*Restore' -count=1 -v` passed focused Shard race coverage. |
| Command F | 2026-06-11T21:55:52-04:00 | 0 | PASS | `make check` passed after a test-only helper extraction fixed initial `cyclop` and context-argument lint findings in the new Shard tests. Gate covered format, boundaries, proto generation diff, lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds. |
| Command G | 2026-06-11T21:56:27-04:00 | 0 | PASS | Corrected credential scan over changed files reported 28 allowlisted matches: sprint/story/evidence prose plus internal validation-token test fixture fields. No key material, password, bearer value, AWS access key, GitHub token, or private key was found. |
| Command H | 2026-06-11T21:56:27-04:00 | 0 | PASS | Corrected raw-identifier scan over changed files reported 69 allowlisted matches: story/evidence prose, command examples, sprint path, and test-only Backend key fixtures that are asserted absent from operator output. |
| AC-3.3.1 shard dry-run | 2026-06-11T21:44:11-04:00 | 0 | PASS | `go test ./internal/shard -run 'TestCreateEvictionPlan|TestEvictionCandidatesExcludePendingReplacementUploads' -count=1 -v` passed the new no-mutation dry-run test and pending replacement exclusion. |
| AC-3.3.1 planner | 2026-06-11T21:44:11-04:00 | 0 | PASS | `go test ./internal/eviction -run 'TestBuildPlan' -count=1 -v` passed bounded selection, expanded cap rejection, and restore-time hot residency coverage. |
| AC-3.3.1 leader skip | 2026-06-11T21:44:11-04:00 | 0 | PASS | `go test ./internal/shard -run TestShardCreateEvictionPlanStoresTokenAndSkipsLeaderBlocks -count=1 -v` passed leader hot-copy skip and stored-plan token coverage. |
| AC-3.3.2 apply metadata | 2026-06-11T21:46:16-04:00 | 0 | PASS | `go test ./internal/shard -run TestApplyEvictionPlanPreservesIndexMetadataReads -count=1 -v` passed after the helper fix. Apply leaves the Block evicted and metadata-only reads available. |
| AC-3.3.2 shard metadata bundle | 2026-06-11T21:46:16-04:00 | 0 | PASS | `go test ./internal/shard -run 'TestApplyEvictionPlanPreservesIndexMetadataReads|TestMetadataReadsStayLocalForEvictedBlock|TestFindDocumentsDoesNotRestoreEvictedConfirmedBlock|TestApplyEvictionPlanValidatesEvidenceRunSample' -count=1 -v` passed. |
| AC-3.3.2 local lifecycle | 2026-06-11T21:46:16-04:00 | 0 | PASS | `go test ./internal/localblock -run 'TestUnlinkBlockDataRemovesOnlyBlockFile|TestClassifyLifecycle|TestPublishRestoredBlockRecordsLifecycleTransition' -count=1 -v` passed. |
| AC-3.3.3 JSON redaction | 2026-06-11T21:49:33-04:00 | 0 | PASS | `go test ./internal/eviction -run TestPlanJSONOmitsBackendKeys -count=1 -v` passed after proving the previous JSON leak. |
| AC-3.3.3 scrapctl redaction | 2026-06-11T21:49:33-04:00 | 0 | PASS | `go test ./internal/scrapctl -run 'TestEvictionPlanPostsRequestAndPrintsEvidence|TestEvictionStatusPrintsFinalCampaignEvidence' -count=1 -v` passed after removing `backend_key` from text output. |
| AC-3.3.3 shard fail-closed | 2026-06-11T21:49:33-04:00 | 0 | PASS | Focused Shard fail-closed command passed disabled apply, missing committed authority, missing restore Backend, hot residency, stale/expired/running plan, rebuild, malformed marker, confirmation drift, unconfirmed selection, and foreign Shard coverage. |
| AC-3.3.3 admin mapping | 2026-06-11T21:49:33-04:00 | 0 | PASS | Focused admin command passed precondition, conflict, unavailable, invalid request, and unexpected error HTTP mappings. |
| AC-3.3.3 CLI failure output | 2026-06-11T21:49:33-04:00 | 0 | PASS | Focused `scrapctl` command passed plan/status redaction, skip/failure details, failed-result exit behavior, HTTP error reporting, and required confirmation/plan ID checks. |
| AC-3.3.3 focused leak scan | 2026-06-11T21:49:33-04:00 | 0 | PASS | Focused credential and raw-identifier scans over changed paths matched only story/evidence prose and internal test fixture metadata covered by the allowlist. |
| Review dry-run visibility | 2026-06-11T22:12:37-04:00 | 0 | PASS | `go test ./internal/shard -run 'TestApplyEvictionPlanPreservesIndexMetadataReads\|TestCreateEvictionPlanDoesNotMutateLocalBlockState' -count=1 -v` passed after adding dry-run Document visibility and Backend-unused assertions. |
| Review redaction coverage | 2026-06-11T22:12:37-04:00 | 0 | PASS | Focused `internal/eviction`, `internal/admin`, and `internal/scrapctl` commands passed new operator-safe apply/status/HTTP error redaction tests. |
| Review regression | 2026-06-11T22:12:52-04:00 | 0 | PASS | Combined affected-package test and focused Shard race gate passed after review fixes. |
| Review full gate | 2026-06-11T22:14:53-04:00 | 0 | PASS | `make check` passed after review fixes, covering format, package boundaries, proto generation diff, lint, `go test ./...`, `go test -race ./...`, integration tests, and command builds. |
| Review leak scan | 2026-06-11T22:15:08-04:00 | 0 | PASS | `git diff --check` passed. Credential scan reported 35 allowlisted matches, and raw-identifier scan reported 103 allowlisted matches: story/evidence prose, sprint paths, redaction fragment tables, and test-only sensitive fixtures asserted absent from operator output. |

## Leak Scan Allowlist

- Command G may match only story keys, sprint tracker prose, redaction
  instructions, or source identifiers in tests. No key material, password,
  bearer value, AWS access key, GitHub token, or private key is allowed.
- Command H may match only story/spec prose, command examples, sprint tracker
  paths, bounded test fixture identifiers, or source comments that explicitly
  describe redaction requirements. No deployed metric label, log field, admin
  output field, trace field, request metadata, Backend object key, validation
  token, auth claim, certificate material, peer address, or raw Document
  identity may leak through operator-facing output.

## Evidence Rows

| AC | Claim | Command | Artifact or Test Path | Ref | Result | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| AC-3.3.1 | Dry-run reports eligibility without local state mutation. | AC-3.3.1 shard dry-run, AC-3.3.1 planner, AC-3.3.1 leader skip | `internal/shard/eviction_apply_test.go`; `internal/shard/eviction_planner_test.go`; `internal/shard/eviction_planner_internal_test.go`; `internal/eviction/planner_test.go` | Story 3.3 working tree | PASS | New Shard test proves dry-run selected the eligible follower-local Block and left `.blk`, `.idx`, and eviction marker unchanged. Existing planner tests prove bounded selection, cap rejection, hot residency, pending replacement exclusion, and leader hot-copy skip. |
| AC-3.3.2 | Apply preserves `.idx` metadata and Local Block Lifecycle remains per-Member filesystem evidence only. | AC-3.3.2 apply metadata, AC-3.3.2 shard metadata bundle, AC-3.3.2 local lifecycle | `internal/shard/eviction_apply_test.go`; `internal/shard/read_lifecycle_test.go`; `internal/shard/find_documents_test.go`; `internal/localblock/transitions_test.go`; `internal/localblock/lifecycle_test.go` | Story 3.3 working tree | PASS | New Shard test proves apply with validation sampling disabled writes the eviction marker, unlinks `.blk`, keeps `.idx`, preserves committed Confirmed Upload authority and empty pending upload state, and allows `HeadDocument`/`FindDocuments` while the Block remains evicted. Existing tests prove metadata reads do not call Backend discovery and validation sampling restores through the normal path when enabled. |
| AC-3.3.3 | Ineligible eviction fails closed and operator output is non-sensitive. | AC-3.3.3 JSON redaction, AC-3.3.3 scrapctl redaction, AC-3.3.3 shard fail-closed, AC-3.3.3 admin mapping, AC-3.3.3 CLI failure output, AC-3.3.3 focused leak scan, Review redaction coverage | `internal/eviction/types.go`; `internal/eviction/planner_test.go`; `internal/eviction/redaction.go`; `internal/eviction/redaction_test.go`; `internal/scrapctl/eviction.go`; `internal/scrapctl/eviction_test.go`; `internal/shard/eviction_apply_test.go`; `internal/admin/eviction.go`; `internal/admin/eviction_test.go` | Story 3.3 working tree | PASS | `BackendKey` remains available in process but is omitted from JSON and human output. Fail-closed cases return bounded skip/failure reasons or mapped HTTP status codes, and sensitive apply/status/HTTP error details are redacted before operator-facing output. Focused scans found no deployed operator-output leak. |

## Pending Evidence

- None for Story 3.3 local policy-gated eviction. Deployed runtime eviction,
  real S3/IAM rehearsal, all-local-copy eviction, and release-readiness evidence
  remain outside this story and are not claimed here.
