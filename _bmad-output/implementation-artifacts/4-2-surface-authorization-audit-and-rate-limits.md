---
baseline_commit: fb2b59a16878250267dfd8c9666b8a04932e989f
created: 2026-06-12T01:07:25-04:00
---

# Story 4.2: Surface Authorization, Audit, and Rate Limits

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want public, peer, admin, and dangerous operations authorized, audited, and rate-limited,
so that production surfaces fail closed before side effects.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-9 - Production security mode and surface boundaries.
- Security slices: #403 - role authorization and peer identity checks; #404 - audit events and rate limits.
- Governing ADRs: ADR 0019 and ADR 0024.
- Current baseline: Story 4.1 is done at `fb2b59a16878250267dfd8c9666b8a04932e989f`; production startup gates and security runtime wiring were committed and pushed before this story was created.
- Related future stories: Story 4.3 owns OpenBao-backed encrypted write/read, Story 4.4 owns durable rewrap workflow, and Story 4.7 owns production security rehearsal closure with real mTLS/OpenBao evidence.

## Acceptance Criteria

1. **AC-4.2.1 - Unauthorized operations deny before side effects.** Given a caller lacks the required role, when it attempts public, peer, admin, or dangerous operations, then authorization denies before side effects. Evidence proves the denied request does not mutate Raft, Backend, Local Block Lifecycle, or security authority state. Bounded denied audit events are allowed and expected, but audit/evidence must not become storage truth.
2. **AC-4.2.1a - Each surface has a separate denial artifact.** Given public gRPC, peer RPC, admin HTTP, and `scrapctl`-initiated admin paths are tested separately, when each path lacks required auth context, then each path returns the expected denial before side effects. Evidence records one denial artifact per surface.
3. **AC-4.2.2 - Dangerous operations emit bounded audit.** Given an authorized caller performs a dangerous operation, when the operation completes or fails, then bounded audit evidence is emitted. Evidence proves audit fields are low-cardinality and redacted.
4. **AC-4.2.3 - Rate-limit denials are typed and redacted.** Given rate limits are exceeded, when requests continue, then the surface returns typed denials without leaking sensitive request metadata. Evidence records public, peer, admin, and dangerous-operation rate-limit behavior where applicable.

## Tasks / Subtasks

- [x] Create the Story 4.2 evidence artifact before behavior changes. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md`.
  - [x] Record baseline commit, timestamp, owner, exact files reviewed, current coverage, gaps, commands, expected results, actual results, and redaction proof.
  - [x] Use strict result language per row: `PASS`, `CONCERNS`, or `FAIL`; do not use hybrid phrases.
  - [x] If existing code already satisfies a row, prove it with current tests or source evidence. Do not mark a row pass from intent, architecture, or Story 4.1 startup evidence alone.

- [x] Audit and reuse existing security primitives. (AC: 1-4)
  - [x] Read and preserve `internal/security/authorization.go`, `internal/security/grpc_authorization.go`, `internal/security/grpc_identity.go`, `internal/security/ratelimit.go`, `internal/security/authorization_metrics.go`, and `internal/audit/audit.go`.
  - [x] Reuse `security.Authorizer`, `security.Principal*Interceptor`, `security.PeerIdentity*Interceptor`, `security.RateLimiter`, `audit.Event`, `audit.Sink`, and existing OTel observer helpers before adding any new abstraction.
  - [x] Confirm `internal/cmd/tls.go` wires production public gRPC, peer gRPC, admin HTTP, audit sink, and rate limiter through existing options from Story 4.1.
  - [x] Do not introduce new libraries, package-level globals, sleeps in limiter tests, new telemetry label shapes, or a parallel role/audit model.

- [x] Close the public gRPC denial and audit/rate-limit matrix. (AC: 1, 2, 4)
  - [x] Cover `WriteDocument`, `ReadDocument`, `HeadDocument`, and `FindDocuments` with missing principal and wrong-role cases.
  - [x] Assert unauthorized public requests fail before `internal/store` calls, Shard routing, Backend access, Local Block Lifecycle changes, decrypt/rewrap attempts, or Document-byte reads/writes.
  - [x] Assert typed gRPC denials: unauthenticated, permission denied, and rate limited as applicable.
  - [x] Assert denied and rate-limited paths emit bounded audit where configured, and no audit/log/metric output includes raw Transaction IDs, Document names, request metadata, cert material, tokens, raw paths, or dependency error text.

- [x] Close the peer RPC denial and Shard-scope matrix. (AC: 1, 2, 4)
  - [x] Cover wrong role, missing principal, wrong Cell, wrong Member, and unauthorized Shard scope for `ForwardRaft`, `ForwardRaftStream`, `ReplicateDocument`, `RequestIndexRebuild`, `ConsistencyCheck`, and `TransferBlock` where each operation exists.
  - [x] Assert peer denials happen before Raft routing, byte replication sinks, local replication files, rebuild/scrub work, Block transfer file access, Backend access, decrypt/rewrap attempts, or Local Block Lifecycle mutation.
  - [x] Preserve ADR 0024 Shard-scope behavior: a valid peer certificate and `peer_member` role are not enough when the Shard is outside the authorized set.
  - [x] Assert peer rate-limit denials use `codes.ResourceExhausted`, do not starve public/admin budgets, and emit bounded audit/metrics.

- [x] Close the admin HTTP and dangerous-operation matrix. (AC: 1-4)
  - [x] Cover admin read paths, operator paths, break-glass/test-hook paths, pprof profile capture, eviction plan/apply/status, rewrap route, light scrub, metrics, and shard diagnostics where currently registered.
  - [x] Assert wrong-role and unauthenticated admin requests fail before handler side effects, provider/service calls, Raft mutations, Backend mutations, Local Block Lifecycle changes, decrypt/rewrap attempts, or dangerous hook execution.
  - [x] Prove authorized dangerous operations emit bounded audit events on success and failure. If audit sink failure for dangerous operations is not fail-closed, add the narrowest fix and tests.
  - [x] Assert admin rate-limit denials return HTTP 429, do not call protected handlers, and remain independent from public and peer limiter budgets.

- [x] Close the `scrapctl`-initiated admin path evidence. (AC: 2, 4)
  - [x] Test `scrapctl` as a client/operator path only; do not move server-side enforcement into the CLI.
  - [x] Prove production `scrapctl` requires its client TLS material before making admin HTTP calls.
  - [x] Add or reuse a test that drives a `scrapctl` admin command against an admin server returning auth/rate-limit denial and verifies safe operator-facing output, no client-side storage bypass, and no raw secret or identifier leak.
  - [x] Keep `scrapctl` evidence display aligned with server truth: pass/fail, affected surface, reason, and next action where the existing output supports it.

- [x] Prove audit field bounds and redaction. (AC: 2, 3, 4)
  - [x] Verify audit records include only bounded principal handle, role, operation, surface, target, result, reason, correlation/security context where implemented.
  - [x] Verify audit records do not include Document bytes, raw Transaction IDs, raw Document names, plaintext data keys, wrapped-key ciphertext, Transit tokens, cert/key material, raw Backend keys, raw paths, unbounded notes, raw request headers, or dependency error strings.
  - [x] Verify audit labels are low-cardinality and from the existing enums in `internal/audit`, not arbitrary request values.
  - [x] Verify denied/rate-limited requests emit bounded audit where policy requires it, without marking the denied operation allowed.

- [x] Prove per-surface rate-limit isolation with deterministic clocks. (AC: 4)
  - [x] Use `security.WithRateLimitNow` and explicit principal/bucket identities. Do not use sleeps.
  - [x] Prove public saturation does not consume peer/admin budgets.
  - [x] Prove peer saturation does not starve admin evidence or repair-control access.
  - [x] Prove admin or dangerous-operation saturation does not consume public/peer budgets.
  - [x] Assert limiter metrics/log/audit output expose bounded surface, operation, and reason only.

- [x] Preserve package, authority, and scope boundaries. (AC: 1-4)
  - [x] Keep reusable primitives in `internal/security` and `internal/audit`.
  - [x] Keep public gRPC enforcement in `internal/server`, peer enforcement in `internal/peer`, admin enforcement in `internal/admin`, app wiring in `internal/cmd`, and client UX/evidence display in `internal/scrapctl`.
  - [x] Do not change storage identity, Shard authority, Raft command shape, Block/Frame layout, Backend object keys, Pebble Projection authority, envelope encryption, OpenBao bootstrap, or protobuf wire contracts for this story.
  - [x] Do not edit generated `gen/` files directly. If a proto contract change becomes unavoidable, stop and justify the ADR/proto impact before proceeding.

- [x] Update evidence and tracker artifacts. (AC: 1-4)
  - [x] Update this story with debug logs, completion notes, review findings, and file list.
  - [x] Update `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md` with the final surface matrix and command evidence.
  - [x] If `internal/scrapctl/evidencebundle` needs a current signal field or gate text for this story's local evidence, keep the update narrow and do not claim Story 4.7 production rehearsal closure.
  - [x] Move `sprint-status.yaml` to `review` only when implementation and local verification are complete.

- [x] Run verification and leak scans. (AC: 1-4)
  - [x] Run focused unit and package tests listed below.
  - [x] Run affected package regression listed below.
  - [x] Run `git diff --check`.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before code review because this story changes security boundary behavior or evidence.
  - [x] Run credential and identifier leak scans over the new evidence artifact, this story, and touched code. Classify matches as forbidden, allowed fixture/test vocabulary, allowed policy vocabulary, or artifact prose.
  - [x] If `make production-rehearsal-security` is not run, record it as skipped with closure impact. Do not claim production rehearsal readiness from package tests.

## Dev Notes

### Current State

- `CONTEXT.md` defines Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Pebble Projection, Local Block Lifecycle, and storage authority vocabulary. Use these terms exactly.
- PRD FR-9 requires production startup to fail closed and requires public, peer, admin, and `scrapctl` paths to have separate security handling. Role authorization and peer identity checks must run before side effects.
- Architecture says `internal/server`, `internal/peer`, and `internal/admin` own runtime enforcement for their surfaces; `scrapctl` is a client/operator path and must not become server-side enforcement.
- ADR 0019 requires production mTLS on every surface, separates authentication from authorization, defines the role set, requires peer Cell/Member checks, requires dangerous admin audit, and requires independent per-surface rate limits.
- ADR 0024 requires peer Shard-scope authorization before Raft routing, replication sinks, or Block transfer handlers. TLS 1.3 and restart-based certificate rotation are already part of the shared SCRAP TLS builder contract.
- Story 4.1 proved startup gates and runtime wiring for production security config. This story starts after that; do not reopen startup-gate scope unless a Story 4.2 test exposes a direct wiring bug.

### Existing Code To Reuse

- `internal/security/authorization.go` implements role policies, principals, context helpers, HTTP authorization, bounded authorization statuses, and typed auth/rate-limit errors.
- `internal/security/grpc_authorization.go` attaches TLS principals to gRPC contexts and audits/rate-limits early principal denials through `WithPrincipalAudit`.
- `internal/security/grpc_identity.go` enforces verified peer identity before peer RPC handlers.
- `internal/security/ratelimit.go` implements per-surface rate-limit policies, deterministic clock injection, bounded decisions, and observer hooks.
- `internal/security/authorization_metrics.go` and `internal/security/ratelimit_metrics.go` emit bounded OTel attributes for surface, operation, reason, and status.
- `internal/audit/audit.go` defines bounded audit event schema, principal hashing, memory sink, logger sink, policy validation, enum validation, and sink error behavior.
- `internal/cmd/tls.go` wires production public and peer gRPC interceptors, admin TLS, authorizer, audit sink, rate limiter, Transit, and health/evidence security labels.
- `internal/server/authorization_test.go` already proves wrong public roles do not reach the Store for core Document methods.
- `internal/server/audit_ratelimit_test.go` already proves public read audit and rate-limit behavior for `HeadDocument`.
- `internal/peer/authorization_test.go` already proves multiple peer denial-before-side-effect cases, including Raft route, stream route, replication sink, local replication files, rebuild/scrub, and Block transfer.
- `internal/peer/audit_ratelimit_test.go` already proves peer audit/rate-limit behavior and wrong-Shard redaction evidence for several cases.
- `internal/admin/authorization_test.go` already proves many admin denials before side effects for operator, planner, rewrap, metrics, break-glass, and light-scrub paths.
- `internal/admin/audit_ratelimit_test.go` already proves dangerous-operation audit, denied dangerous audit, method-denied audit, distinct denied TLS principal handles, rewrap route classification, and pprof audit behavior.
- `internal/scrapctl/tls_test.go`, `internal/scrapctl/status_shard_test.go`, and `internal/cmd/healthcheck_test.go` already prove production client TLS requirements for selected `scrapctl`/healthcheck paths.
- `internal/scrapctl/evidencebundle` already has security report signals for `UnauthorizedDenialsPassed` and `AuditSamplesRecorded`; update only if Story 4.2 evidence needs a narrow current signal.

### Likely Gaps To Close

- Existing coverage is broad but spread across packages. The evidence artifact must reconcile it into one per-surface matrix and name exact tests/commands.
- Public gRPC coverage may need more explicit audit/redaction proof for write/read/find, not just `HeadDocument`.
- Peer coverage may need one command-level matrix row per RPC family and explicit evidence that no denied path mutates Local Block Lifecycle or reaches Block transfer file access.
- Admin coverage may need explicit failure-path audit for authorized dangerous operations that fail after authorization, and explicit audit-sink failure behavior for dangerous operations if not already proven.
- `scrapctl`-initiated admin denial evidence is likely the weakest surface. It must prove CLI client behavior and safe output, not server-side policy ownership.
- Rate-limit isolation exists in `internal/security` unit tests, but Story 4.2 must prove or document that ingress packages use the independent surfaces correctly.

### Previous Story Intelligence

- Story 4.1 review found evidence overclaim risk. Keep Story 4.2 evidence local/package scoped unless a live production rehearsal command actually runs.
- Story 4.1 post-review language separated deterministic construction-order proof from live listener proof. Apply the same discipline here: a package test can prove side-effect ordering; it is not automatically Tier 3 production security rehearsal evidence.
- Story 3.7 review showed closure artifacts must include current artifact status, exact proof commands/test names, scan counts or classifications, strict `PASS`/`CONCERNS`/`FAIL`, and clear baseline scope.
- Commit/push before continuing was explicitly requested by the user. Keep story creation, implementation, and review-fix commits separated when practical.

### Implementation Guidance

- Start with evidence and tests. Most implementation appears present; the highest-risk failure mode is claiming complete surface proof without reconciling every surface and side-effect boundary.
- When denying before side effects, assert concrete counters or fake service calls: Store calls, Raft router calls, replication sink calls, local file creation, provider/service calls, Backend calls, decrypt/rewrap calls, or handler execution.
- Rate-limit tests must use injected clocks and explicit keys. Do not rely on sleeps, real time, or shared package-global limiters.
- Audit/log/metric redaction checks should search concrete forbidden values injected by the test, not only generic regexes.
- For admin audit failure behavior, keep the fix local to dangerous/security-sensitive operations. Lower-risk audit degradation should remain visible as readiness/evidence failure per architecture, not necessarily a handler failure unless policy requires it.
- `scrapctl` should surface server denial safely and require production TLS. It must never import Store, Shard, Backend, encryption, or direct admin service implementations to bypass the server.
- Do not add a new package or dependency unless the existing primitives cannot support an AC and the evidence explains why.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md` - story status, debug log, completion notes, review findings, and file list.
- `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md` - per-surface matrix, commands, redaction checks, and remaining concerns.
- `internal/security/*_test.go` - rate-limit isolation, interceptor audit/rate-limit, metrics/redaction tests if gaps remain.
- `internal/audit/*_test.go` - audit schema bounds, sink failure, and redaction tests if gaps remain.
- `internal/server/*_test.go` - public Document method denial/audit/rate-limit coverage.
- `internal/peer/*_test.go` - peer identity, Shard-scope, audit/rate-limit, and no-side-effect coverage.
- `internal/admin/*_test.go` - admin and dangerous-operation denial/audit/rate-limit coverage.
- `internal/scrapctl/*_test.go` and `internal/scrapctl/evidencebundle/*_test.go` - CLI-admin denial rendering or evidence signal coverage if gaps remain.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.

Likely avoid:

- `proto/`, `gen/`, Block/Frame code, Backend object key code, storage identity, Shard authority, Pebble Projection authority, encrypted write/read logic, durable rewrap internals, OpenBao bootstrap CLI, deployment manifests, production rehearsal closure docs, and release closure docs.

No ADR is required if the implementation follows ADR 0019 and ADR 0024. Create or update an ADR only if the implementation changes the production security contract, role model, peer identity authority, TLS contract, dependency choices, wire protocol, storage format, or cross-package ownership boundary.

### Testing Requirements

Run focused primitive tests first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/audit -run 'TestRolePolicy|TestAuthorize|TestContextWith|TestPrincipal|TestRateLimiter|TestLoadRateLimit|TestNewEvent|TestLogger|TestMemory|TestPrincipal.*Interceptor|TestPeerIdentity.*Interceptor|Test.*Metric' -count=1 -v
```

Run focused surface tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/peer ./internal/admin -run 'Authoriz|Audit|RateLimit|Denied|WrongShard|Pprof|BreakGlass|Metrics|Eviction|Rewrap|ShardDiagnostics' -count=1 -v
```

Run app wiring and CLI/evidence tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestAppSecurityRuntimeLoadsProductionAuthorizer|TestLoadConfig|TestNewApp|TestRunHealthcheck' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./internal/scrapctl/evidencebundle -run 'Security|Production|Readiness|TLS|Evidence|Status|Doctor|Eviction|Audit|Rate|Denied' -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/audit ./internal/server ./internal/peer ./internal/admin ./internal/cmd ./internal/scrapctl ./internal/scrapctl/evidencebundle -count=1
```

Run leak scans with patterns kept in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md _bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md internal/security internal/audit internal/server internal/peer internal/admin internal/cmd internal/scrapctl
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md _bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md internal/security internal/audit internal/server internal/peer internal/admin internal/cmd internal/scrapctl
```

Run broad gates before review:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

### Latest Technical Information

- No new external dependency or package-registry adoption is needed for Story 4.2. Reuse Go standard-library TLS/X.509 support, repo-pinned gRPC/protobuf, and existing `internal/security` and `internal/audit` primitives.
- The repo currently pins `google.golang.org/grpc v1.81.1` and `google.golang.org/protobuf v1.36.11` in `go.mod`. Re-check `go.mod` before changing any version claim.
- If an implementation change touches gRPC interceptor behavior, rely on the existing gRPC status mapping in `internal/security` and existing generated service contracts. Do not upgrade gRPC or regenerate protobuf output unless a source proto change is explicitly required.
- If external research is needed while implementing, use primary sources only and record any dependency/version conclusion in the evidence artifact. This story should not require non-repo research for normal completion.

### References

- `CONTEXT.md` - domain vocabulary, Cell/Member identity, Local Block Lifecycle, Backend/Pebble/Raft authority boundaries, and non-production visibility.
- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.2 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-9, NFR-2, NFR-3, NFR-4, and production security evidence gate.
- `_bmad-output/planning-artifacts/architecture.md` - Security Mode and Startup Gates, Surface Ownership, Identity/Authorization/Audit/Rate Limits, Security Mode and Enforcement, canonical security fixture matrix, and requirements-to-structure mapping.
- `docs/adr/0019-production-security-boundary.md` - production security boundary and role/audit/rate-limit contracts.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - peer Shard-scope authorization, TLS 1.3, and restart-based certificate rotation.
- `docs/phase-4.5-security-implementation-slices.md` - #403 and #404 implementation slices.
- `_bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md` - previous story implementation and review intelligence.
- `_bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md` - evidence style, command recording, and scan classification pattern.
- `internal/security/authorization.go`
- `internal/security/grpc_authorization.go`
- `internal/security/grpc_identity.go`
- `internal/security/ratelimit.go`
- `internal/security/authorization_metrics.go`
- `internal/security/ratelimit_metrics.go`
- `internal/audit/audit.go`
- `internal/cmd/tls.go`
- `internal/cmd/app.go`
- `internal/server/authorization_test.go`
- `internal/server/audit_ratelimit_test.go`
- `internal/peer/authorization_test.go`
- `internal/peer/audit_ratelimit_test.go`
- `internal/admin/authorization_test.go`
- `internal/admin/audit_ratelimit_test.go`
- `internal/scrapctl/tls_test.go`
- `internal/scrapctl/status_shard_test.go`
- `internal/scrapctl/evidencebundle/bundle.go`

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

- 2026-06-12T01:12:07-04:00 - Marked Story 4.2 in progress after committing and pushing the ready-for-dev story artifact.
- 2026-06-12T01:15:26-04:00 - Added red admin dangerous-operation failure audit tests; they failed because only the pre-operation allowed audit existed and failure-audit rejection did not fail closed.
- 2026-06-12T01:16:00-04:00 - Added `recordFailedOperation` and wired dangerous admin failure paths for eviction, rewrap, projection-key hook, Transit rotate hook, and light scrub hook.
- 2026-06-12T01:17:00-04:00 - Added public gRPC denied audit/redaction test and `scrapctl` bounded admin-denial tests.
- 2026-06-12T01:21:39-04:00 - Completed focused tests, affected regression, leak scans, `git diff --check`, and `make check`.

### Completion Notes List

- Created `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md` with source review list, research/reuse record, red/green log, final surface matrix, leak-scan classification, and production rehearsal boundary.
- Added public gRPC audit/redaction coverage for denied `WriteDocument`, `ReadDocument`, `HeadDocument`, and `FindDocuments` before Store side effects.
- Added `scrapctl status` denial tests for admin HTTP 403 and 429 responses, proving bounded errors do not copy raw response bodies.
- Added dangerous admin operation failure audit tests, including fail-closed behavior when the failed audit event cannot be recorded.
- Implemented bounded failed-operation audit events for admin eviction plan creation/apply, rewrap, projection-key hook, Transit rotate hook, and light scrub hook failure paths.
- No `internal/scrapctl/evidencebundle` code update was needed; existing security report signals are sufficient for this local Story 4.2 evidence.
- Verification passed: focused primitive/surface/app/CLI tests, affected package regression, `git diff --check`, strict token scan, broader leak-scan classification, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### File List

- `_bmad-output/implementation-artifacts/4-2-surface-authorization-audit-and-rate-limits.md`
- `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/admin/audit_ratelimit_test.go`
- `internal/admin/eviction.go`
- `internal/admin/rewrap.go`
- `internal/admin/server.go`
- `internal/scrapctl/tls_test.go`
- `internal/server/audit_ratelimit_test.go`

### Change Log

- 2026-06-12 - Closed Story 4.2 surface authorization, audit, and rate-limit evidence gaps; added failed dangerous-operation audit behavior and local verification evidence.
