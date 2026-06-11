---
baseline_commit: 1abc429d1c51d2ac18c3c4184f5fd2e2ca0ba66f
---

# Story 2.5: Shard-Aware Admin and `scrapctl` Diagnostics

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want admin HTTP and `scrapctl` to show Shard-aware status,
so that I can diagnose routing, leadership, peers, and health per Shard.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirement: FR-5.
- Decision gate: DG-2, closed by ADR 0026.
- Prerequisites: Story 2.1 Shard routing boundary, Story 2.2 multi-Shard Cell startup composition, Story 2.3 public Transaction routing, and Story 2.4 peer RPC Shard-scope authorization.

## Acceptance Criteria

1. **AC-2.5.1 - Admin per-Shard status.** Given a multi-Shard Cell is running, when admin status is requested, then output identifies per-Shard health, leader, peer, and route state. Evidence proves status is read-only and does not mutate Shard authority, local Block files, Backend state, or Raft/Pebble state.
2. **AC-2.5.2 - CLI terminology and boundaries.** Given `scrapctl` queries the Cell, when it renders diagnostics, then it preserves Cell, Member, and Shard terminology exactly. Evidence includes CLI output examples and a changed-boundary list.
3. **AC-2.5.3 - Redacted diagnostic evidence.** Given diagnostic evidence is generated, when admin and CLI outputs are captured, then no raw identifiers, sensitive peer addresses, or secret material leak. Evidence records redaction checks for both surfaces.
4. **AC-2.5.4 - Production diagnostics fail closed.** Given admin or `scrapctl` diagnostics run under production profile, when required auth, TLS, or role policy is missing, then diagnostics fail closed instead of downgrading to a development fallback. Evidence records the denied diagnostic path and redaction proof.

## Tasks / Subtasks

- [x] Add a read-only Shard diagnostics snapshot boundary. (AC: 1, 3)
  - [x] Prefer a narrow provider interface consumed by `internal/admin` and implemented in `internal/cmd` over importing `internal/shard` into `internal/admin` or `internal/scrapctl`.
  - [x] Include bounded Cell/Member fields (`cell_id`, `member_hostname`, `member_id`) and per-Shard entries with `shard_id`, local/remote membership, route ranges, health/readiness, leader state, leader ID, peer count or bounded peer health, upload pressure, eviction/restore health, and bounded failure reason fields where available.
  - [x] Reuse existing `startupTopology`, `routing.Placement.RouteMapSummary`, `shardSet.StartupStatus`, and Shard read-only methods (`CheckReadiness`, `IsLeader`, `LeaderID`, `UploadPressureSnapshot`, `EvictionHealthSnapshot`) where they fit; do not duplicate route-map or leadership logic.
  - [x] Keep the snapshot read-only against Shard authority: no Raft proposals, no public Store calls, no peer RPCs, no rebuild/scrub trigger, no eviction plan/apply call, no rewrap call, no Backend list/probe, and no local Block/openlog/Pebble writes.
  - [x] Sort Shard diagnostics by Shard ID and copy slices/maps before storing them in long-lived structs or returning them from providers.
- [x] Extend admin HTTP status without changing public or peer wire contracts. (AC: 1, 3, 4)
  - [x] Extend `/healthz` or add a clearly named read-only admin status endpoint only if `/healthz` becomes too broad. Prefer preserving `scrapctl status` compatibility by extending the existing JSON shape with optional Shard diagnostics.
  - [x] Protect new admin diagnostics with the existing admin `admin_reader` authorization, audit, and rate-limit path. Do not create an unauthenticated debug endpoint.
  - [x] In production/test security modes, prove missing client auth, missing `admin_reader`, missing TLS material, or HTTP URLs fail closed through the existing security/TLS path and do not fall back to development behavior.
  - [x] Make diagnostic provider failure bounded: mark the affected Shard or aggregate status degraded with a low-cardinality reason instead of returning raw dependency errors, local paths, peer addresses, or certificate details.
- [x] Update `scrapctl` diagnostics rendering. (AC: 2, 3, 4)
  - [x] Extend `internal/scrapctl.Health` or introduce a focused diagnostics type that mirrors the admin JSON and preserves existing `status`, `upload-pressure`, `leader`, and `peers` command behavior unless explicitly superseded by Shard-aware data.
  - [x] For `scrapctl status --output=json`, include Shard diagnostics in machine-readable form without renaming Cell, Member, Shard, Transaction, Document, Block, Backend, or peer terms.
  - [x] For text output, avoid the current `%+v` dump if it becomes unreadable; render stable line-oriented fields that include Cell, Member, Shard, route, leader, peer, health, and pressure labels.
  - [x] Keep `scrapctl` a client/operator path only. It must call admin HTTP or Kubernetes/metrics paths already owned by the CLI; it must not import `internal/shard`, read Shard data directories, inspect Backend keys, or become a storage authority.
  - [x] Preserve `scrapctlTLSRequired`: production mode must require HTTPS plus client TLS configuration for admin/public HTTP calls.
- [x] Add redaction and side-effect evidence. (AC: 1, 3)
  - [x] Capture admin JSON, CLI JSON, CLI text, audit denial output, and representative error strings with distinctive forbidden fixtures.
  - [x] Leak-scan outputs for raw `transaction_id`, `document_name`, idempotency keys, Backend keys, local filesystem paths, sensitive peer addresses, certificate/key material, Transit tokens, raw principal IDs, request IDs, trace IDs, and unbounded dependency errors.
  - [x] Add test doubles proving status requests do not call write-like methods, Raft proposal paths, rebuild/scrub triggers, eviction apply/plan operations, rewrap operations, Backend inventory/listing, or Block writer/file operations.
  - [x] Shard IDs, route ranges, bounded member labels, leader IDs, pressure levels, readiness states, and low-cardinality failure reasons are acceptable evidence fields.
- [x] Keep scope narrow and preserve current contracts. (AC: 1-4)
  - [x] Do not change `proto/`, `gen/`, public `DocumentService`, peer `PeerService`, storage format, Block/Frame layout, Backend object identity, Raft command shape, tenant identity, Shard rebalancing, slot transfer, upload/restore behavior, Content Quarantine, or release-closure evidence.
  - [x] Do not move routing ownership out of `internal/routing`; admin and CLI should consume route metadata from composition/snapshot boundaries.
  - [x] Do not broaden `internal/admin` or `internal/scrapctl` imports into Shard internals. `internal/cmd` remains the composition owner that can adapt Shards to narrow status interfaces.
  - [x] Do not treat metrics-only leader output as sufficient per-Shard status if admin can already supply an authoritative per-Shard snapshot.
- [x] Add focused tests and verification. (AC: 1-4)
  - [x] Add `internal/admin` tests for Shard diagnostics JSON, GET-only/read-only behavior, bounded degraded provider failures, redaction, and production auth/rate-limit/audit gating.
  - [x] Add `internal/scrapctl` tests for `status --output=json`, text rendering, terminology preservation, TLS production fail-closed behavior, and redaction.
  - [x] Add `internal/cmd` tests proving `newApp` wires Shard diagnostics for explicit multi-Shard topology and preserves existing single-Shard fallback behavior.
  - [x] Record verification in this story before review: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd`, `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`, `env GOCACHE=/tmp/scrap-v2-go-build make check`, and an explicit leak-scan command over captured evidence or changed files.

### Review Findings

- [x] [Review][Patch] Remote Shards degraded a healthy local Member [internal/cmd/shard_diagnostics.go] - fixed by degrading the aggregate only for local Shards whose health is not `ok`; remote Shards remain visible as `not_local`.
- [x] [Review][Patch] Remote Shard leadership was ambiguous [internal/cmd/shard_diagnostics.go] - fixed by adding bounded `leader_state` values (`leader`, `follower`, `unknown`, `not_local`) to admin JSON and `scrapctl` text/JSON.
- [x] [Review][Patch] Upload/eviction pressure changed readiness to `not_ready` [internal/cmd/shard_diagnostics.go] - fixed by separating health degradation from readiness degradation; readiness only changes on readiness failures.
- [x] [Review][Patch] Successful diagnostics could echo unbounded labels [internal/admin/shard_diagnostics.go, internal/scrapctl/output.go] - fixed by bounding successful admin snapshots and CLI text fields, including path/address/secret-marker redaction.
- [x] [Review][Patch] Default `scrapctl status` text dropped existing eviction/restore health fields [internal/scrapctl/output.go] - fixed by rendering eviction pressure, lifecycle counts, restore failures, and Shard diagnostics together.
- [x] [Review][Patch] Production fail-closed evidence was too generic [internal/admin/shard_diagnostics_test.go, internal/scrapctl/status_shard_test.go] - fixed with diagnostics-specific admin role/rate-limit tests and a production TLS-before-HTTP `scrapctl status` test.
- [x] [Review][Patch] Read-only side-effect evidence was incomplete [internal/cmd/shard_diagnostics_test.go] - fixed with a fake diagnostics source/target that proves only read-only snapshot methods are reachable and remote Shards do not invoke local Shard methods.
- [x] [Review][Patch] Required diagnostic evidence was under-recorded [this story] - fixed by recording captured output/evidence summaries and the changed-boundary list below.

## Dev Notes

### Current State

- Story 2.2 added `startupTopology`, `shardSet`, per-Shard local directories, bounded startup status, and multi-Shard health checking. It deliberately kept Shard-specific admin operations on the single-Shard fallback only; Story 2.5 is the handoff for operator-facing multi-Shard diagnostics.
- Story 2.3 added public Transaction routing through `internal/cmd/public_store_router.go`. Do not reroute public handlers in this story.
- Story 2.4 added placement-derived peer Shard authorization and bounded denial metrics. Do not change peer policy here.
- `internal/admin.Server` currently serves `/healthz` with security, upload, eviction, rewrap, and production-readiness fields. It already uses `admin_reader` authorization, audit, and rate limiting when configured.
- `internal/cmd.appendShardAdminOptions` currently wires upload/eviction/rewrap/test-hook providers only for `topology.SingleShardFallback`. Multi-Shard admin status currently has no per-Shard provider beyond startup logs.
- `internal/cmd.shardSet.StartupStatus(topology)` already renders bounded Shard ID, membership, route ranges, state, and failure category. It is a good source for route/membership/status shape but does not include live leader or peer health.
- `internal/shard.Shard` exposes read-only methods for leader/readiness/upload/eviction snapshots. Some snapshot methods may update telemetry gauges; they must not mutate Shard authority or files.
- `internal/scrapctl.Run` uses the standard library `flag` package. `status` fetches admin `/healthz`; `upload-pressure` aliases status; `leader` currently parses aggregate metrics; `peers` reads Kubernetes pod readiness. Preserve existing commands unless the Shard-aware status data safely replaces or augments them.
- `internal/scrapctl/tls.go` already requires TLS when `SCRAP_SECURITY_MODE=production` or TLS flags/env vars are present, and rejects non-HTTPS URLs in that mode.

### Implementation Guidance

- Best shape: add status structs and a `ShardDiagnosticsProvider` style interface in `internal/admin`; implement an adapter in `internal/cmd` that combines `Config`, `startupTopology`, `shardSet`, peer map cardinality, and per-Shard read-only snapshot methods.
- Keep the admin JSON backward-compatible: existing `Health` fields should continue decoding in old tests and scripts, with optional Shard diagnostics appended.
- Prefer bounded enum-like strings: `ok`, `degraded`, `open`, `closed`, `local`, `remote`, `not_local`, `leader_unknown`, `readiness_failed`, `snapshot_unavailable`.
- Peer diagnostics should avoid raw peer addresses. If peer health is derived from configured Raft peers, expose counts and bounded states, not full addresses. If a Member name is needed, use bounded Member identity terms and document why it is non-sensitive.
- Production fail-closed evidence should exercise existing controls rather than adding a second security mechanism: `admin.New` authorization tests, `scrapctlTLSRequired`, `withHTTPClientTLS`, and HTTP status handling.
- If implementation discovers a need for a new admin endpoint that changes operator contract semantics, keep it HTTP/admin-only and document why `/healthz` is insufficient. A new gRPC admin service or proto change is out of scope.

### Project Structure Notes

Likely update:

- `internal/admin/server.go` and `internal/admin/server_test.go` - Shard diagnostics response structs, provider option, JSON rendering, provider failure behavior, auth/audit/rate-limit coverage.
- `internal/cmd/app.go` and a focused `internal/cmd/*diagnostics*.go` or `internal/cmd/shard_set.go` helper - app-level adapter from Shard set/topology/config to admin diagnostics provider.
- `internal/cmd/app_test.go` or `internal/cmd/shard_set_test.go` - multi-Shard `newApp` wiring, read-only snapshot evidence, ordering/copying.
- `internal/scrapctl/status.go`, `internal/scrapctl/output.go`, and `internal/scrapctl/*_test.go` - CLI decode/render of Shard diagnostics and production TLS failure coverage.
- `_bmad-output/implementation-artifacts/2-5-shard-aware-admin-and-scrapctl-diagnostics.md` and `sprint-status.yaml` - story status/evidence updates during dev.

Likely avoid:

- `internal/routing/*` except for a missing accessor that is already justified by ADR 0026 route metadata.
- `internal/shard/*` unless an existing read-only method is insufficient and a narrow accessor is required.
- `internal/peer/*`, `internal/server/*`, `proto/`, `gen/`, `internal/backend/*`, storage packages, and ADR files.

### Testing Notes

- Start with red tests in `internal/admin` and `internal/scrapctl` because those are the user-facing surfaces for this story.
- Use `httptest` for admin status and local `http.Client` transports for `scrapctl`; do not require real network listeners or Kubernetes for unit tests.
- Use test doubles that count calls to read-only snapshot methods and panic/fail on mutation-like methods.
- Assert JSON fields by decoding typed structs or maps; use string scans only for redaction/leak checks and CLI text examples.
- Keep fixture output examples stable and small enough to paste into the story completion notes.

### Previous Story Intelligence

- Story 2.1 established `internal/routing` as the only owner of slot count, Transaction hashing, placement validation, route lookup, route summaries, and bounded lookup telemetry.
- Story 2.2 established deterministic Shard-set ordering, per-Shard data directory isolation, bounded startup status, multi-Shard health checking, and the rule that Shard-specific admin operations stay on the single-Shard fallback until Story 2.5.
- Story 2.3 made public API calls route by Transaction and added route-unavailable typed failures plus bounded route telemetry.
- Story 2.4 made peer authorization derive from validated local Shard membership, added bounded authorization-denial metrics, and preserved fail-closed no-Shard scrub/rebuild behavior in explicit multi-Shard placement.
- Recent relevant commits: `1abc429 fix: address story 2.4 review findings`, `7a3a28d test: cover peer shard authorization evidence`, `fe86f0a docs: create story 2.4 peer shard authorization`, `461e536 fix: address story 2.3 review findings`, and `6d7d3b8 feat: route public API by transaction`.

### Technical Research Notes

- GitHub searches for reusable Go admin/Shard status CLI implementations did not surface an implementation to adopt. Results were unrelated cluster status examples plus old `petabytecl/scrap` v1-era `scrapctl` code, so this story should reuse V2 local packages.
- Exa research against official Go `net/http` docs confirms the existing `http.Client` plus `http.NewRequestWithContext` shape remains appropriate for context-bound status calls. Source: https://pkg.go.dev/net/http
- No new CLI, HTTP, JSON, assertion, or mocking dependency is expected. Use the Go standard library and existing repo test patterns.

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.5 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-13 `scrapctl` operational baseline and production operator surface constraints.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-2 architecture, package ownership, admin/scrapctl Shard status requirement, and boundary map.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard release boundary and admin/scrapctl diagnostics requirement.
- `docs/adr/0019-production-security-boundary.md` - production admin role/TLS/audit/rate-limit requirements and non-production visibility.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer identity and Shard-scope policy that diagnostics must not bypass.
- `CONTEXT.md` - glossary definitions for Cell, Member, Shard, Transaction, Document, Block, Backend, and routing identity.
- `_bmad-output/project-context.md` - package boundaries, testing rules, redaction rules, and commit safety.
- `_bmad-output/implementation-artifacts/2-2-multi-shard-cell-startup-composition.md` - current Shard set composition, admin fallback handoff, and startup status evidence.
- `_bmad-output/implementation-artifacts/2-3-public-api-routes-by-transaction.md` - public routing completion and admin/scrapctl scope boundary.
- `_bmad-output/implementation-artifacts/2-4-peer-rpc-shard-scope-authorization.md` - peer authorization completion and bounded denial evidence patterns.
- `internal/admin/server.go`, `internal/admin/server_test.go` - existing admin `/healthz`, auth/audit/rate-limit, upload/eviction/rewrap health patterns.
- `internal/scrapctl/status.go`, `internal/scrapctl/run.go`, `internal/scrapctl/tls.go`, `internal/scrapctl/output.go` - current CLI command, admin HTTP, TLS, and output patterns.
- `internal/cmd/app.go`, `internal/cmd/shard_set.go`, `internal/cmd/routing_config.go` - composition root, startup topology, Shard set, peer map, and admin option wiring.
- `internal/shard/shard.go`, `internal/shard/upload_pressure.go`, `internal/shard/eviction_health.go` - existing read-only Shard snapshot methods.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- CREATE-STORY: `git status --short --branch` confirmed a clean `v2...origin/v2` branch at baseline `1abc429d1c51d2ac18c3c4184f5fd2e2ca0ba66f`.
- RESEARCH: `gh search repos "Go admin health shard status CLI diagnostics" --limit 5` returned no reusable candidates.
- RESEARCH: `gh search code "shard status admin health language:Go" --limit 5` returned unrelated cluster/admin status implementations.
- RESEARCH: `gh search code "scrapctl status language:Go" --limit 5` returned old `petabytecl/scrap` v1-era CLI code, not a V2 implementation to reuse.
- RESEARCH: Exa official Go `net/http` docs lookup confirmed the existing context-bound `http.Client` pattern remains the right no-dependency fit.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd` failed with missing `admin.ShardDiagnostics`, `admin.WithShardDiagnosticsProvider`, dropped CLI `shard_diagnostics`, raw struct text output, and missing app-level `shard_diagnostics`.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd` passed after adding the admin provider, cmd adapter, and CLI rendering.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- LINT: `env GOCACHE=/tmp/scrap-v2-go-build make check` initially failed on test cyclomatic complexity and CLI text-renderer cognitive complexity; split helpers and reran.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd` passed after lint refactor.
- PASS: `git diff --check` passed.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed, including lint, `go test ./...`, race tests, integration-tag tests, and `scrapd`/`scrapctl` builds.
- PASS: Production-code leak scan had no matches for distinctive forbidden fixtures, local paths, Backend key markers, and private-key header markers.
- PASS: Secret-pattern scan over changed story/source/test files had no matches.
- REVIEW: `bmad-code-review` ran Blind Hunter, Edge Case Hunter, and Acceptance Auditor layers against the Story 2.5 diff from baseline `1abc429d1c51d2ac18c3c4184f5fd2e2ca0ba66f`.
- REVIEW-FIX: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd` passed after fixing review findings for remote Shards, leader state, bounded labels, production fail-closed coverage, and text output preservation.
- REVIEW-FIX: `git diff --check` passed.
- REVIEW-FIX: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- REVIEW-FIX: Post-review leak scans over changed story/source/test files had no matches.
- REVIEW-FIX: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after review fixes, including lint, full tests, race tests, integration-tag tests, and `scrapd`/`scrapctl` builds.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added admin Shard diagnostics provider types and extended `/healthz` with optional `shard_diagnostics` while making the endpoint GET-only.
- Added `internal/cmd` Shard diagnostics adapter that combines validated topology, Cell/Member identity, route ranges, local Shard readiness, leader state, peer count, upload pressure, and eviction/restore health without public Store calls, peer RPCs, Raft proposals, Backend probes, or storage writes.
- Extended `scrapctl status` JSON and text output to preserve Cell, Member, and Shard terminology and render bounded Shard diagnostics.
- Added admin, `scrapctl`, and app-level tests for JSON shape, text rendering, GET-only behavior, provider failure redaction, TLS production fail-closed coverage, app wiring, and address/path leak prevention.
- Preserved scope: no proto/generated files, public/peer wire contracts, storage format, Backend identity, routing ownership, or Shard authority behavior changed.
- Review fixes added explicit `leader_state` diagnostics so remote Shards report `not_local`, local followers report `follower`, local leaders report `leader`, and no-leader bootstrap windows report bounded `unknown`/`no_leader` evidence.
- Captured diagnostic examples in tests: admin JSON includes `shard_diagnostics` with Cell/Member/Shard fields; CLI JSON preserves `leader_state`, route, membership, and Shard terms; CLI text includes `Cell:`, `Member:`, `Shard N:`, `EvictionPressure:`, and `RestoreFailuresByReason:`.
- Redaction evidence covers successful and failure paths: `/tmp/secret`, `10.1.2.3:9091`, `backend-key`, `private-key-material`, oversized labels, raw provider errors, and private-key header markers do not appear in admin or CLI text output.
- Production fail-closed evidence covers diagnostics-specific admin missing-role denial (`403` before provider call), admin rate limiting (`429` before provider call), and `scrapctl status` production TLS validation before any HTTP request.
- Changed-boundary list: `internal/admin` adds optional `/healthz` JSON fields only; `internal/cmd` adapts composition-owned Shard snapshots; `internal/scrapctl` decodes/renders admin HTTP diagnostics. No `proto/`, `gen/`, public gRPC, peer gRPC, storage format, Backend identity, Raft command, Shard rebalancing, upload/restore, or Content Quarantine boundary changed.

### File List

- `_bmad-output/implementation-artifacts/2-5-shard-aware-admin-and-scrapctl-diagnostics.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/admin/server.go`
- `internal/admin/shard_diagnostics.go`
- `internal/admin/shard_diagnostics_test.go`
- `internal/cmd/app.go`
- `internal/cmd/shard_diagnostics.go`
- `internal/cmd/shard_diagnostics_test.go`
- `internal/scrapctl/output.go`
- `internal/scrapctl/status.go`
- `internal/scrapctl/status_shard_test.go`

## Change Log

- 2026-06-11: Created Story 2.5 Shard-aware admin and `scrapctl` diagnostics context and moved status to ready-for-dev.
- 2026-06-11: Implemented Story 2.5 Shard-aware admin and `scrapctl` diagnostics and moved status to review.
- 2026-06-11: Fixed Story 2.5 code-review findings and moved status to done.
