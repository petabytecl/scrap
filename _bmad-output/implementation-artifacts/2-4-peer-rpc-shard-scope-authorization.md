---
baseline_commit: 461e53622a399b66e34842f4a9d2ea3a2150cad0
---

# Story 2.4: Peer RPC Shard-Scope Authorization

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want peer RPCs authorized by Shard scope,
so that one peer cannot mutate or read Shard state it is not authorized for.

## Traceability

- Epic: Epic 2 - Operators Can Run a Shard-Aware Cell.
- Requirements: FR-4, FR-5.
- Decision gates: DG-2, closed by ADR 0026; peer Shard-scope policy from ADR 0024.
- Prerequisites: Story 2.1 placement validation, Story 2.2 multi-Shard Cell composition, Story 2.3 public Transaction routing.

## Acceptance Criteria

1. **AC-2.4.1 - Placement-derived allow path.** Given a peer is authorized for Shard A, when it calls a Shard A Shard-carrying peer RPC, then the request may proceed to the normal handler path. Evidence proves the authorized Shard set is derived from validated placement/local membership, not caller address, certificate presence, peer address, local files, or Backend objects.
2. **AC-2.4.2 - Wrong-Shard deny before side effects.** Given the same peer calls a Shard B peer RPC and this Member is not authorized for Shard B, then the request is denied before Raft routing, replication sink/write, Block transfer send, rebuild, scrub, local files, or Backend side effects.
3. **AC-2.4.3 - Bounded denial evidence.** Given denial evidence is emitted, when wrong-Shard access is tested, then audit, logs, metrics, status errors, and test evidence use bounded values only. Evidence records the denied-operation command and a leak scan proving no raw principal, peer address, certificate material, `transaction_id`, `document_name`, local path, Backend key, or dependency error detail is exposed.
4. **AC-2.4.4 - Stale or missing auth fails closed.** Given peer membership is stale or the caller lacks required auth context, when a Shard-carrying peer RPC arrives, then the request fails closed before Raft, replication, rebuild, scrub, or Block transfer side effects. Evidence covers wrong Shard, stale membership, and missing auth context cases.

## Tasks / Subtasks

- [x] Lock app-level authorized-Shard derivation from placement membership. (AC: 1, 4)
  - [x] Add or update an `internal/cmd` test that starts from validated multi-Shard placement/local membership, builds the peer server path through `newPeerServer`, and proves `peer.WithAuthorizedShards(shards.IDs()...)` reflects only local Shards.
  - [x] Prove a local Shard request may reach the normal handler path by replacing the Raft router or replication sink with a recording test double after construction.
  - [x] Prove a remote/non-local Shard request is denied before the same recording double is called.
  - [x] Preserve `shardSet.IDs()` copy semantics; do not retain mutable caller-owned slices/maps in new long-lived peer authorization state.
- [x] Complete wrong-Shard side-effect coverage for Shard-carrying peer RPCs. (AC: 2, 4)
  - [x] Cover `ForwardRaft`, `ForwardRaftStream`, `ReplicateDocument`, and `TransferBlock` with wrong-Shard denial assertions.
  - [x] Assert no side effects occur: no Raft route call, no replication sink call, no Block transfer send, and no local Block writer/file operation when a recording boundary exists.
  - [x] Include at least one allowed-path check for two local Shards so the tests prove allow/deny behavior is Shard-set based, not a hardcoded Shard `0` or first configured Shard.
  - [x] Treat `ConsistencyCheck` and `RequestIndexRebuild` carefully: their current proto requests do not carry `shard_id`. In multi-Shard mode, `newPeerServer` currently does not wire scrub/rebuild handlers because there is no unambiguous Shard target. Preserve this fail-closed behavior unless an ADR-backed wire-contract change is explicitly added.
- [x] Verify stale membership and missing auth context fail closed. (AC: 2, 4)
  - [x] Add tests where the caller has peer role and same Cell identity but requests a Shard outside the configured authorized set.
  - [x] Add tests where peer identity or principal context is missing and assert denial happens before Shard-scope handlers.
  - [x] Add tests where role exists but expected Cell/Member binding does not match, and assert no side effects.
  - [x] Use typed errors/status codes (`security.ErrPermissionDenied`, `security.ErrUnauthenticated`, `codes.PermissionDenied`, `codes.Unauthenticated`) instead of string matching.
- [x] Prove bounded audit/log/metric evidence for denials. (AC: 3)
  - [x] Extend `internal/peer` audit tests to cover wrong-Shard denial for every Shard-carrying RPC family or a table that classifies each family.
  - [x] Capture audit/log/status/metric evidence with distinctive forbidden values and assert leak scans omit raw principal IDs, peer addresses, cert material, Transaction IDs, Document names, local paths, and Backend keys.
  - [x] Keep labels and audit fields low-cardinality: surface, operation, target, result, reason, authorization status, and bounded Shard ID are acceptable; raw request identifiers are not.
  - [x] If new metrics are added, use OpenTelemetry and update tests so `make check` proves bounded attributes. Prefer existing rate-limit and audit evidence if it satisfies AC-2.4.3 without new instruments.
- [x] Keep scope narrow and preserve wire/storage contracts. (AC: 1-4)
  - [x] Do not change `proto/`, `gen/`, storage format, Backend object identity, Raft command shape, public API routing, admin diagnostics, `scrapctl`, tenant identity, or Shard rebalancing unless the story proves a required gap and adds the required ADR first.
  - [x] Do not infer authorized Shards from hostnames, peer addresses, certificates alone, Backend keys, local Block files, cached peer maps, or public routing output.
  - [x] Do not add `tenant_id` to peer authorization or storage identity.
  - [x] Do not broaden `internal/peer` imports into `internal/shard`; keep narrow interfaces (`ReplicationSink`, `BlockDirResolver`, `RaftRouter`, scrub/rebuild handlers).
- [x] Record verification in this story before review. (AC: 1-4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer ./internal/cmd ./internal/security`.
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries`.
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build make check`.
  - [x] A leak-scan command over captured evidence/log/status strings or changed files, recorded with PASS/FAIL.

### Review Findings

- [x] [Review][Patch] Full app peer composition did not prove validated local Shard authorization source [internal/cmd/app_test.go] — fixed by moving the app-level proof through `newApp` with explicit multi-Shard placement and a single local Shard.
- [x] [Review][Patch] Multi-Shard no-Shard peer RPC fail-closed behavior was under-tested [internal/cmd/app_test.go] — fixed by asserting `RequestIndexRebuild` and `ConsistencyCheck` fail closed in explicit multi-Shard placement, and by wiring no-Shard peer scrub/rebuild handlers only for the single-Shard fallback topology.
- [x] [Review][Patch] Wrong-Shard audit table lacked side-effect assertions [internal/peer/audit_ratelimit_test.go] — fixed by asserting no Raft route, replication sink, Block directory resolver, or stream send occurs for the wrong-Shard denial matrix.
- [x] [Review][Patch] Wrong-Shard denial evidence did not render logs or metrics [internal/peer/audit_ratelimit_test.go] — fixed by rendering audit log output and bounded OTel authorization-denial metric attributes into the leak scan evidence.
- [x] [Review][Patch] Leak scan fixtures omitted peer address, certificate material, local path, Backend key, and dependency detail inputs [internal/peer/audit_ratelimit_test.go] — fixed by injecting those distinctive fixtures through context metadata, peer address, and side-effect test doubles before scanning evidence output.
- [x] [Review][Patch] ReplicateDocument wrong-Shard denial did not prove local Block writer/file side effects were prevented [internal/peer/authorization_test.go] — fixed by exercising the no-sink local writer path and asserting no Block files or writer state are created.
- [x] [Review][Patch] Multiple-authorized-Shard allow evidence covered only `ForwardRaft` [internal/peer/authorization_test.go] — fixed by extending allowed-path evidence to `ForwardRaftStream`, `ReplicateDocument`, and `TransferBlock` boundary progression.
- [x] [Review][Patch] `shardSet.IDs()` copy semantics were not directly asserted [internal/cmd/app_test.go] — fixed by mutating a caller-owned `IDs()` result and proving app Shard membership remains unchanged.

## Dev Notes

### Current State

- `internal/peer.Server` already has `WithAuthorizedShards`, `authorizedShardIDs`, `authorizePeerForShard`, `authorizePeerShardScope`, and `authorizeShard`. Do not duplicate this mechanism.
- `ForwardRaft`, `ForwardRaftStream`, `ReplicateDocument`, and `TransferBlock` already call Shard-scope authorization before their main side-effect boundary. `ReplicateDocument` authorizes after reading the init message and before the replication sink or Block writer. `TransferBlock` authorizes before resolving Block directories or streaming bytes.
- `internal/cmd/newPeerServer` already passes `peer.WithAuthorizedShards(shards.IDs()...)`. Story 2.4 should lock this with app-level tests that prove the set comes from validated local Shard membership.
- `shardSet.IDs()` returns a copy. `peer.WithAuthorizedShards` also copies into `authorizedShardIDs`. Preserve these immutability properties.
- `shardSetReplicationSink`, `shardSetRaftRouter`, and `shardSet.BlockDirForShard` already dispatch by explicit request Shard ID and fail closed when the target is not local.
- `ConsistencyCheckRequest` and `RequestIndexRebuildRequest` currently do not include `shard_id`. `newPeerServer` wires scrub/rebuild handlers only for the single-Shard fallback topology. In explicit multi-Shard placement these RPCs have no Shard target and stay fail-closed until an ADR-backed peer wire change adds a target.
- `internal/audit.NewEvent` hashes raw principal IDs into bounded `sha256:` handles. `internal/security.RateLimitOTelMetrics` emits bounded surface/operation/reason attributes and does not export the principal key.
- `internal/cmd/newPeerGRPCServerOptions` already applies peer principal and peer identity interceptors for unary and streaming RPCs when production/test security controls are enabled.

### Implementation Guidance

- Start with tests. The most likely implementation work is adding missing app-level and evidence coverage, not replacing peer authorization code.
- Prefer table-driven tests over duplicated RPC-specific setup when asserting identical wrong-Shard behavior, but keep each side-effect assertion explicit.
- For Shard-carrying streaming RPCs, verify denial occurs before stream side effects. For `ForwardRaftStream`, wrong-Shard should stop before route calls. For `ReplicateDocument`, wrong-Shard denial may read only enough to obtain init and must not call sink/writer.
- For status errors, assert `errors.Is` plus `status.Code`; avoid substring checks except leak scans.
- For audit/log redaction, render the captured event/log/status to a string and scan for distinctive forbidden fixtures.
- If a test needs a peer principal, use `security.ContextWithPrincipal` and `security.ContextWithPeerIdentity` rather than TLS setup unless the interceptor path itself is under test.
- If a test needs the production interceptor path, reuse `internal/security/grpc_identity_test.go` patterns and test fixtures instead of building ad hoc certificate parsing.

### Project Structure Notes

Likely update:

- `internal/cmd/app_test.go` - app/composition proof that `newPeerServer` receives placement-derived local Shards.
- `internal/peer/authorization_test.go` - side-effect denial matrix and allowed-path checks for Shard-carrying RPCs.
- `internal/peer/audit_ratelimit_test.go` - bounded audit evidence and wrong-Shard denial classification.
- `internal/security/grpc_identity_test.go` or `internal/cmd/tls.go` tests - only if interceptor-level peer identity/rate-limit evidence is missing.
- `_bmad-output/implementation-artifacts/2-4-peer-rpc-shard-scope-authorization.md` and `sprint-status.yaml` - story status/evidence updates during dev.

Likely avoid:

- `proto/` and `gen/` unless an ADR-backed decision adds Shard IDs to no-Shard peer RPCs.
- `internal/shard/*` unless an existing narrow interface is missing and the test proves the need.
- `internal/routing/*`; peer authorization uses local Shard membership, not Transaction route lookup.
- `internal/server/*`; public routing was Story 2.3.
- `internal/admin/*` and `internal/scrapctl/*`; Story 2.5 owns operator diagnostics.
- Backend, Block/Frame layout, storage identity, tenant identity, and release evidence closure.

### Testing Notes

- Existing relevant tests include:
  - `internal/peer/authorization_test.go` for role, Cell/Member identity, wrong-Shard, and side-effect denial.
  - `internal/peer/audit_ratelimit_test.go` for audit/rate-limit behavior and current wrong-Shard audit coverage.
  - `internal/peer/transfer_test.go` for Shard-specific Block directory resolution.
  - `internal/security/grpc_identity_test.go` for peer identity interceptor behavior.
  - `internal/cmd/app_test.go` for multi-Shard app composition.
- Add the narrowest red tests first. If all production behavior is already present, the story may close with tests/evidence only.
- Required gates before review: targeted package tests, package-boundary check, and `make check`.

### Previous Story Intelligence

- Story 2.1 established `internal/routing` and validated placement without leaking raw Transaction IDs.
- Story 2.2 established multi-Shard `shardSet` composition, per-Shard local directories, peer Raft/replication/TransferBlock dispatch by Shard ID, bounded startup status, and fail-closed handling for missing local Shards.
- Story 2.3 added public API Transaction routing while explicitly leaving peer authorization to Story 2.4. Review patches added bounded route lookup telemetry and broader two-Shard method coverage.
- Recent relevant commits: `461e536 fix: address story 2.3 review findings`, `6d7d3b8 feat: route public API by transaction`, `1b0d2d5 docs: create story 2.3 public routing`, and earlier `d970de3 fix(security): enforce peer Shard scope (#434)`.

### Technical Research Notes

- Official gRPC guidance describes interceptors as the normal mechanism for per-RPC server-side authentication, authorization, logging, and metrics. This matches the existing `internal/cmd/tls.go` peer principal/identity interceptors. Source: https://grpc.io/docs/guides/interceptors/
- Official gRPC-Go authentication documentation keeps TLS/mTLS identity separate from application authorization. Keep ADR 0019/0024's role, Cell, Member, principal, and Shard checks layered above transport identity. Source: https://github.com/grpc/grpc-go/blob/master/Documentation/grpc-auth-support.md
- Official protobuf guidance says adding new fields can be wire-compatible, but this repo treats peer proto changes as wire-contract changes requiring ADR coverage. Do not add `shard_id` to `ConsistencyCheckRequest` or `RequestIndexRebuildRequest` casually. Source: https://github.com/protocolbuffers/protocolbuffers.github.io/blob/main/content/programming-guides/proto3.md

### References

- `_bmad-output/planning-artifacts/epics.md` - Epic 2 overview and Story 2.4 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-4/FR-5, DG-2, multi-Shard routing, and peer authorization evidence expectations.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-2 architecture, package ownership, and `internal/peer` Shard-scope requirement.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - accepted peer Shard-scope policy.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - accepted multi-Shard release boundary and authorized Shard source.
- `CONTEXT.md` - peer service, Shard membership, and route/gateway constraints.
- `_bmad-output/project-context.md` - package boundaries, testing, telemetry redaction, and commit safety.
- `_bmad-output/implementation-artifacts/2-1-shard-routing-boundary-and-placement-validation.md` - placement/routing handoff constraints.
- `_bmad-output/implementation-artifacts/2-2-multi-shard-cell-startup-composition.md` - Shard set composition and peer dispatch patterns.
- `_bmad-output/implementation-artifacts/2-3-public-api-routes-by-transaction.md` - public routing completion notes and peer authorization handoff.
- `proto/scrap/v1/peer.proto` - current Shard-carrying and no-Shard peer RPC request shapes.
- `internal/peer/server.go`, `internal/peer/transfer.go` - peer authorization and side-effect boundaries.
- `internal/peer/authorization_test.go`, `internal/peer/audit_ratelimit_test.go`, `internal/peer/transfer_test.go` - existing peer authorization/evidence tests.
- `internal/cmd/app.go`, `internal/cmd/app_test.go`, `internal/cmd/shard_set.go` - app composition, local Shard set, and peer server wiring.
- `internal/security/grpc_identity.go`, `internal/security/grpc_authorization.go`, `internal/cmd/tls.go` - mTLS principal/peer identity and authorization interceptors.
- `internal/audit/*`, `internal/security/ratelimit.go` - bounded audit and rate-limit evidence behavior.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/peer ./internal/security` initially failed because the new audit table used `context.Context` without importing `context`.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/peer ./internal/security` passed after adding the missing import.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed.
- LINT: `env GOCACHE=/tmp/scrap-v2-go-build make check` failed on `gocognit` for the new wrong-Shard audit test; split case setup and assertions into helpers.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd ./internal/peer ./internal/security` passed after the lint refactor.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after the lint refactor.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer -run TestPeerServerAuditsWrongShardDenialsWithoutRawIdentifierLeaks -count=1` passed as the explicit leak-scan evidence command.
- REVIEW: BMad code-review launched Blind Hunter, Edge Case Hunter, and Acceptance Auditor layers against `461e53622a399b66e34842f4a9d2ea3a2150cad0..HEAD`; findings were patch-only with no decision-needed or deferred items.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/peer ./internal/cmd` passed after review patches.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make package-boundaries` passed after review patches.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer -run TestPeerServerAuditsWrongShardDenialsWithoutRawIdentifierLeaks -count=1` passed after audit/log/status/metric leak evidence was expanded.
- LINT: `env GOCACHE=/tmp/scrap-v2-go-build make check` initially failed on formatter/cyclop/context-argument lints in new review tests; ran `make fmt`, split assertions, and moved `context.Context` to first helper parameter.
- PASS: `env GOCACHE=/tmp/scrap-v2-go-build make check` passed after review lint fixes.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added app-level proof that `newPeerServer` derives authorized peer Shards from validated local placement membership and copies that set before serving.
- Added peer tests proving multiple local Shards are allowed while wrong-Shard `TransferBlock` denial happens before Block directory resolution or stream sends.
- Added bounded audit/status evidence for wrong-Shard denials across `ForwardRaft`, `ForwardRaftStream`, `ReplicateDocument`, and `TransferBlock` with raw identifier leak checks.
- Added bounded OTel authorization-denial metrics for peer authorization failures and wired them through app composition.
- Tightened explicit multi-Shard placement so no-Shard scrub/rebuild peer RPC handlers are not wired without an ADR-backed Shard target.
- Kept scope out of proto, generated files, storage identity, peer wire contract, public API routing, admin, `scrapctl`, Backend, and Shard internals.

### File List

- `_bmad-output/implementation-artifacts/2-4-peer-rpc-shard-scope-authorization.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/cmd/app.go`
- `internal/cmd/app_test.go`
- `internal/cmd/authorization_test.go`
- `internal/cmd/telemetry.go`
- `internal/cmd/tls.go`
- `internal/peer/audit_ratelimit_test.go`
- `internal/peer/authorization_test.go`
- `internal/peer/server.go`
- `internal/security/authorization_metrics.go`

## Change Log

- 2026-06-11: Created Story 2.4 peer RPC Shard-scope authorization context and moved status to ready-for-dev.
- 2026-06-11: Implemented Story 2.4 peer Shard-scope authorization evidence and moved status to review.
- 2026-06-11: Addressed code-review findings, added bounded authorization-denial metrics, preserved multi-Shard no-Shard RPC fail-closed behavior, and moved status to done.
