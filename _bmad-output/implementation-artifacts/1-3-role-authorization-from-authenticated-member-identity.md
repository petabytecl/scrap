---
baseline_commit: 88f800890e76dc4a696aac2382e93f0ed469e98a
---

# Story 1.3: Role Authorization from Authenticated Member Identity

Status: done

## Story

As a storage operator,
I want public, peer, and admin operations authorized from authenticated principals and Member identity,
so that unauthorized requests fail closed before they can mutate storage state.

## Traceability

- Functional requirement: FR3
- Non-functional requirement: NFR2
- GitHub issue: #403 - https://github.com/petabytecl/scrap/issues/403
- Governing ADR: ADR 0019
- Prerequisites: #399 and #402 are complete; #402 merged as `88f800890e76dc4a696aac2382e93f0ed469e98a`.
- Phase boundary: Do not begin Phase 5 cold-only reads or Epic 2 encryption behavior in this story.

## Acceptance Criteria

1. Given an authenticated public API caller lacks the required Document reader or writer role, when the caller invokes Document read, list, or write behavior, then the request is denied before storage side effects occur, and the response does not leak policy internals or sensitive identifiers.
2. Given an authenticated peer API caller has incomplete or mismatched `cell_id`, `member_hostname`, or `member_id` identity, when it invokes Raft, replication, scrub, repair, or `TransferBlock` behavior, then the request is denied before it can affect Shard state or serve bytes, and the mismatch is visible in bounded admin health or evidence state.
3. Given an authenticated admin caller has only read privileges, when it invokes repair, restore, eviction, quarantine, pprof, or fault operations, then the operation is denied before side effects occur, and dangerous operations require the appropriate operator or break-glass role.
4. Given role policy loading or authorization fails, when logs, errors, admin health, `scrapctl`, or evidence output are inspected, then they contain no certificate material, raw role-policy internals, Document bytes, raw Document identifiers, Backend keys, Transit tokens, or unbounded identity strings.

## Tasks / Subtasks

- [x] Add RED tests for reusable role policy and authorization primitives. (AC: 1, 2, 3, 4)
  - [x] Cover supported roles: `document_writer`, `document_reader`, `peer_member`, `admin_reader`, `admin_operator`, `admin_break_glass`.
  - [x] Cover bounded malformed policy errors, unknown roles, missing principals, and deny-by-default decisions.
  - [x] Do not add a new dependency for policy parsing or assertions.

- [x] Implement focused authorization primitives in `internal/security`. (AC: 1, 2, 3, 4)
  - [x] Load the production role policy JSON already required by `SCRAP_ROLE_POLICY_FILE`.
  - [x] Map authenticated principals to role sets using a bounded, documented first policy shape.
  - [x] Attach authenticated principal/roles to request context through gRPC and HTTP boundary helpers.
  - [x] Return bounded authorization denials suitable for gRPC `PermissionDenied` and HTTP `403`.

- [x] Enforce public Document service roles at the public gRPC boundary. (AC: 1, 4)
  - [x] `WriteDocument` requires `document_writer`.
  - [x] `ReadDocument`, `HeadDocument`, and `FindDocuments` require `document_reader`.
  - [x] Deny before calling `store.Store` methods or starting write goroutines.
  - [x] Preserve existing development/test behavior unless an authz policy is explicitly installed.

- [x] Enforce peer role and Cell/Member identity at the peer gRPC boundary. (AC: 2, 4)
  - [x] All peer RPCs require `peer_member`.
  - [x] All peer RPCs require the `PeerIdentityConfig` extracted by #402 to match configured `cell_id`, `member_hostname`, and `member_id`.
  - [x] Deny before appending Blocks, routing Raft, triggering rebuild, returning scrub data, or streaming Block bytes.
  - [x] Keep peer identity based on verified URI SANs only; do not infer authority from DNS SAN, hostname, remote address, or certificate presence.

- [x] Enforce admin HTTP read/operator/break-glass roles. (AC: 3, 4)
  - [x] `/healthz`, `/metrics`, eviction plan status, and safe pprof reads require `admin_reader`.
  - [x] Eviction plan creation and apply require `admin_operator`.
  - [x] Test hooks and dangerous pprof profile/trace capture require `admin_break_glass` if they are ever enabled.
  - [x] Deny before invoking the configured admin service interfaces.

- [x] Wire production runtime authorization from `internal/cmd`. (AC: 1, 2, 3, 4)
  - [x] Build authorization policy after startup gates pass and before serving.
  - [x] Install public, peer, and admin authorization only in production mode by default.
  - [x] Keep non-production local tests explicit and visibly non-production.
  - [x] Do not make `scrapctl` a server-side enforcement point; it continues to present credentials and call admin/public APIs.

- [x] Surface bounded authorization state in admin health/evidence-ready fields. (AC: 2, 4)
  - [x] Include only low-cardinality state such as `configured`, `not_configured`, `ready`, `denied`, `mismatch`, or `missing_role`.
  - [x] Do not expose raw principal values, SANs, cert subjects, Document identifiers, policy file paths, or policy bodies.

- [x] Verify with focused and broad gates. (AC: 1, 2, 3, 4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/server ./internal/peer ./internal/admin ./internal/cmd`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./...`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...`
  - [x] touched-path lint with `golangci-lint`
  - [x] `git diff --check`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make build`
  - [x] `make check` if the pre-existing `internal/spike` lint baseline permits it; otherwise document the unchanged baseline failure.

## Dev Notes

### Story Scope

This story implements application authorization after mTLS authentication. It may add role-policy parsing, principal extraction from verified client certificates, context helpers, gRPC interceptors, HTTP middleware, peer identity match checks, and bounded admin health state.

This story must not implement audit events, rate limits, Transit/OpenBao clients, encryption, rewrap, new protobuf APIs, certificate hot reload, tenant-specific authorization, Phase 5 cold-only reads, or storage-format changes.

### Current State of UPDATE Files

- `internal/security/startup_gate.go` validates that a role policy file exists and lists only known role names. It does not load per-principal role mappings at runtime. Reuse the existing role constants and error-bounding style instead of introducing a second role vocabulary.
- `internal/security/identity.go` extracts peer Cell/Member identity only from verified URI SANs with the format `spiffe://scrap/cell/<cell_id>/member/<member_hostname>/<member_id>`. Keep that contract unchanged.
- `internal/security/grpc_identity.go` adds peer identity to gRPC context for the production peer server. Add role/principal authz separately; do not weaken the existing `Unauthenticated` path.
- `internal/cmd/config.go` already reads `SCRAP_ROLE_POLICY_FILE` and `SCRAP_PEER_IDENTITY_POLICY_FILE` into startup gate config. Runtime authorization should use the same configured policy path rather than new env names.
- `internal/cmd/tls.go` builds production gRPC/admin TLS options. Add authorization wiring here or adjacent composition code so production serving cannot start without the policy.
- `internal/server/server.go` owns public Document RPC handlers. Deny before calling `store.Store`, before creating the `io.Pipe`, and before starting the write goroutine in `WriteDocument`.
- `internal/peer/server.go` and `internal/peer/transfer.go` own peer RPC handlers. Deny before filesystem writes, Raft routing, rebuild triggering, scrub result return, and Block streaming.
- `internal/admin/server.go` and `internal/admin/eviction.go` own admin HTTP handlers. Deny before calling projection hooks, eviction planner/applier/status provider, metrics handler, pprof handlers, or health/evidence work as required by role.

### Policy Shape for First Implementation

Use a small JSON policy that is easy to validate and test. Recommended shape:

```json
{
  "roles": ["document_writer", "document_reader", "peer_member", "admin_reader", "admin_operator", "admin_break_glass"],
  "principals": [
    {
      "id": "spiffe://scrap/cell/cell-a/member/member-a/member-1",
      "roles": ["peer_member", "admin_reader"]
    }
  ]
}
```

`roles` preserves the Story 1.1 startup gate requirement. `principals` maps an authenticated URI SAN string to role names. Values must be bounded, trimmed, and rejected if empty, unknown, duplicated in ways that change meaning, or too long. Do not log or return the raw principal ID in deployed errors.

If implementation discovers an existing repo convention that conflicts with this shape, stop and record an ADR before changing the authorization contract.

### Authorization Semantics

- Missing authenticated principal: `Unauthenticated` for gRPC, `401` for HTTP.
- Authenticated principal without required role: `PermissionDenied` for gRPC, `403` for HTTP.
- Peer identity missing or malformed: keep `Unauthenticated` from the #402 identity interceptor.
- Peer identity present but not equal to configured Cell/Member relationship: `PermissionDenied` and no side effects.
- Denial messages must be generic, such as `permission denied` or `peer identity mismatch`; do not include principal IDs, SAN values, policy paths, Document identifiers, or Block paths.

### Architecture Guardrails

- `internal/security` owns reusable role parsing, principal extraction, policy evaluation, and context helpers.
- `internal/server` owns public gRPC enforcement for Document operations.
- `internal/peer` owns peer RPC enforcement and Cell/Member identity checks.
- `internal/admin` owns admin HTTP role policy and dangerous-operation checks.
- `internal/cmd` owns production composition and policy loading.
- Core storage packages must not import gRPC status codes or HTTP concerns.
- No ADR is required if the implementation follows ADR 0019 and the policy shape above. Create an ADR only if the role model, peer identity authority, wire protocol, storage format, dependency choices, or package ownership boundary changes.

### Previous Story Intelligence

Story 1.2 added:

- `internal/security/tls_config.go`, `identity.go`, and `grpc_identity.go`.
- Production public/peer/admin TLS runtime wiring in `internal/cmd/tls.go`.
- Peer client/transport TLS injection while preserving development/test insecure defaults.
- Admin TLS serving and `scrapctl` TLS client support.
- Canonical ephemeral cert fixtures under `test/fixtures/security`.

Important lessons:

- Keep production and development/test behavior explicit. Existing package tests rely on insecure local gRPC unless production auth is explicitly installed.
- Errors and operator output must stay bounded; do not include cert paths, SANs, subjects, key material, raw policy file contents, or raw Document identity.
- `make check` previously hit unrelated pre-existing `internal/spike` lint findings locally, while GitHub CI `check` passed for #402. Re-run and document the current reality instead of assuming the old state still applies.

### Testing Requirements

Use Go `testing` and stdlib helpers only. Do not add testify, gomock, gomega, grpc middleware, or a policy-engine dependency.

Minimum focused tests:

- `internal/security`: policy accepts known roles, rejects unknown roles, maps URI SAN principal to roles, denies missing role, rejects oversized or malformed principal IDs, and never exposes raw policy in errors.
- `internal/server`: unauthorized `WriteDocument`, `ReadDocument`, `HeadDocument`, and `FindDocuments` do not call the store; authorized calls still work.
- `internal/peer`: missing role or mismatched Cell/Member identity does not call replication sink, Raft router, rebuild handler, scrub cache, or Block streaming.
- `internal/admin`: read-only callers can read health/status where appropriate; read-only callers cannot apply eviction or invoke dangerous hooks; operator callers can apply eviction.
- `internal/cmd`: production runtime installs authorization options from `SCRAP_ROLE_POLICY_FILE`; development/test runtime does not accidentally require authz in existing local tests.

### Latest Technical Information

- grpc-go authorization examples use server interceptors that return `codes.PermissionDenied` before invoking handlers. This matches the fail-before-side-effect requirement.
- grpc-go also has `google.golang.org/grpc/authz`, but adopting it would add policy-engine semantics and a new dependency contract beyond this story. Prefer the repo-local role matcher unless requirements expand.
- The local repo already pins `google.golang.org/grpc v1.81.1`; do not upgrade dependencies for this story.

### Out of Scope

- Audit event schema or sinks.
- Rate limits.
- OpenBao Transit, fake Transit, encryption, encrypted read/write, or rewrap.
- New public/peer/admin protobuf operations.
- Tenant authorization or tenant-specific storage identity.
- Certificate reload.
- Production enablement of pprof/test hooks without break-glass, audit, and rate-limit coverage.
- Phase 5 cold-only reads.

## Project Structure Notes

- New shared primitives should live in `internal/security`, likely `roles.go`, `policy.go`, `grpc_authorization.go`, and `http_authorization.go` if split files improve focus.
- Public boundary changes belong in `internal/server`.
- Peer boundary changes belong in `internal/peer`.
- Admin boundary changes belong in `internal/admin`.
- Composition changes belong in `internal/cmd`.
- Security fixtures stay under `test/fixtures/security`.
- Evidence artifacts, if created, belong under `_bmad-output/implementation-artifacts/phase-4.5/evidence/authz/`.

## References

- [Epics: Story 1.3 and Epic 1](../planning-artifacts/epics.md#story-13-role-authorization-from-authenticated-member-identity)
- [PRD: FR-3 role authorization and peer identity checks](../planning-artifacts/prds/prd-scrap-2026-06-07/prd.md#fr-3-role-authorization-and-peer-identity-checks-fail-closed)
- [Architecture: Authentication and Security](../planning-artifacts/architecture.md#authentication--security)
- [Architecture: Requirements to Structure Mapping](../planning-artifacts/architecture.md#requirements-to-structure-mapping)
- [ADR 0019: Production security boundary](../../docs/adr/0019-production-security-boundary.md)
- [Phase 4.5 implementation slices](../../docs/phase-4.5-security-implementation-slices.md)
- [Project context](../project-context.md)
- [GitHub issue #403](https://github.com/petabytecl/scrap/issues/403)
- [PR #412](https://github.com/petabytecl/scrap/pull/412)

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- Story created by BMAD create-story workflow on 2026-06-08.
- RED phase: focused package tests initially failed because authorization primitives/options did not exist yet.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/server ./internal/peer ./internal/admin ./internal/cmd` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./...` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/security/... internal/server/... internal/peer/... internal/admin/... internal/cmd/...` passed with 0 issues.
- `git diff --check` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make build` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` failed on the existing repo-wide lint baseline in `internal/spike` and generated `gen/go/scrap/v1` files, outside the touched paths.
- BMAD code review found one coverage gap for explicit side-effect-boundary assertions; added focused tests and reran focused tests, full tests, race tests, touched-path lint, diff check, build, and `make check`.
- PR coverage follow-up added direct security authorization and gRPC interceptor tests; `internal/security` local coverage is 84.5% with `env GOCACHE=/tmp/scrap-v2-go-build go test -coverprofile=/tmp/issue403-security.cover ./internal/security ./internal/server ./internal/peer ./internal/admin ./internal/cmd`.
- Codex review follow-up fixed three findings: same-Cell peer callers no longer need to equal the receiving member identity, gRPC health `Check`/`Watch` bypass document principal authorization, and admin health reports bounded authorization status such as `mismatch` and `missing_role`.
- After Codex review fixes, focused tests, full tests, race tests, touched-path lint, coverage, diff check, and `make build` passed; `make check` still fails only on the existing repo-wide lint baseline outside touched paths.

### Completion Notes List

- Story created by BMAD create-story workflow on 2026-06-08.
- Context analysis completed using current repo state after #402 merge, Phase 4.5 planning artifacts, PRD FR-3, ADR 0019, Story 1.2 implementation notes, grpc-go authorization examples, and local package/test patterns.
- Added bounded role policy parsing, role sets, authorization errors, principal context helpers, and gRPC/HTTP authorization boundary helpers in `internal/security`.
- Enforced Document reader/writer roles before public store side effects and before write goroutines start.
- Enforced `peer_member` plus verified Cell/Member identity matching before peer replication, Raft, rebuild, scrub, and Block transfer side effects.
- Enforced admin reader/operator/break-glass roles across health, metrics, pprof, eviction, and dangerous test hooks.
- Wired production runtime authorization from the existing `SCRAP_ROLE_POLICY_FILE` startup-gate path while preserving explicit non-production behavior.
- Added focused tests for policy validation, denial behavior, public/peer/admin side-effect prevention, and production/non-production composition.
- Added focused coverage for authorization helper transport mappings, TLS principal resolution, policy loader/parser edge cases, gRPC principal interceptors, nil-authorizer compatibility, and peer missing-identity denial.
- Addressed Codex review findings for peer caller identity semantics, public gRPC health checks, and bounded admin authorization status reporting.
- Local BMAD code review is complete with no unresolved patch or decision-needed findings.

### File List

- `_bmad-output/implementation-artifacts/1-3-role-authorization-from-authenticated-member-identity.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/admin/authorization_test.go`
- `internal/admin/eviction.go`
- `internal/admin/server.go`
- `internal/cmd/app.go`
- `internal/cmd/authorization_test.go`
- `internal/cmd/tls.go`
- `internal/peer/authorization_test.go`
- `internal/peer/server.go`
- `internal/peer/transfer.go`
- `internal/security/authorization.go`
- `internal/security/authorization_test.go`
- `internal/security/grpc_authorization_test.go`
- `internal/security/grpc_authorization.go`
- `internal/security/identity.go`
- `internal/security/identity_test.go`
- `internal/security/mode_test.go`
- `internal/security/policy.go`
- `internal/security/roles.go`
- `internal/security/startup_gate.go`
- `internal/server/authorization_test.go`
- `internal/server/server.go`
- `internal/server/telemetry.go`

### Change Log

- 2026-06-08: Implemented Story 1.3 role authorization from authenticated member identity and marked the story done after local code review.
