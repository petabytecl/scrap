---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
created_at: 2026-06-11T11:13:17-04:00
---

# Story 1.5: Core Gateway Restart and Rebuild Evidence

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want core write/read/head/find behavior proven across restart and Projection rebuild,
so that Epic 1 is not closed from happy-path tests alone.

## Traceability

- Epic: Epic 1 - Billing ETL Can Trust Immutable Document Writes and Reads.
- Requirements: FR-1, FR-2, FR-3.
- Acceptance IDs: AC-1.5.1, AC-1.5.2, AC-1.5.3, AC-1.5.4.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0001, ADR 0014, ADR 0016, ADR 0024, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-1.5.1 - Restart preserves public behavior.** Given committed Documents exist, when a Member restarts, then `WriteDocument`, `ReadDocument`, `HeadDocument`, and `FindDocuments` behavior remains correct through a real Shard-backed Store/server path. Evidence links exact restart command output and proves visible Documents remain readable or fail closed according to authority state.
2. **AC-1.5.2 - Rebuild does not trust stale Projection.** Given Pebble Projection state is stale, missing, poisoned, or rebuilt, when rebuild completes, then public behavior follows committed authority and verified local Block metadata/bytes rather than stale Pebble assumptions. Evidence proves Pebble Projection is derived and not storage truth.
3. **AC-1.5.3 - Epic 1 closure rollup.** Given Epic 1 evidence is collected, when closure is evaluated, then ACK, replay/conflict, read/head/find, restart/rebuild, cancellation, and redaction evidence are linked and summarized as `PASS`, `CONCERNS`, or `FAIL` using the V2 release-gate language.
4. **AC-1.5.4 - Shard context survives rebuild.** Given replay/rebuild covers records with Shard context, when Projection state is rebuilt, then storage identity remains `(transaction_id, document_name)` while Shard authority and route context remain recoverable. Evidence records the single-Shard fixture used here and the Epic 2 boundary that will own multi-Shard routing.

## Tasks / Subtasks

- [x] Add characterization tests before production changes. (AC: 1, 2, 4)
  - [x] Add a real Shard restart test for core behavior: write at least two Documents in one Transaction, close the Shard/server, reopen from the same data directory, then assert `HeadDocument`, `ReadDocument`, `FindDocuments`, exact replay, and conflicting duplicate behavior still match Stories 1.1 through 1.4.
  - [x] Prefer registered gRPC evidence through `server.Register` plus a real Shard-backed `store.Store` for at least one restart test. Do not use `internal/spike` as new Epic 1 closure evidence.
  - [x] Add a Projection rebuild test that poisons or removes Pebble-only state, runs `TriggerRebuild`, waits with `WaitRebuild`, and proves stale Projection entries disappear while committed Documents remain readable/listable.
  - [x] Add a fail-closed rebuild case for corrupt or missing visible metadata. Rebuild must not manufacture visibility, drop committed identity silently, or return least-bad bytes.
  - [x] Add public gRPC tests for `store.ErrRebuilding` on `WriteDocument`, `ReadDocument`, `HeadDocument`, and `FindDocuments`; expected status is transient `codes.Unavailable`, not `codes.Internal`.
- [x] Apply only the minimal implementation corrections needed by the tests. (AC: 1, 2, 4)
  - [x] If the gRPC `ErrRebuilding` tests fail, update central server Store error mapping in `internal/server/server.go`; do not special-case individual handlers.
  - [x] If restart/rebuild evidence fails, keep fixes inside the current authority boundaries: `internal/shard`, `internal/index`, `internal/block`, or Shard test helpers. Do not move storage behavior into `internal/server`.
  - [x] Preserve `projectionRebuilder` semantics: rebuild is detached from the triggering RPC, the Shard is unavailable while rebuilding, swap uses a temporary Pebble directory, and failed swap must either restore the previous Projection or keep the Shard degraded instead of serving from nil/poisoned state.
  - [x] Preserve existing Upload Outbox and Confirmed Upload Catalog rebuild behavior. Do not turn Backend inventory, local files alone, audit, or telemetry into Document visibility authority.
- [x] Record Epic 1 evidence rollup. (AC: 3)
  - [x] Add an `Epic 1 Evidence Rollup` section to this story or a focused artifact such as `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`.
  - [x] Link Story 1.1 ACK evidence, Story 1.2 replay/conflict evidence, Story 1.3 read/head/cancellation evidence, Story 1.4 find/redaction evidence, and this story's restart/rebuild evidence.
  - [x] Mark each Epic 1 evidence area `PASS`, `CONCERNS`, or `FAIL`. `PASS` requires current command output; `CONCERNS` requires an explicit owner/follow-up; `FAIL` is required for missing P0 evidence.
  - [x] Include a changed-boundary list and explicitly state that Epic 2 still owns multi-Shard routing by Transaction.
- [x] Preserve privacy and routing evidence. (AC: 1-4)
  - [x] Run or add leak-scan evidence covering new production diffs and evidence artifacts for raw `transaction_id`, `document_name`, local paths, Backend keys, `.blk`, `.idx`, and Document bytes.
  - [x] Do not add raw `transaction_id`, `document_name`, Backend object key, Block ID, local path, trace ID, request ID, or Shard internals to deployed logs, metrics, traces, or public responses.
  - [x] Keep `tenant_id` validation-only for this story. Do not add it to Document identity, Pebble Projection keys, Block `.idx`, Backend keys, telemetry identity, or response metadata.
- [x] Run required verification and record exact results. (AC: 1-4)
  - [x] Run targeted restart/rebuild tests first, for example `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'Restart|Rebuild|Rebuilding'`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` because this story exercises rebuild state, Shard locks, and public transport mapping.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` if Store/server/shard/index boundaries change.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving the story to review unless a narrower gate is explicitly justified in Debug Log References.

### Review Findings

- [x] [Review][Patch] Read streaming can send bytes before handling terminal reader errors [internal/server/server.go:330]
- [x] [Review][Patch] Mid-stream `store.ErrRebuilding` reader errors map to `Internal` instead of transient `Unavailable` [internal/server/server.go:411]
- [x] [Review][Patch] Rebuild unavailable status exposes wrapped internal error text [internal/server/server.go:599]
- [x] [Review][Patch] Restart gRPC test can leak server and Shard resources on early failure [internal/server/restart_rebuild_test.go:85]
- [x] [Review][Patch] Epic 1 rollup top-level status is ambiguous with a `CONCERNS` item present [_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md:7]

## Dev Notes

### Current State

- Story 1.1 through Story 1.4 are complete in this working tree, not necessarily committed. Do not revert their changed files or untracked story/test files while implementing Story 1.5.
- Current sprint tracker routes this story after Story 1.4. Story 1.4 is `done`; Story 1.5 was `backlog` before this story file was created.
- `internal/shard` already exposes `TriggerRebuild(ctx)`, `WaitRebuild()`, `SetRebuildingForTest(bool)`, and `CheckReadiness(ctx)`. Rebuild uses `projectionRebuilder` with a temp Pebble directory and swaps it under Shard lock.
- Existing Shard tests already cover direct `TriggerRebuild` success, preserving committed Projection, rejecting writes/reads while rebuilding, rebuild prepare failure recovery, pending-upload preservation, and a race guard around `DiskStats` during rebuild.
- Existing integration restart evidence in `test/integration/integration_test.go` still uses `internal/spike`. New Story 1.5 evidence should promote the real Shard/server path and avoid treating spike restart as V2 closure evidence.
- Story 1.4 review deferred one Story 1.5-relevant issue: `store.ErrRebuilding` from Shard gates currently falls through central gRPC Store error mapping to `codes.Internal`. Story 1.5 should resolve or explicitly fail this as part of transient rebuild semantics.

### Exact Restart/Rebuild Contract

- A Member restart with the same data directory and same Shard identity must preserve committed Document behavior. Reopened public behavior must prove `HeadDocument`, `ReadDocument`, `FindDocuments`, exact replay, and conflicting duplicate rejection.
- Pebble Projection is derived. If Pebble is deleted, stale, or poisoned, the system must not treat stale Projection entries as storage truth. Rebuild must reconstruct or fail closed from committed authority and verified Block metadata/bytes.
- Client reads remain all-or-error. `ReadDocument` must verify Block/Frame and Document bytes before returning a reader; no prefix, partial, or least-bad bytes may be returned.
- Metadata-only reads (`HeadDocument`, `FindDocuments`) may use retained local `.idx` plus strict Projection Resolution, but must fail closed when visible metadata cannot be resolved.
- During rebuild, public DocumentService methods should report a transient unavailable condition, not `INTERNAL`, and must not expose filesystem paths, Backend keys, Block IDs, or dependency error strings.
- Rebuild is a local Shard authority operation. It must not add multi-Shard routing, route caches, cross-Shard scans, new Shard maps, or `tenant_id` storage identity. Story 2.3 owns public routing by Transaction.

### Implementation Guardrails

- Keep public transport behavior in `internal/server` limited to validation, authz, Store invocation, telemetry/audit, and central Store error mapping.
- Keep rebuild, restart, Projection swap, and visibility authority in `internal/shard` / `internal/index` / `internal/block`.
- Do not import `grpc/status` or `grpc/codes` into Store/core packages. Store/core packages return domain errors; server maps them.
- Do not duplicate Block index parsing outside `internal/block` or Projection Resolution helpers.
- Do not rebuild from Backend inventory, Backend HEAD/LIST, local file existence alone, audit, telemetry, or evidence artifacts. Backend access belongs to explicit upload/restore workflows.
- Do not change public proto, peer proto, storage format, Pebble key schema, Raft command shape, or package ownership without an ADR first.
- Preserve `log/slog` as the application logging API. Do not introduce zap-native application logging.
- Keep tests deterministic: use `t.TempDir`, explicit close/reopen, `WaitRebuild`, contexts, and bounded polling with clear failure messages. Avoid sleeps as synchronization except where existing helper patterns already require bounded polling.

### Project Structure Notes

- Likely touched production files:
  - `internal/server/server.go` - if central Store error mapping needs `store.ErrRebuilding -> codes.Unavailable`.
  - `internal/store/errors.go` - only if a bounded unavailable reason constant is needed for rebuild status details.
  - `internal/shard/shard.go` or `internal/shard/projection_rebuilder.go` - only if restart/rebuild characterization reveals an authority bug.
  - `internal/index/*` or `internal/block/*` - only if strict Projection Resolution or Block verification fails under rebuild evidence.
- Likely touched tests:
  - `internal/shard/shard_test.go` or a new focused `internal/shard/restart_rebuild_test.go` for direct Shard restart/rebuild evidence.
  - `internal/server/*_test.go` for public gRPC `ErrRebuilding` mapping and real Shard-backed restart behavior.
  - `test/integration/integration_test.go` only if promoting real Shard/server restart coverage belongs at integration level. Do not extend `internal/spike` for this story.
- Likely touched artifacts:
  - `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md`
  - `_bmad-output/implementation-artifacts/sprint-status.yaml`
  - optional `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`

### Testing Notes

- Reuse existing helpers before adding new ones: `openTestShard`, `openUploadTestShard`, `openUploadTestShardInDir`, `waitForLeader`, `startServerWith`, `startReadVerificationShardServer`, `writeDocument`, `assertReadDocumentContent`, `assertHeadDocumentSize`, `assertFindDocumentCount`, `triggerRebuildAndWait`, and `CorruptProjectionForTest`.
- For restart tests, avoid a hidden in-memory path: close gRPC connections/server, close the Shard, reopen from the same `DataDir`, and assert through a fresh client or fresh Shard handle.
- For rebuild-not-stale evidence, create a committed Document plus a Pebble-only poisoned entry, trigger rebuild, and assert the poisoned entry no longer drives public behavior.
- For fail-closed evidence, corrupt `.idx` or remove required metadata in a temp dir. Do not mutate checked-in fixtures or shared paths.
- For public gRPC mapping, fake Stores are acceptable for central mapping tests, but at least one restart/rebuild behavior test should use a real Shard-backed Store.
- For closure rollup, do not mark Epic 1 `PASS` from old notes alone. Link exact commands from Story 1.1 through Story 1.5 and note any missing current evidence as `CONCERNS` or `FAIL`.

### Previous Story Intelligence

- Story 1.4 added real Shard and registered gRPC `FindDocuments` evidence, invalid lookup before Store side-effect proof, context cancellation/deadline preservation, and Backend non-authority checks for discovery.
- Story 1.4 kept `FindDocuments` transport-only in `internal/server` and kept authority in `internal/shard` plus strict Projection Resolution. Story 1.5 should preserve that boundary while adding restart/rebuild evidence.
- Story 1.4 review found the `ErrRebuilding` public mapping gap and deferred it as pre-existing. Story 1.5 is the natural place to resolve it because rebuild status is core acceptance.
- Story 1.3 established all-or-error read/head behavior and context cancellation/deadline preservation. Restart/rebuild tests must not weaken those semantics.
- Story 1.2 established exact replay vs conflicting duplicate behavior. Restart/rebuild tests must reassert those outcomes after reopen/rebuild.
- Story 1.1 established ACK only after required local/peer durability and visibility. Restart evidence should prove reopened state reflects the same committed visibility boundary.

### Git Intelligence

- Recent commits show narrow, test-backed Shard/security changes: `d970de3 fix(security): enforce peer Shard scope`, `4013b66 fix(security): harden public API and deploy controls`, `69ad47f feat(shard): coordinate upload pressure pause ownership`, `954bfda feat(shard): harden upload confirmation replay`, and `e0c72ce feat(shard): add upload outbox event boundary`.
- Local pattern remains characterization test first, minimal production correction, central Store error mapping, exact Debug Log References, and broad gates before review.

### Technical Research Notes

- Repo-pinned versions remain authority: Go `1.26.4`, gRPC `v1.81.1`, Pebble `v1.1.5`, etcd Raft `v3.6.0`, and OpenTelemetry `v1.44.0`. No dependency upgrade or registry search is in scope for this story.
- Official Go `context` docs define Context as carrying deadlines and cancellation across API boundaries and requiring propagation through call chains. Source: https://pkg.go.dev/context
- Official gRPC-Go `status` docs define status errors as serialized gRPC errors with optional Details and expected handler/client behavior. Source: https://pkg.go.dev/google.golang.org/grpc/status
- Official Pebble docs describe Pebble as an ordered key-value store and note DB close/iterator safety constraints. Source: https://pkg.go.dev/github.com/cockroachdb/pebble
- Official etcd Raft docs describe raft messages and state machine application through committed entries; keep SCRAP's Raft replay/apply behavior aligned with repo-owned `internal/raft`. Source: https://pkg.go.dev/go.etcd.io/raft/v3

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 1 and Story 1.5 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-1 through FR-3, NFR-1 through NFR-7, and evidence risk model.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - V2 release-scope architecture, FR implications, and future multi-Shard boundary.
- `_bmad-output/implementation-artifacts/1-1-durable-document-write-ack.md` - ACK durability evidence and previous implementation file patterns.
- `_bmad-output/implementation-artifacts/1-2-immutable-replay-and-conflict-handling.md` - replay/conflict behavior and evidence.
- `_bmad-output/implementation-artifacts/1-3-verified-read-and-metadata-inspection.md` - read/head all-or-error and cancellation evidence.
- `_bmad-output/implementation-artifacts/1-4-transaction-scoped-document-discovery.md` - find/discovery evidence and review learnings.
- `_bmad-output/implementation-artifacts/deferred-work.md` - deferred `ErrRebuilding` mapping issue from Story 1.4 review.
- `CONTEXT.md` - glossary, write state machine, Projection rebuild invariant, restart identity model, and peer rebuild boundary.
- `docs/adr/0001-bytes-separate-from-raft.md` - Document bytes stay out of Raft.
- `docs/adr/0014-projection-resolution-boundary.md` - strict client Projection Resolution vs lenient recovery/replay path.
- `docs/adr/0016-phase-4-partial-eviction-boundary.md` - metadata-only reads, restore boundary, and restart classification.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - restart-based rotation and peer identity/scope boundary.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - future routing boundary and no hidden cross-Shard scan in Epic 1.
- `docs/go-style-guide.md` - Go style, errors, tests, concurrency, telemetry, and package conventions.
- `internal/store/errors.go` - Store domain sentinels including `ErrRebuilding`.
- `internal/server/server.go` - public gRPC transport and central Store error mapping.
- `internal/shard/shard.go` - Shard lifecycle, restart, read/write gates, and rebuild integration.
- `internal/shard/projection_rebuilder.go` - Projection rebuild workflow and swap behavior.
- `internal/shard/projection.go` - Projection apply and duplicate/read helper behavior.
- `internal/shard/shard_test.go` - existing restart, rebuild, and direct Shard helper patterns.
- `internal/shard/projection_rebuilder_test.go` - rebuild unit coverage and fail-closed cases.
- `test/integration/integration_test.go` - existing spike restart evidence that should not be extended as V2 closure evidence.
- `https://pkg.go.dev/context` - official Go context cancellation reference.
- `https://pkg.go.dev/google.golang.org/grpc/status` - official gRPC-Go status API.
- `https://pkg.go.dev/github.com/cockroachdb/pebble` - official Pebble package docs.
- `https://pkg.go.dev/go.etcd.io/raft/v3` - official etcd Raft package docs.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Implementation Plan

- Add restart/rebuild characterization tests first, using real Shard and registered gRPC paths where public behavior is claimed.
- Patch only the proven gaps, with special attention to central `ErrRebuilding` transport mapping and avoiding any new storage or routing authority.
- Record an Epic 1 evidence rollup with exact command outputs and release-gate status language before moving to review.

### Debug Log References

- FAIL (RED): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'Rebuilding'` - public Write/Head/Read/Find rebuild-in-progress errors returned `Internal`, wanted `Unavailable`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'Restart|Rebuild|Rebuilding'` - focused restart, rebuild, and public rebuilding-status coverage passed after implementation.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard` - one combined run reported `internal/shard` FAIL without an extracted race/failure detail in the truncated Raft log output; isolated `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -count=1` passed, and the final combined rerun passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS: `ruby -rshellwords -e 'needles = {"raw grpc restart tx" => "tx-" + "grpc-" + "restart", "raw shard restart tx" => "tx-" + "shard-" + "restart", "raw rebuild valid tx" => "tx-" + "rebuild-" + "valid", "raw rebuild stale tx" => "tx-" + "rebuild-" + "stale", "raw rebuild corrupt tx" => "tx-" + "rebuild-" + "corrupt", "raw rebuilding tx" => "tx-" + "rebuilding", "raw document name" => "doc" + ".xml", "restart payload" => "restart" + " evidence" + " payload", "backend key marker" => "Backend" + " key", "local shard path" => "local" + "/" + "shards", "tmp path" => "/" + "tmp" + "/", "block suffix" => "." + "blk", "index suffix" => "." + "idx"}; files = ["internal/server/server.go", "internal/store/errors.go", "_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md"]; data = `git diff -- #{files.shelljoin}`; hits = needles.select { |_name, needle| data.include?(needle) }; abort("leak scan failed: #{hits.keys.join(", ")}") unless hits.empty?; puts "leak scan PASS"'`.
- FAIL then PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` - first run failed on lint shape (`cyclop`, `unparam`, `wrapcheck`); final run passed lint, package boundaries, proto checks, `go test ./...`, `go test -race ./...`, integration tests, and command builds.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'ReadDocumentReaderPartialDataLoss|ReadDocumentReaderRebuilding|DocumentServiceMapsRebuilding'`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/shard ./internal/server -run 'Restart|Rebuild|Rebuilding'`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/server ./internal/shard`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/server ./internal/shard`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
- PASS (review patches): `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Added direct Shard restart evidence that writes two Documents in one Transaction, closes and reopens the same Shard data directory, then proves `HeadDocument`, `ReadDocument`, `FindDocuments`, exact replay, and conflicting duplicate behavior still match Stories 1.1 through 1.4.
- Added registered gRPC restart evidence through `server.Register` and a real Shard-backed `store.Store`; no new Epic 1 closure evidence uses `internal/spike`.
- Added Projection rebuild evidence that poisons Pebble-only state, runs `TriggerRebuild`, waits with `WaitRebuild`, and proves stale Projection entries disappear while committed Documents remain readable and listable.
- Added fail-closed rebuild evidence for corrupt visible metadata: rebuild does not manufacture visibility or serve least-bad bytes, and public read/head/find authority remains strict.
- Fixed the deferred Story 1.4 rebuild-status gap centrally: `store.ErrRebuilding` now maps to `codes.Unavailable` with stable ErrorInfo reason through `internal/server/server.go`.
- Changed-boundary list: `internal/store` gained one unavailable reason constant, `internal/server` central Store mapping changed, and new `internal/server` plus `internal/shard` tests carry restart/rebuild evidence. No proto, storage format, Raft command, Block format, Shard routing, or package ownership boundary changed.
- Epic 1 evidence rollup created at `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` with PASS/CONCERNS/FAIL status for Stories 1.1 through 1.5.
- Routing-boundary proof: storage identity remains `(transaction_id, document_name)`, `tenant_id` remains validation-only, and Epic 2 still owns multi-Shard routing by Transaction.
- Redaction proof: scoped leak scan passed over new production diffs and the rollup artifact; no new deployed logs, metrics, traces, or public responses include raw Document identity, Backend object keys, local paths, request IDs, trace IDs, or Shard internals.
- Code review patches prevent partial chunk sends on terminal reader errors, preserve mid-stream rebuilding errors as `Unavailable`, stabilize the public rebuild unavailable message, close restart-test resources on early failures, and make the Epic 1 rollup top-level state unambiguous.

### File List

- `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md`
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/server/restart_rebuild_test.go`
- `internal/server/read_cancellation_test.go`
- `internal/server/server.go`
- `internal/shard/restart_rebuild_test.go`
- `internal/store/errors.go`

### Change Log

- 2026-06-11: Created Story 1.5 Core Gateway Restart and Rebuild Evidence context package and marked it ready for development.
- 2026-06-11: Implemented Story 1.5 restart/rebuild evidence, fixed central rebuild status mapping, created Epic 1 rollup, and marked ready for review.
- 2026-06-11: Applied all Story 1.5 code review patches and marked story done.
