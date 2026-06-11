---
baseline_commit: 7c47d0f3ff0066873ee85669e3cbd55ae4b0b5c4
---

# Story 2.3: Public API Routes by Transaction

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a billing service engineer,
I want public Document API calls routed to the owning Shard,
so that write/read/head/find behavior works without hardcoded Shard ID assumptions.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirement: FR-5.
- Decision gate: DG-2, closed by ADR 0026.
- Prerequisites: Story 2.1 Shard routing boundary and placement validation; Story 2.2 multi-Shard Cell startup composition.

## Acceptance Criteria

1. **AC-2.3.1 - Transaction-routed public handlers.** Given a Transaction maps to Shard A, when `WriteDocument`, `ReadDocument`, `HeadDocument`, or `FindDocuments` arrives, then the public server path routes through a Store-compatible routing boundary to Shard A. Evidence proves handlers do not hardcode Shard ID constants.
2. **AC-2.3.2 - Two-Shard read/write coverage.** Given a different Transaction maps to Shard B, when the same public calls arrive, then they route to Shard B without handler-level Shard constants. Evidence covers at least two Shards and both read and write paths.
3. **AC-2.3.3 - Route failure fails closed.** Given route lookup fails, or the owning Shard is not available locally, when a public request is handled, then the request fails safely and does not fall back to local files, Backend inventory, Shard `0`, or the first configured Shard. Evidence records the typed failure and redaction proof.
4. **AC-2.3.4 - Startup prerequisites preserved.** Given placement validation or Shard-set composition has not completed successfully, when public API routing would otherwise start, then public routing fails closed instead of serving through a default Shard. Evidence proves Stories 2.1 and 2.2 remain production routing prerequisites.

## Tasks / Subtasks

- [x] Add a Store-compatible public routing boundary. (AC: 1, 2, 3)
  - [x] Implement a narrow adapter, likely in `internal/cmd`, that satisfies `internal/store.Store`, owns a `routing.Router`, and delegates each Store method to the local Shard selected by `transaction_id`.
  - [x] Reuse `internal/routing.Router.Lookup`; do not duplicate hash, slot, placement, or route-summary logic.
  - [x] Keep `WriteDocument` streaming: select the target Store before delegation and pass the `io.Reader` through without buffering the full Document.
  - [x] Copy maps/slices passed into any long-lived router struct; do not retain mutable caller-owned collections.
- [x] Wire multi-Shard public serving through the router. (AC: 1, 2, 4)
  - [x] Replace `publicStoreForTopology`'s multi-Shard `failClosedPublicStore` path with the routing Store only after `validateStartupTopology` and `openShardSet` succeed.
  - [x] Preserve the existing single-Shard development/test fallback: missing placement outside production still serves through the lone Shard.
  - [x] Preserve fail-closed behavior when the Shard set is nil/empty, placement is absent, the route points to a non-local Shard, or composition did not complete.
  - [x] Keep public gRPC handlers in `internal/server` transport-only if possible; handlers should call the Store contract and should not import `internal/routing` or know Shard IDs unless a minimal transport test hook proves necessary.
- [x] Define bounded route failure semantics. (AC: 3, 4)
  - [x] Map route lookup or local-target failures to typed Store errors, not string matching. Use a bounded `UNAVAILABLE` reason such as the existing `shard_routing_pending` for not-configured routing, or add a narrow low-cardinality reason if route-unavailable needs to be distinguishable.
  - [x] Do not include raw `transaction_id`, `document_name`, `tenant_id`, peer addresses, Backend keys, local paths, request IDs, trace IDs, certificate material, secret values, or unbounded dependency errors in public errors, logs, metrics, spans, or evidence.
  - [x] Shard IDs, slot numbers, route outcomes, and low-cardinality failure reasons may be used in tests/evidence when bounded.
- [x] Keep scope to public Transaction routing only. (AC: 1-4)
  - [x] Do not change proto/wire contracts, generated files, storage identity, Block/Frame layout, Backend object identity, Raft command shape, peer authorization policy, admin diagnostics, `scrapctl`, Shard rebalancing, slot transfer, tenant routing, or release-closure evidence.
  - [x] Do not add `tenant_id` to route identity or storage identity; routing input is `transaction_id` only.
  - [x] Keep `FindDocuments` Transaction-scoped and routed to exactly one Shard.
- [x] Add focused tests and evidence. (AC: 1-4)
  - [x] Add router unit tests with two fake Store targets proving `WriteDocument`, `HeadDocument`, `ReadDocument`, and `FindDocuments` route known Transactions to different Shards.
  - [x] Add fail-closed tests for empty/invalid Transaction route lookup, non-local Shard target, nil/empty Shard set, and multi-Shard startup before successful composition.
  - [x] Add `internal/cmd` coverage proving `newApp` with a valid two-local-Shard placement registers a routing public Store instead of `failClosedPublicStore`, while a one-local-Shard Member fails safely for Transactions owned by remote Shards.
  - [x] Add or update public server tests only as needed to prove handlers continue to call Store methods without Shard constants.
  - [x] Add redaction tests using distinctive forbidden Transaction/Document fixtures and assert public errors, logs, spans, route telemetry records, and story evidence omit raw identifiers.
  - [x] Record verification in this story: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/server ./internal/routing`, `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`, and `env GOCACHE=/tmp/scrap-v2-go-build make check` before review.

## Dev Notes

### Current State

- Story 2.1 added `internal/routing` with `SlotCount == 1024`, deterministic FNV-1a `transaction_id -> slot` hashing, immutable placement validation, `routing.Router.Lookup`, route-map summaries, and bounded lookup telemetry. Reuse this package; do not create a second router.
- Story 2.2 added `internal/cmd` Shard set composition. `openShardSet` opens local Shards under per-Shard directories for multi-Shard mode, preserves the old single-Shard fallback layout, dispatches peer Raft/replication/TransferBlock by request Shard ID, and exposes bounded startup status.
- `internal/server.Register` accepts one `store.Store`. The public gRPC handlers validate request metadata, add hashed identifier span attributes, and call Store methods; they currently have no Shard-routing concept.
- `internal/cmd/publicStoreForTopology` currently returns the single local Shard only for the single-Shard fallback and returns `failClosedPublicStore` for multi-Shard mode. `failClosedPublicStore` returns `store.NewUnavailable(store.UnavailableReasonShardRoutingPending, "multi-Shard public routing is not configured")` for all Store methods.
- `internal/store.Store` has exactly four methods matching the public Document API: write, head, read, and find. A Store-compatible router can satisfy the current contract without a proto change.
- `internal/routing.Router.Lookup` records bounded lookup outcomes and never records raw Transaction IDs. It returns `routing.ErrInvalidTransaction` for invalid routing input and `routing.ErrRouteNotFound` for uncovered slots.

### Implementation Guidance

- Preferred shape: keep `internal/server` unchanged and add a private `internal/cmd` Store adapter over `routing.Router` plus `map[uint64]store.Store`. That keeps `internal/cmd` as composition root and leaves handlers transport-only.
- Route all four public Store calls from the same boundary. Do not special-case `FindDocuments` with a cross-Shard scan; it is Transaction-scoped.
- For `WriteDocument`, route by the validated init `transaction_id` before reading chunks from the request stream. The router must not buffer Document bytes or inspect body content.
- Unknown or non-local owning Shards are not permission to fall back to Shard `0`, the first map entry, local files, Backend objects, peer addresses, or certificate identity. Return a bounded typed unavailable error.
- Avoid exported APIs unless a package boundary needs them. If a helper is only used by `internal/cmd`, keep it private and small.
- Keep logs and metrics bounded. Route outcome, reason, slot, and Shard ID are acceptable bounded values; raw Transaction and Document identifiers are not.

### Project Structure Notes

Likely update:

- `internal/cmd/shard_set.go` or a new focused `internal/cmd/public_store_router.go` - Store-compatible router and multi-Shard `publicStoreForTopology` wiring.
- `internal/cmd/shard_set_test.go` and/or `internal/cmd/app_test.go` - router dispatch, fail-closed startup, and two-Shard application wiring.
- `internal/store/errors.go` - only if a new bounded unavailable reason is needed for route-unavailable versus routing-not-configured.
- `internal/server/*_test.go` - only if handler-level proof is needed; keep handlers Store-oriented.

Likely avoid:

- `internal/routing` unless a small accessor or error helper is truly missing. Do not move Store or Shard lifecycle concepts into routing.
- `internal/server/server.go` unless the current Store interface cannot express the route boundary. If edited, preserve validation, security, rate limiting, audit, hashed telemetry, and central error mapping.
- `internal/shard/*`; Shards already satisfy `store.Store`.
- `internal/peer/*`; Story 2.4 owns full peer Shard-scope authorization evidence.
- `internal/admin/*` and `internal/scrapctl/*`; Story 2.5 owns operator-facing Shard-aware diagnostics.
- `proto/`, `gen/`, storage formats, Backend object keys, and ADR changes; no wire/storage/dependency decision is expected for this story.

### Testing Notes

- Start with red tests for the Store-compatible router: known `tx-alpha` routes to Shard 7 and `tx-bravo` routes to Shard 9 with the current `internal/routing` fixture.
- Add tests for all Store methods because `WriteDocument` uses streaming and `ReadDocument` returns an `io.ReadCloser`; do not prove only metadata paths.
- For route failure tests, assert typed errors with `errors.Is` or `store.UnavailableReason`, not substrings.
- For server tests, existing helpers under `internal/server` already exercise all public RPCs through Store fakes and the spike Store. Reuse them rather than adding a second transport harness.
- Run leak scans on captured evidence/log/status strings that contain distinctive fixture values.

### Previous Story Intelligence

- Story 2.1 review patches sanitized placement-file read errors, rejected omitted/null JSON fields, added `ShardIDValid` to lookup telemetry, and prevented production startup from serving a hardcoded Shard `0`.
- Story 2.2 review patches kept public serving fail-closed in multi-Shard mode, redacted startup address/path fields, accepted one-local-Shard production Members for multi-Shard placements, validated per-Shard directory collisions before Backend setup, preserved startup cleanup errors, and kept Shard-specific admin routes on the single-Shard fallback only.
- Story 2.2 added tests proving multi-Shard public Store methods currently return `shard_routing_pending`. Story 2.3 should replace that placeholder with real routing for locally hosted owning Shards while preserving fail-closed behavior for missing or remote targets.
- Recent commits are narrow and test-backed: `7c47d0f fix: close story 2.2 review gaps`, `ebf8ad8 fix: address story 2.2 review findings`, `0b87e7f docs: record story 2.2 review findings`, `f6db964 feat: add v2 storage and multi-shard story work`, and `d970de3 fix(security): enforce peer Shard scope (#434)`.

### Technical Research Notes

- GitHub code search for a directly reusable fixed-slot Transaction-to-Shard Store router did not return a candidate to adopt for this repo.
- Exa research against the official Go package docs confirms `hash/fnv` remains the standard-library package for FNV-1/FNV-1a hash functions. Story 2.1 already committed to FNV-1a in `internal/routing`; keep using that implementation rather than adding a dependency. Source: https://pkg.go.dev/hash/fnv
- `go.mod` already lists `github.com/cespare/xxhash/v2` only as an indirect dependency. Do not promote it to direct routing use without an ADR-level dependency decision.
- No new third-party dependency is expected for this story.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.3 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-5 multi-Shard startup/routing and release evidence expectations.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-2 architecture, Store-compatible public router, package boundary map, and FR-5 structure mapping.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard routing/startup boundary and implementation guidance.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope policy and placement-derived authorized Shard set.
- `CONTEXT.md` - glossary definitions, Document identity, fixed hash slots, and Transaction-scoped `FindDocuments`.
- `_bmad-output/project-context.md` - agent rules for package boundaries, testing, telemetry redaction, and commit safety.
- `_bmad-output/implementation-artifacts/2-1-shard-routing-boundary-and-placement-validation.md` - routing package, previous review findings, and handoff constraints.
- `_bmad-output/implementation-artifacts/2-2-multi-shard-cell-startup-composition.md` - current Shard set composition, fail-closed public Store placeholder, review findings, and completion notes.
- `internal/routing/*` - current route lookup, placement validation, route-map summary, and bounded lookup telemetry.
- `internal/cmd/app.go`, `internal/cmd/shard_set.go`, `internal/cmd/routing_config.go` - current startup topology, Shard set composition, and public Store wiring.
- `internal/server/server.go`, `internal/server/telemetry.go` - current public handler behavior, Store boundary, error mapping, and hashed telemetry.
- `internal/store/store.go`, `internal/store/errors.go` - Store contract and typed Store error taxonomy.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestPublicStoreRouter'` failed with undefined `newPublicStoreRouter` and `storeapi.UnavailableReasonShardRouteUnavailable`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestPublicStoreRouter'` passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd` passed.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestNewAppBuildsTwoShardTopology|TestNewAppLeavesShardAdminRoutesDisabledForMultiShardSingleLocalMember'` failed because multi-Shard public serving still used `failClosedPublicStore`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestNewAppBuildsTwoShardTopology|TestNewAppLeavesShardAdminRoutesDisabledForMultiShardSingleLocalMember'` passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestPublicStoreRouter'` passed after wiring.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd` passed after wiring.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestHeadDocumentRouteUnavailableReturnsBoundedErrorInfo'` passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/server` passed.
- SCOPE: `git status --short` and `git diff -- proto gen internal/peer internal/admin internal/scrapctl internal/shard internal/backend` confirmed no proto/generated/peer/admin/scrapctl/shard/backend production changes.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/server ./internal/routing` passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- FORMAT: `env GOCACHE=/tmp/scrap-v2-go-build make check` initially failed at `fmt-check`; applied the formatter-required test signature/literal cleanup.
- LINT: `env GOCACHE=/tmp/scrap-v2-go-build make check` then failed on test cyclomatic complexity and an unused router constructor parameter; split the test and removed the parameter.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after fixes.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added a private Store-compatible public router in `internal/cmd` that selects local Shard Stores through `routing.Router.Lookup`, copies the Shard target map, streams write bodies through unchanged, and returns bounded typed Store errors for invalid or unavailable routes.
- Wired multi-Shard `publicStoreForTopology` to build the Store router from successfully opened local Shards, while preserving the single-Shard fallback and fail-closed behavior for missing local targets.
- Added `shard_route_unavailable` as a bounded Store unavailable reason and covered public gRPC mapping with `ErrorInfo` details that omit raw Transaction and Document identifiers.
- Kept scope to public Transaction routing: no proto, generated code, storage identity, peer authorization, admin diagnostics, `scrapctl`, Shard authority, Backend identity, tenant routing, or release-closure behavior changed.
- Added focused router, startup wiring, fail-closed, and public gRPC route-unavailable tests; final targeted package, package-boundary, and `make check` gates passed.

### File List

- `_bmad-output/implementation-artifacts/2-3-public-api-routes-by-transaction.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/cmd/public_store_router.go`
- `internal/cmd/public_store_router_test.go`
- `internal/cmd/app_test.go`
- `internal/cmd/shard_set.go`
- `internal/server/route_unavailable_test.go`
- `internal/store/errors.go`

## Change Log

- 2026-06-11: Implemented Story 2.3 public Transaction routing and moved status to review.
