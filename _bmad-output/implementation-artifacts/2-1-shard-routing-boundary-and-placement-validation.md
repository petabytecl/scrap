---
baseline_commit: d970de3d0bbec6b6ec260d94e3722774bc3995e4
created_at: "2026-06-11T11:55:47-04:00"
---

# Story 2.1: Shard Routing Boundary and Placement Validation

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want Transaction routing defined by validated Shard placement,
so that every Transaction maps deterministically to one owning Shard.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-5.
- Acceptance IDs: AC-2.1.1, AC-2.1.2, AC-2.1.3.
- Governing sources: `_bmad-output/planning-artifacts/epics.md`, `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`, `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`, `CONTEXT.md`, ADR 0024, ADR 0026.
- GitHub issue: not assigned in the current epic artifact. Before implementation PR, link either a tracker issue or this BMAD story artifact.

## Acceptance Criteria

1. **AC-2.1.1 - Complete slot ownership validation.** Given a valid slot-to-Shard map, when startup validates routing config, then all 1024 fixed hash slots are covered exactly once. Evidence includes the validation command, changed-boundary list, and a route-map summary with Shard IDs and slot ranges only, not raw Transaction identifiers.
2. **AC-2.1.2 - Invalid placement fails before serving.** Given duplicate slot ownership, missing slot ownership, out-of-range slots, or ownership assigned to an unknown Shard ID, when production startup runs, then startup fails closed before public, peer, or admin listeners accept traffic. Evidence proves the failure happens during config/startup validation and not after a serving surface starts.
3. **AC-2.1.3 - Routing telemetry is bounded and redacted.** Given route lookup runs, when telemetry or evidence is emitted, then labels are low-cardinality and do not expose raw `transaction_id`, `document_name`, `tenant_id`, Backend keys, local paths, or Document bytes. Evidence records telemetry/redaction checks.

## Tasks / Subtasks

- [x] Add the routing package as a pure boundary first. (AC: 1, 3)
  - [x] Create `internal/routing` with a package comment that states it owns fixed hash slots, Transaction-to-slot hashing, slot-to-Shard mapping, route lookup, placement validation, and route metadata. It must not own Raft apply, gRPC status mapping, Backend I/O, public request validation, or Shard lifecycle.
  - [x] Add red tests for `SlotCount == 1024`, deterministic `transaction_id -> slot -> shard_id` lookup, route-map summary output, and stable hash test vectors. Use `CONTEXT.md` as the source for `hash(transaction_id) % 1024`.
  - [x] Implement a deterministic standard-library hash for routing. Prefer `hash/fnv` FNV-1a 64 unless an accepted ADR already specifies another hash. Do not use `hash/maphash`; its seed is process-local and cannot define stable placement. Do not promote `github.com/cespare/xxhash/v2` from indirect to direct dependency without ADR-level approval.
  - [x] Keep routing inputs to `transaction_id` and placement config. Do not include `tenant_id` in storage identity or route identity.
  - [x] Return routing/domain errors from `internal/routing`; do not import `grpc/status` or `grpc/codes`.
- [x] Validate placement config exhaustively. (AC: 1, 2)
  - [x] Define a small immutable placement model with explicit known Shard IDs and inclusive slot ranges. Copy maps/slices on construction so callers cannot mutate validated placement.
  - [x] Cover valid one-Shard and two-Shard maps in tests. The one-Shard map is for development/test compatibility only; it does not satisfy V2 release-ready multi-Shard evidence.
  - [x] Cover missing slots, duplicate/overlapping ranges, out-of-range slots, reversed ranges, empty Shard set, and assignments to unknown Shard IDs.
  - [x] Ensure validation errors name only bounded config concepts such as `slot`, `shard_id`, `coverage`, or `overlap`; do not echo raw Transaction IDs, file paths, peer addresses, or secrets.
  - [x] Add a route-map summary helper that reports Shard IDs and slot ranges, sorted deterministically. Do not include Transaction identifiers in summaries.
- [x] Add the minimal `internal/cmd` startup gate without composing multiple Shards yet. (AC: 2)
  - [x] Add a production placement config input, preferably `SCRAP_SHARD_PLACEMENT_FILE`, parsed as JSON into the `internal/routing` model. Development/test mode may default to a single Shard ID `0` owning slots `0-1023` to preserve current local behavior.
  - [x] In production mode, missing, unreadable, malformed, or invalid placement config must fail startup before opening Backend connections or public, peer, or admin listeners.
  - [x] Keep the current `appShardID = 0` single-Shard composition isolated as a development/test bridge. Do not claim Story 2.1 starts or serves a multi-Shard Cell; Story 2.2 owns constructing the configured Shard set.
  - [x] Add `internal/cmd` tests proving invalid placement fails before listener creation. Use a config that would otherwise reach listener setup, and assert the returned error is the routing/placement validation error rather than a later listen or subsystem error.
  - [x] Update existing production startup tests to include a minimal valid placement file if production mode now requires one.
- [x] Add bounded routing telemetry or telemetry hook evidence. (AC: 3)
  - [x] Add routing lookup telemetry through a narrow recorder or OTel metrics type. Labels may include bounded outcome/reason and Shard ID. Avoid raw Transaction IDs, Document names, tenant values, peer addresses, Backend keys, local paths, request IDs, trace IDs, and unbounded error strings.
  - [x] Prefer no slot label on per-lookup metrics unless the implementation proves fixed bounded cardinality is acceptable. Slot ranges may appear in route-map summaries and diagnostics.
  - [x] Add tests that route a Transaction containing a distinctive raw string and prove spans, metrics, logs, and route summaries omit that raw value.
  - [x] Preserve existing hashed identifier behavior in `internal/telemetry`; do not add a second identifier hashing scheme unless routing telemetry needs one and tests prove bounded output.
- [x] Preserve existing public API and peer boundaries. (AC: 1-3)
  - [x] Do not change `proto/scrap/v1/document.proto`, `proto/scrap/v1/peer.proto`, or generated files for this story unless an accepted ADR update explicitly requires it.
  - [x] Do not route public `WriteDocument`, `ReadDocument`, `HeadDocument`, or `FindDocuments` handlers through the new router in this story. Story 2.3 owns public API routing by Transaction.
  - [x] Do not change peer authorization behavior beyond sharing validated placement concepts if needed. Story 2.4 owns full wrong-Shard peer RPC denial from placement membership.
  - [x] Preserve Store and core package boundaries: `internal/server` maps transport, `internal/store` defines the Store contract and domain errors, `internal/shard` owns Shard authority, and `internal/routing` owns only routing/placement decisions.
- [x] Record evidence and update story status before review. (AC: 1-3)
  - [x] Add Debug Log References with exact RED and PASS commands.
  - [x] Include a changed-boundary list that clearly separates new `internal/routing`, minimal `internal/cmd` startup validation, and untouched public/peer routing behavior.
  - [x] Include a route-map summary example with Shard IDs and slot ranges only.
  - [x] Include redaction evidence over production diffs and the story artifact.
  - [x] Run at minimum `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/routing ./internal/cmd`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` because this story creates a new production package boundary.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving to review unless a narrower gate is explicitly justified in Debug Log References.
  - [x] Run `git diff --check`.

### Review Findings

- [x] [Review][Patch] Sanitize placement-file read errors so startup output does not expose operator filesystem paths [internal/cmd/routing_config.go:33]
- [x] [Review][Patch] Add production-mode startup coverage for invalid placement before serving surfaces are created [internal/cmd/routing_config_test.go:94]
- [x] [Review][Patch] Prevent production startup from serving hardcoded Shard 0 when validated placement routes slots elsewhere [internal/cmd/app.go:61]
- [x] [Review][Patch] Reject placement JSON with omitted or null range fields instead of silently treating them as zero values [internal/routing/placement.go:38]
- [x] [Review][Patch] Add `internal/routing` to the package-boundary gate claimed by the story evidence [scripts/check-package-boundaries.sh:25]
- [x] [Review][Patch] Remove raw routing fixture values from the story evidence artifact [2-1-shard-routing-boundary-and-placement-validation.md:195]
- [x] [Review][Patch] Make rejected lookup telemetry distinguish "no Shard" from valid Shard 0 [internal/routing/router.go:23]

## Dev Notes

### Current State

- Story 1.1 through Story 1.5 are complete in this working tree, not necessarily committed. Do not revert their changed files or untracked story/test files while implementing Story 2.1.
- Epic 1 evidence rollup is `CONCERNS` only because GitHub issue linkage is not assigned in BMAD artifacts; runtime evidence is `PASS`. Story 2.1 should keep tracker linkage explicit rather than treating local artifacts as release closure by themselves.
- `CONTEXT.md` already fixes the routing shape: `hash(transaction_id) % 1024 -> slot -> Shard`, with fixed 1024 hash slots. The current code does not implement that router.
- The current `scrapd` composition in `internal/cmd/app.go` still hardcodes `const appShardID uint64 = 0` and passes that value into telemetry, peer transport, `shard.Open`, and `peer.WithAuthorizedShards`.
- `internal/peer` already has `WithAuthorizedShards` and denies unauthorized Shard-carrying peer RPCs before Raft routing or replication side effects. Story 2.4 will derive that set from validated placement membership.
- `internal/store.Store` has no route method. Public handlers call a single Store implementation. Story 2.3 will introduce or adapt a Store-compatible routing boundary for public requests.
- There is no `internal/routing` package and no `test/integration/routing` package in the current checkout.
- There is an older untracked `_bmad-output/implementation-artifacts/2-1-openbao-transit-boundary-and-test-only-fake.md` from a superseded Phase 4.5 epic layout. Do not use it as current Epic 2 truth; the current sprint tracker routes to `2-1-shard-routing-boundary-and-placement-validation`.

### Exact Routing Contract

- Fixed slot count is 1024.
- Route identity input is `transaction_id` only. `document_name` is part of Document identity but not Transaction routing. `tenant_id` remains validation-only future scope and must not change storage or route identity.
- A valid placement covers every slot from `0` through `1023` exactly once.
- Unknown Shard ownership means an assignment references a `shard_id` not declared in the placement's known Shard set.
- Duplicate ownership means the same slot is assigned to two or more Shards, including overlapping ranges.
- Missing ownership means at least one slot in `0-1023` has no owning Shard.
- Route lookup returns the owning Shard plus bounded route metadata such as slot and configured range. It must not expose raw Transaction identifiers in errors, logs, metrics, summaries, or evidence.
- Single-Shard development/test placement is compatibility only. It is not enough for V2 release evidence, which must eventually prove at least two Shards.

### Implementation Guardrails

- Keep `internal/routing` deterministic, synchronous, and side-effect free except optional telemetry recording.
- Do not add package-level mutable registries, global route maps, background goroutines, caches, file watchers, or dynamic rebalancing in this story.
- Do not infer Shard ownership from local files, Backend keys, hostnames, peer addresses, certificate presence, or network location.
- Do not add Shard rebalancing, slot transfer, migration, public routing, peer placement authorization, admin status rendering, `scrapctl` diagnostics, Backend upload/restore changes, or release closure evidence here.
- If the implementation needs to change storage format, wire protocol, dependency/runtime choices, security/encryption/auth contracts, or cross-package ownership beyond `internal/routing` plus the startup validation hook, stop and add/update an ADR first.
- Use `log/slog` only for application logging. Do not introduce zap-native application logging.
- Use schema/JSON decoding for any placement file; avoid ad hoc comma parsing for nested slot ranges.
- Config errors should fail fast and name the offending key or file path variable, but public/log/evidence output must not dump full config content if it could include sensitive deployment details.

### Project Structure Notes

- Expected new files:
  - `internal/routing/doc.go` - package boundary documentation.
  - `internal/routing/routing.go` - placement model, validation, route lookup, route-map summary.
  - `internal/routing/errors.go` - bounded domain errors if needed.
  - `internal/routing/telemetry.go` or `metrics.go` - narrow routing telemetry hook if needed.
  - `internal/routing/*_test.go` - deterministic hash, validation, route lookup, summary, and redaction tests.
- Likely touched production files:
  - `internal/cmd/config.go` - parse placement config path or config value.
  - `internal/cmd/app.go` - validate placement before Backend/listener setup and keep current single-Shard bridge isolated.
  - `internal/cmd/app_test.go` or `internal/cmd/config_test.go` - production startup and config parsing tests.
- Avoid touching unless tests prove it is required:
  - `internal/server/server.go` - Story 2.3 owns public request routing.
  - `internal/peer/server.go` - Story 2.4 owns placement-derived peer authorization.
  - `internal/shard/*` - Shard authority should not need changes for pure placement validation.
  - `proto/` and `gen/` - no wire contract change is expected.

### Testing Notes

- Start with `go test ./internal/routing` red tests before production code.
- Use table-driven tests for placement validation because there are many invalid shapes.
- Use direct one-off tests for deterministic hash vectors and startup fail-before-listen behavior.
- Avoid external services, real object storage, network listeners with fixed ports, sleeps, and shared filesystem paths in unit tests.
- For startup failure tests, use `t.TempDir()` placement fixtures and assert validation happens before any listener can be opened. A useful pattern is to set otherwise invalid listener addresses and prove the placement error wins.
- For redaction tests, use distinctive forbidden values such as `tx-route-secret`, `tenant-secret`, `invoice-secret.xml`, `backend/key`, `.blk`, `.idx`, and `/tmp/route-secret`; assert they do not appear in telemetry attributes, route summaries, or error strings.
- For route-map summaries, assert deterministic ordering. Avoid map iteration order leaks.

### Previous Story Intelligence

- Story 1.5 explicitly left multi-Shard routing to Epic 2 and kept storage identity `(transaction_id, document_name)` through restart/rebuild evidence.
- Story 1.4 warned not to add a router, Shard map, slot hash, or public cross-Shard scan inside discovery logic. Story 2.1 is now the correct place to add the router, but Story 2.3 still owns public API handler routing.
- Story 1.4 and Story 1.5 both reinforced that public handlers stay transport-only and Store/core packages do not import gRPC status packages.
- Story 1.4 redaction evidence used manual metric/span checks plus diff leak scans. Reuse that pattern for routing telemetry.
- Story 1.5 review patches tightened all-or-error streaming and stable unavailable error details. Preserve bounded error text for routing placement failures.
- Recent commits show narrow, test-backed boundary changes: `d970de3 fix(security): enforce peer Shard scope`, `4013b66 fix(security): harden public API and deploy controls`, `69ad47f feat(shard): coordinate upload pressure pause ownership`, `954bfda feat(shard): harden upload confirmation replay`, and `e0c72ce feat(shard): add upload outbox event boundary`.

### Technical Research Notes

- Repo-pinned versions remain authority: Go `1.26.4`, gRPC `v1.81.1`, Pebble `v1.1.5`, etcd Raft `v3.6.0`, and OpenTelemetry `v1.44.0`. No dependency upgrade is in scope.
- GitHub code search for a directly adaptable "fixed hash slots shard routing go transaction id" implementation returned no adopted candidate for this repo. Implement the minimal local routing boundary instead of importing a framework.
- `github.com/cespare/xxhash/v2` is present in `go.mod` only as an indirect dependency. Do not promote it to a direct routing dependency without an ADR-level dependency decision.
- Official Go `hash/fnv` docs define FNV-1 and FNV-1a non-cryptographic hash functions in the standard library. This is sufficient for deterministic internal placement if accepted by tests and docs. Source: https://pkg.go.dev/hash/fnv
- Official Go `hash/maphash` docs state seeds are process-local and cannot be serialized or recreated across processes. Do not use `hash/maphash` for placement because SCRAP routing must be stable across Members and restarts. Source: https://pkg.go.dev/hash/maphash

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 and Story 2.1 source acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-5, NFR-1, NFR-4, NFR-5, NFR-7, and multi-Shard evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-2 architecture, planned `internal/routing` package, boundary map, and FR-5 structure mapping.
- `CONTEXT.md` - glossary, Member identity model, 1024 fixed hash slots, and Transaction routing rule.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope policy and placement-derived future authorized Shard set.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard routing/startup release boundary.
- `docs/v2-scope-reconciliation.md` - current-state note that `scrapd` wires Shard ID `0` and multi-Shard routing is required V2 scope.
- `_bmad-output/implementation-artifacts/1-4-transaction-scoped-document-discovery.md` - prior routing-boundary warnings and redaction test patterns.
- `_bmad-output/implementation-artifacts/1-5-core-gateway-restart-and-rebuild-evidence.md` - latest Epic 1 boundary/evidence learnings.
- `_bmad-output/implementation-artifacts/epic-1-evidence-rollup.md` - current Epic 1 runtime evidence status and tracker-linkage concern.
- `docs/go-style-guide.md` - Go design, error, test, metrics, and package conventions.
- `internal/cmd/app.go` - current single-Shard composition and listener startup order.
- `internal/cmd/config.go` - config parsing and validation style.
- `internal/cmd/run.go` - `scrapd` Run/loadConfig/newApp flow.
- `internal/store/store.go` - current Store contract that public routing will later adapt.
- `internal/server/server.go` - public gRPC handlers that must stay unrouted until Story 2.3.
- `internal/peer/server.go` - existing authorized-Shards hook and wrong-Shard denial behavior.
- `internal/telemetry/identity.go` - hashed identifier behavior and raw-local override rules.
- `https://pkg.go.dev/hash/fnv` - official Go FNV hash docs.
- `https://pkg.go.dev/hash/maphash` - official Go maphash seed behavior docs.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/routing ./internal/cmd` -> failed with missing `internal/routing` production package, undefined `Config.ShardPlacementFile`, and undefined `validateStartupRoutingGates`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/routing ./internal/cmd` -> `ok github.com/petabytecl/scrap/internal/routing 0.002s`; `ok github.com/petabytecl/scrap/internal/cmd 0.124s`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` -> `GO="go" scripts/check-package-boundaries.sh`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` -> formatter, package boundaries, buf lint/generate, `git diff --exit-code -- gen`, `golangci-lint run` with `0 issues`, `go test ./...`, `go test -race ./...`, integration tests, and scrapd/scrapctl builds passed.
- PASS: `git diff --check` -> no output.
- REVIEW PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/routing ./internal/cmd` -> `ok github.com/petabytecl/scrap/internal/routing`; `ok github.com/petabytecl/scrap/internal/cmd`.
- REVIEW PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` -> `GO="go" scripts/check-package-boundaries.sh`.
- REVIEW PASS: `git diff --check` -> no output.
- REVIEW PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` -> formatter, package boundaries, buf lint/generate, generated diff check, golangci-lint `0 issues`, `go test ./...`, `go test -race ./...`, integration tests, and scrapd/scrapctl builds passed.

### Completion Notes List

- Added pure `internal/routing` boundary with `SlotCount == 1024`, FNV-1a 64 `transaction_id -> slot` hashing, immutable placement validation, route lookup metadata, deterministic route-map summaries, and bounded lookup recorder records.
- Added `SCRAP_SHARD_PLACEMENT_FILE` JSON startup gate in `internal/cmd`; production requires the file, development/test defaults to Shard `0` owning slots `0-1023`, and validation runs before Backend/listener setup.
- Changed-boundary list: new `internal/routing`; minimal `internal/cmd` config/startup validation; no public API handler routing, peer authorization behavior, proto, generated code, Store, or Shard authority changes.
- Route-map summary example: `0-511:shard=7,512-1023:shard=9`.
- Redaction evidence: `TestRouterRecordsBoundedLookupTelemetry` routes a distinctive forbidden Transaction fixture and asserts lookup telemetry records omit the raw fixture and its recognizable substrings; summaries include only Shard IDs and slot ranges.
- Existing hashed telemetry identifier behavior in `internal/telemetry` was not changed, and no new direct dependency was added.

### File List

- `_bmad-output/implementation-artifacts/2-1-shard-routing-boundary-and-placement-validation.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/cmd/app.go`
- `internal/cmd/config.go`
- `internal/cmd/config_test.go`
- `internal/cmd/routing_config.go`
- `internal/cmd/routing_config_test.go`
- `internal/routing/doc.go`
- `internal/routing/errors.go`
- `internal/routing/hash.go`
- `internal/routing/placement.go`
- `internal/routing/router.go`
- `internal/routing/routing_test.go`

### Change Log

- 2026-06-11: Implemented Story 2.1 Shard routing boundary and placement startup validation; moved status to review.
