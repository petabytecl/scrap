---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
---

# Story 2.2: Multi-Shard Cell Startup Composition

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want `scrapd` to compose multiple Shards from validated config,
so that one Cell can run the required V2 multi-Shard topology.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-4, FR-5.
- Decision gate: DG-2, closed by ADR 0026.
- Prerequisite: Story 2.1 Shard routing boundary and placement validation.

## Acceptance Criteria

1. **AC-2.2.1 - Configured Shard set construction.** Given valid multi-Shard placement, when `scrapd` starts, then `internal/cmd` constructs and wires the configured local Shard set from validated placement/membership config. Evidence identifies the composition boundary and startup verification command.
2. **AC-2.2.2 - Invalid local Member placement fails closed.** Given local Member placement is omitted, null, empty, duplicate, references an unknown Shard, or would make two local Shards share one Shard data directory, when startup runs, then startup fails closed with an actionable, non-sensitive error before Backend, Shard, public listener, peer listener, or admin listener side effects. Evidence proves the error does not leak sensitive peer addresses, certificate material, secret values, raw identifiers, or local filesystem paths.
3. **AC-2.2.3 - Per-Shard lifecycle visibility.** Given at least two Shards are configured, when evidence is collected, then per-Shard lifecycle state is visible without confusing Cell, Member, Shard, or peer identity. Evidence includes redacted per-Shard status output.

## Tasks / Subtasks

- [x] Define the `internal/cmd` startup topology model. (AC: 1, 2)
  - [x] Replace the Story 2.1 single-Shard route-map gate with a startup topology result that includes the validated `routing.Placement`, deterministic route-map summary, and immutable local Shard ID list.
  - [x] Extend the production placement input with explicit local Member membership, preferably a typed `local_shards` field in `SCRAP_SHARD_PLACEMENT_FILE`. Decode with `encoding/json.Decoder`, `DisallowUnknownFields`, and typed structs; do not add ad hoc comma parsing.
  - [x] Validate local membership in `internal/cmd`: present and non-empty in production, not null, no duplicates, every local Shard ID is declared by the validated `routing.Placement`, every local Shard derives a unique data directory, and the configured topology used for V2 evidence contains at least two Shards.
  - [x] Preserve the Story 2.1 development/test fallback only outside production: missing placement may still default to Shard `0` owning slots `0-1023`, but production must use explicit placement and local membership.
- [x] Add a Shard set composition owner in `internal/cmd`. (AC: 1, 3)
  - [x] Introduce a small private Shard set type or helper file, such as `internal/cmd/shard_set.go`, that owns `map[uint64]*shard.Shard`, ordered Shard IDs, bounded status snapshots, and reverse-order close.
  - [x] Derive isolated per-Shard data directories for multi-Shard mode. Current `shard.Open` creates `blocks`, `pebble`, `openlog`, and `raft` under its `DataDir`; never open two local Shards against the same directory.
  - [x] Keep existing single-Shard development/test data layout compatible unless the story implementation includes explicit migration tests and an accepted ADR. Multi-Shard production/test fixtures may use a new per-Shard subdirectory layout.
  - [x] Move `appShardID` usage behind the single-Shard fallback only. Production multi-Shard startup must not route telemetry, peer scope, public serving, health, or admin behavior through a hardcoded Shard `0`.
  - [x] On partial startup failure, close all previously opened Shards, peer transports, clients, listeners, and telemetry resources in deterministic reverse order.
- [x] Wire each local Shard without expanding later Story 2 behavior. (AC: 1)
  - [x] For each local Shard, call `transport.ForShard(shardID, peers)` and `shard.Open` with that Shard ID, the shared resolved Raft peer map, shared upload Backend config, and per-Shard telemetry/metric bundles.
  - [x] Adapt peer Raft routing, peer replication sinks, and `TransferBlock` local Block sources to dispatch by request `shard_id` into the local Shard set. Reuse `peer.RaftRouter`; add only narrow adapters needed for Shard set dispatch.
  - [x] Pass the configured local Shard IDs to `peer.WithAuthorizedShards`. Do not broaden peer authorization policy or wrong-Shard evidence here; Story 2.4 owns full placement-derived peer RPC authorization and denial coverage.
  - [x] Do not register public Document handlers against an arbitrary default Shard in multi-Shard mode. Until Story 2.3 adds Store-compatible Transaction routing, multi-Shard public Document requests must fail closed rather than fall back to Shard `0` or the first local Shard.
  - [x] Do not register health against one arbitrary Shard in multi-Shard mode. Use a minimal aggregate health checker over the local Shard set or return a bounded fail-closed health state until the Shard set is fully composed.
  - [x] Avoid making admin HTTP or `scrapctl` diagnostics look complete in this story. If a current admin operation cannot safely target multiple Shards, return a bounded fail-closed status or leave it on the single-Shard fallback; Story 2.5 owns operator-facing Shard-aware diagnostics.
- [x] Add per-Shard lifecycle evidence without leaking sensitive data. (AC: 2, 3)
  - [x] Add a bounded startup status helper or log payload in `internal/cmd` that reports Shard IDs, local/remote membership category, route-map ranges, open/closed state, and bounded failure categories.
  - [x] Do not include raw Transaction IDs, Document names, tenant values, peer addresses, Backend keys, local filesystem paths, request IDs, trace IDs, cert/key material, Transit tokens, or unbounded error strings in status, logs, metrics, or story evidence.
  - [x] If telemetry resources currently assume a single `scrap.shard_id`, do not let multi-Shard process-level telemetry pretend Shard `0` represents the whole process. Use bounded per-Shard metric attributes or an aggregate provider, and document the choice in evidence.
- [x] Update targeted tests. (AC: 1, 2, 3)
  - [x] Add `internal/cmd` tests for a valid two-Shard placement/local membership file proving `newApp` builds both Shards with distinct data directories and does not reject non-zero Shard IDs.
  - [x] Add `internal/cmd` tests for missing production membership, null membership, empty membership, duplicate local Shards, unknown local Shards, malformed JSON, and directory-collision guardrails. These tests should prove placement/composition errors win before Backend, Shard, or listener setup.
  - [x] Add cleanup coverage proving a later Shard-open failure closes earlier opened Shards and does not leave partially served public, peer, or admin surfaces.
  - [x] Add peer dispatch unit tests only for composition-owned adapters: Shard ID A reaches Shard A, Shard ID B reaches Shard B, and unknown Shard IDs fail closed with bounded errors. Leave full wrong-Shard authorization/audit side-effect proof to Story 2.4.
  - [x] Update existing single-Shard `internal/cmd` tests so Story 2.1 development/test fallback behavior remains intentional and isolated.
- [x] Record evidence in this story before review. (AC: 1, 2, 3)
  - [x] Include changed-boundary notes separating `internal/cmd` Shard set composition, reused `internal/routing` validation, narrow peer dispatch adapters, and explicitly deferred public routing/admin diagnostics.
  - [x] Include a redacted two-Shard status example with Shard IDs and slot ranges only.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/routing ./internal/peer`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving the story to review.

### Review Findings

- [x] [Review][Patch] Remove raw addresses and `data_dir` from multi-Shard startup evidence/logs [internal/cmd/app.go:428]
- [x] [Review][Patch] Allow production Members that host one local Shard when the placement configures multiple Shards [internal/cmd/routing_config.go:85]
- [x] [Review][Patch] Move and strengthen local Shard data-directory collision validation before Backend setup, including symlink/real-path collisions [internal/cmd/shard_set.go:162]
- [x] [Review][Patch] Include bounded closed/failure categories in startup Shard status instead of only successful `open`/`not_local` states [internal/cmd/shard_set.go:76]
- [x] [Review][Patch] Add per-Shard metric attributes or an aggregate provider so multi-Shard metrics are not indistinguishable under `scrap.shard_id=multi` [internal/cmd/shard_set.go:151]
- [x] [Review][Patch] Preserve close errors when partial Shard startup cleanup fails [internal/cmd/shard_set.go:103]
- [x] [Review][Patch] Exercise fail-closed public Store methods in tests so `shard_routing_pending` behavior is proven [internal/cmd/app_test.go:88]
- [x] [Review][Patch] Preserve high Shard IDs in per-Shard metric attributes without `int64` collapse [internal/cmd/telemetry.go]
- [x] [Review][Patch] Reject broken symlinks that point multiple local Shards at the same target directory [internal/cmd/shard_set.go]
- [x] [Review][Patch] Keep cleanup-failed Shards visible in startup status and preserve close/unregister rollback errors [internal/cmd/shard_set.go]

## Dev Notes

### Current State

- Story 2.1 is complete in this working tree and added `internal/routing` with `SlotCount == 1024`, deterministic FNV-1a Transaction-to-slot hashing, immutable placement validation, route lookup metadata, route-map summaries, and bounded lookup telemetry. Reuse it; do not duplicate routing logic in `internal/cmd`.
- `internal/cmd/routing_config.go` currently loads `SCRAP_SHARD_PLACEMENT_FILE` and validates placement, but `validateStartupRouteMap` rejects any placement that does not route every slot to `appShardID == 0`. Story 2.2 should replace that bridge with Shard set composition.
- `internal/cmd/app.go` still declares `const appShardID uint64 = 0`, opens one `shard.Shard`, registers public and health services against that Shard, builds one peer server around that Shard, and logs one route map. This is the main Story 2.2 handoff.
- `internal/peer` already exposes `WithAuthorizedShards` and `RaftRouter`; it can deny unauthorized Shard-carrying peer RPCs before side effects. Story 2.2 should pass the local Shard set and add composition dispatch adapters only where needed.
- `shard.Open` assumes its `Config.DataDir` is one Shard's root and creates `blocks`, `pebble`, `openlog`, and `raft` below that root. Opening multiple Shards with the same `DataDir` would collide local files and must be rejected or avoided.
- The working tree contains many earlier BMAD story files and source changes. Do not revert unrelated files while implementing Story 2.2.

### Scope Boundaries

- Do not implement public API Transaction routing here. Story 2.3 owns routing write/read/head/find through the Store-compatible boundary.
- Do not implement full wrong-Shard peer authorization evidence here. Story 2.4 owns placement-derived peer RPC authorization, denial audit, stale membership, and side-effect proof.
- Do not implement operator-facing Shard-aware admin and `scrapctl` diagnostics here. Story 2.5 owns that surface.
- Do not add Shard rebalancing, slot transfer, migration, Cell federation, tenant routing, Backend upload/restore behavior changes, proto/wire changes, generated-code edits, or release-closure evidence here.
- If implementation needs to change storage format, wire protocol, dependency/runtime choices, security/encryption/auth contracts, or cross-package ownership beyond `internal/cmd` composition plus narrow peer adapters, stop and add/update an ADR first.

### Implementation Guidance

- Keep `internal/routing` pure: fixed hash slots, placement validation, route lookup, route summaries, and low-cardinality routing telemetry only.
- Keep `internal/cmd` as the composition root: config validation, Shard set construction, process lifecycle, server wiring, and startup status.
- Prefer immutable maps/slices for topology and Shard ID lists. Copy caller-provided slices/maps before storing them in long-lived structs.
- Sort Shard IDs before opening Shards, logging status, building status snapshots, and closing resources so evidence and tests are deterministic.
- Use `filepath.Join` for any per-Shard path construction. Never include computed local paths in returned startup errors or evidence artifacts.
- Use `log/slog` for application logs. Keep log attributes bounded and low-cardinality.
- No new third-party dependency is expected for this story. Use standard library config parsing and the existing repo packages.

### Project Structure Notes

- Likely update:
  - `internal/cmd/routing_config.go` - return startup topology instead of single-Shard route-map-only validation.
  - `internal/cmd/app.go` - replace single `*shard.Shard` fields/wiring with Shard set composition while preserving lifecycle ordering.
  - `internal/cmd/shard_set.go` or another focused `internal/cmd` file - local Shard set, status, dispatch adapters, cleanup helpers.
  - `internal/cmd/routing_config_test.go` and/or `internal/cmd/app_test.go` - valid and invalid multi-Shard startup coverage.
  - `internal/peer/*` only if current peer server APIs cannot accept Shard-set dispatch without broadening peer policy.
- Likely avoid:
  - `internal/routing` beyond small accessors needed by composition; do not add lifecycle or server concepts there.
  - `internal/server` except for a narrow fail-closed adapter or router prerequisite if absolutely required to avoid default-Shard serving. Story 2.3 owns the real public router.
  - `proto/` and `gen/`; no wire change is intended.
  - `internal/shard` unless a missing exported lifecycle/status method is strictly necessary.

### Testing Notes

- Favor `internal/cmd` tests for startup order, config validation, local membership, per-Shard data directory isolation, status redaction, and cleanup.
- Favor focused `internal/peer` unit tests for Shard set dispatch adapters if adapters are added.
- Keep pure placement validation in `internal/routing` tests; do not move placement rules into `internal/cmd`.
- Use `t.TempDir()` placement fixtures and invalid listener addresses to prove placement/composition errors happen before listener creation.
- If tests capture logs/status, scan for forbidden raw values such as temp directory paths, peer addresses, cert/key file paths, distinctive Transaction IDs, Backend keys, and secret-looking tokens.

### Previous Story Intelligence

- Story 2.1 review patches intentionally sanitized placement-file read errors so startup output does not expose operator filesystem paths. Preserve that redaction boundary for local membership and Shard-set composition errors.
- Story 2.1 added coverage proving invalid placement wins before serving-surface setup and preventing hardcoded Shard `0` from serving production placements that route elsewhere. Story 2.2 should change that failure into successful multi-Shard composition for valid multi-Shard placement.
- Story 2.1 rejected omitted or null range fields in placement JSON. Any new local membership JSON fields should get the same strict missing/null/unknown-field treatment where zero values are valid.
- Story 2.1 added `ShardIDValid` to lookup telemetry so rejected lookups cannot be confused with valid Shard `0`; keep the same distinction in multi-Shard startup/status output.
- Story 1.5 and Story 2.1 both preserved `(transaction_id, document_name)` as storage identity. Do not add `tenant_id` to routing or storage identity while implementing startup composition.

### Technical Research Notes

- No new external library or framework research is required for this story. The implementation should reuse repo-local packages and the Go standard library.
- If implementation discovers a need for a new dependency, pause and justify it against the repo's no-new-dependency-without-ADR rule before adding it.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.2 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-4, FR-5, DG-2, NFR-1, NFR-4, NFR-5, NFR-7, and multi-Shard evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-2 architecture, `internal/cmd` Shard set composition, `internal/routing` boundary, peer authorized Shard set, and boundary map.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard startup/routing boundary and implementation guidance.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope policy and placement-derived authorized Shard set.
- `docs/v2-scope-reconciliation.md` - current-state note that `scrapd` wires Shard ID `0` and multi-Shard routing/startup remains required V2 scope.
- `CONTEXT.md` - glossary definitions for Cell, Member, Shard, fixed hash slots, and routing identity.
- `_bmad-output/implementation-artifacts/2-1-shard-routing-boundary-and-placement-validation.md` - previous story implementation notes, review findings, and handoff constraints.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- Research: `gh search repos "multi shard startup go raft shard set" --limit 5` and `gh search code "local_shards DisallowUnknownFields shard placement" --language go --limit 5` returned no reusable implementation to adopt.
- Research: Exa search confirmed the standard library `encoding/json.Decoder` + `DisallowUnknownFields` path was the right no-dependency fit for strict placement config parsing.
- Review follow-up research: `gh search code 'ObserveInt64 WithAttributes language:Go' --limit 5` and Exa search for OpenTelemetry Go observable gauge attributes confirmed callback observations should attach bounded attributes with `metric.WithAttributes`.
- Secondary review pass: blind and edge-case review layers identified metric Shard ID collapse, broken symlink target collisions, startup cleanup status gaps, and metric-registration rollback cleanup gaps; acceptance review found no AC/spec drift.
- Red phase: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd` failed before implementation because `validateStartupTopology`, `app.shards`, and `app.publicStore` were absent.
- Red phase: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/telemetry` failed on the unchecked review findings before patching: production one-local-Shard validation, symlink data-dir collision order, startup status failure categories, Shard metric attributes, and cleanup error preservation.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/routing ./internal/peer` passed.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test ./...` passed.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./internal/shard -run TestWriteDocumentPeerDurabilityQuorumAllowsOnePeerFailure -count=1 -v` passed after observing a race-suite timing failure in that pre-existing helper.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/telemetry` passed after review fixes.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/routing ./internal/peer ./internal/telemetry` passed after review fixes.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build go test ./...` passed after review fixes.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed after review fixes.
- Verification: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after review fixes, including lint, generated-code check, normal tests, race tests, integration-tag tests, and `scrapd`/`scrapctl` builds.

### Completion Notes List

- `internal/cmd` now validates a startup topology before side effects: strict JSON placement decoding, required `local_shards` in production, sorted immutable local Shard IDs, duplicate/unknown/null/empty membership rejection, and non-production single-Shard fallback preservation.
- Added the `internal/cmd` Shard set composition owner. Multi-Shard mode opens each local Shard under `DataDir/shards/shard-{id}`, keeps the old single-Shard fallback layout, and closes opened Shards in deterministic reverse order on partial startup failure.
- Reused `internal/routing` for placement validation and route summaries. No routing lifecycle logic moved into `internal/routing`.
- Added narrow peer dispatch adapters for Raft, replicated document append, and `TransferBlock` block-dir resolution by request Shard ID. Unknown local Shards fail closed with bounded `FAILED_PRECONDITION` errors.
- Public Document serving fails closed in multi-Shard mode until Story 2.3 adds Store-compatible Transaction routing. Existing admin upload/eviction/rewrap/test-hook surfaces remain on the single-Shard fallback only; Story 2.5 still owns operator-facing Shard-aware diagnostics.
- Health now checks the local Shard set instead of one arbitrary Shard. Process-level telemetry uses a bounded `scrap.shard_id=multi` label for multi-Shard composition rather than pretending Shard `0` represents the whole Cell.
- Addressed review findings by redacting raw startup address/path fields, accepting one-local-Shard production Members in multi-Shard placements, validating real per-Shard data-dir collisions before Backend setup, preserving cleanup errors, exposing bounded failure categories in Shard startup status, and adding per-Shard OTel attributes to Raft/disk metrics.
- Addressed secondary review hardening by preserving high Shard IDs as string metric attributes, catching broken symlink target collisions, marking cleanup-failed Shards in startup status, and keeping rollback close/unregister errors retryable and visible.
- Added tests for multi-Shard public Store fail-closed methods returning `shard_routing_pending`.
- Redacted two-Shard status example:
  ```text
  shard_status=[{shard_id:7 membership:local routes:[0-511] state:open} {shard_id:9 membership:local routes:[512-1023] state:open}]
  ```
- `internal/shard/write_ack_test.go` now uses a bounded poll in the openlog-empty helper to wait for asynchronous follower cleanup observed during the full race suite.

### File List

- `_bmad-output/implementation-artifacts/2-2-multi-shard-cell-startup-composition.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/cmd/app.go`
- `internal/cmd/app_test.go`
- `internal/cmd/routing_config.go`
- `internal/cmd/routing_config_test.go`
- `internal/cmd/shard_set.go`
- `internal/cmd/shard_set_test.go`
- `internal/cmd/telemetry.go`
- `internal/cmd/telemetry_test.go`
- `internal/telemetry/attributes.go`
- `internal/telemetry/disk.go`
- `internal/telemetry/disk_test.go`
- `internal/telemetry/raft.go`
- `internal/telemetry/raft_test.go`
- `internal/peer/server.go`
- `internal/peer/transfer.go`
- `internal/shard/write_ack_test.go`
- `internal/store/errors.go`
- `internal/telemetry/resource.go`

## Change Log

- 2026-06-11: Implemented multi-Shard startup composition and validation.
- 2026-06-11: Addressed code review findings - 7 review patch items resolved.
- 2026-06-11: Addressed secondary review cleanup and telemetry hardening findings.
