---
baseline_commit: de5fbbab49117ca2395fd7c0424dc402cbf4eaa3
created: 2026-06-12T15:32:07-04:00
---

# Story 5.5: Admin HTTP Quarantine Operations

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a security operator,
I want admin HTTP operations to list, inspect, confirm, and release quarantined Documents,
so that quarantine response follows the existing V2 admin surface.

## Traceability

- Epic: Epic 5 - Security Operators Can Contain Unsafe Content Without Mutating Documents.
- Requirements: FR-12.
- Governing decision: DG-1 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs: ADR 0008 async content scanning, ADR 0025 admin surface amendment, ADR 0026 multi-Shard release boundary.
- Current baseline: Story 5.4 is done and pushed at `de5fbbab49117ca2395fd7c0424dc402cbf4eaa3`.
- Related future scope: Story 5.6 owns `scrapctl` quarantine UX; Story 5.7 owns content-safety closure evidence.

## Acceptance Criteria

1. **AC-5.5.1 - Admin list and inspect are bounded and protected.** Given quarantined Documents exist, when an authorized admin lists or inspects them, then bounded metadata is returned without Document bytes. Evidence records authorization, rate-limit, audit, and redaction proof.
2. **AC-5.5.2 - Confirm converges through Raft authority.** Given an authorized operator confirms quarantine, when the request is accepted, then the lifecycle change converges through Raft authority. Evidence proves confirm does not mutate local scanner state directly.
3. **AC-5.5.3 - Release changes read eligibility only after commit.** Given an authorized operator releases quarantine, when the request is accepted, then read eligibility changes only after committed metadata converges. Evidence records release, audit, and post-release read behavior.

## Tasks / Subtasks

- [x] Create the Story 5.5 evidence artifact before production code changes. (AC: 1-3)
  - [x] Create `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md`.
  - [x] Record baseline commit, changed boundaries, endpoint contract, authz/rate-limit/audit proof, Raft convergence proof, release/read proof, redaction scans, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [x] Keep closure scoped to Story 5.5. Do not claim `scrapctl` quarantine UX, scanner engine runtime closure, or Epic 5 closure.

- [x] Add authoritative quarantine lifecycle commands. (AC: 2, 3)
  - [x] Extend `proto/scrap/v1/raft.proto` additively with metadata-only confirm and release commands. Do not reuse existing field numbers or put Document bytes, scanner payloads, operator notes, signatures, rules, or auth claims in Raft.
  - [x] Regenerate `gen/go/scrap/v1/raft.pb.go` through the repo protobuf toolchain. Do not edit generated files by hand.
  - [x] Implement Shard proposal/apply paths that wait for committed local apply, following `proposeQuarantineDocument`, proposal watchers, trace context injection, and `applySpanInfo` patterns.
  - [x] Confirm must leave read denial active and record a bounded confirmed lifecycle state.
  - [x] Release must remove or mark inactive the active Content Quarantine gate only through a committed Raft entry; reads must remain denied until that committed apply is visible.
  - [x] Add replay/reopen tests proving confirm and release rebuild from Raft/Projection state without scanner memory.

- [x] Extend Content Quarantine Projection helpers. (AC: 1-3)
  - [x] Add bounded list support over the existing `q\x01` Content Quarantine prefix. Use Pebble iterators with lower/upper bounds, copy iterator values before close, and cap results.
  - [x] Add inspect support for one `(transaction_id, document_name)` using existing `GetContentQuarantine`.
  - [x] Add release support that deletes or inactivates the read-gate record with `pebble.Sync`.
  - [x] If confirm needs additional lifecycle fields, version the value format and keep current v1 records decodable as active/unconfirmed. Do not make old quarantine records unreadable.
  - [x] Fail closed on corrupt quarantine records for list, inspect, confirm, release, and read eligibility. Do not silently skip corrupt records.

- [x] Add Shard-facing operator methods. (AC: 1-3)
  - [x] Expose narrow Shard methods for listing, inspecting, confirming, and releasing Content Quarantine. Keep `internal/shard` as authority and keep `internal/admin` as HTTP rendering only.
  - [x] Validate all `transaction_id` and `document_name` inputs with store validation at the boundary.
  - [x] For confirm/release, require the Document is currently quarantined; map missing active quarantine to a bounded not-found/precondition result.
  - [x] Preserve leader/read behavior: not-leader and unavailable Shard route errors must not mutate state.
  - [x] Do not mutate scanner scheduler state, scanner watermarks, Block bytes, `.blk` files, `.idx` files, Backend objects, Transaction entries, or upload catalogs.

- [x] Add admin HTTP endpoints. (AC: 1-3)
  - [x] Add a focused `internal/admin/quarantine.go` rather than expanding `server.go` heavily.
  - [x] Register endpoints only when a quarantine service is wired:
    - `GET /admin/quarantine/documents` with bounded optional filters such as `transaction_id` and `limit`.
    - `GET /admin/quarantine/document` with `transaction_id` and `document_name` query parameters.
    - `POST /admin/quarantine/confirm` with bounded JSON body `{ "transaction_id": "...", "document_name": "..." }`.
    - `POST /admin/quarantine/release` with the same bounded JSON body.
  - [x] Use `security.RoleAdminReader` for list/inspect.
  - [x] Use `security.RoleAdminOperator` or `security.RoleAdminBreakGlass` for confirm/release, per ADR 0025. If the current single-role helper cannot express this, add a small helper and tests for either-role authorization.
  - [x] Use `http.MaxBytesReader`, `json.Decoder.DisallowUnknownFields`, bounded query parsing, and explicit method checks.
  - [x] Return JSON only. Responses may include validated `transaction_id` and `document_name` because the operator action requires them, but must not include Document bytes, scanner signatures, YARA/ClamAV rule text, dependency logs, local paths, Backend keys, trace IDs, request IDs, auth claims, or free-form operator notes.
  - [x] Map invalid input to `400`, missing active quarantine to `404` or a clearly documented precondition status, route/unavailable/not-leader to `503`, and unexpected failures to bounded `500` messages.

- [x] Wire the admin service in the composition root. (AC: 1-3)
  - [x] Add `admin.WithQuarantineService(...)` or equivalent and wire it from `internal/cmd/app.go`.
  - [x] In single-Shard fallback, wire the local Shard directly.
  - [x] In multi-Shard mode, route inspect/confirm/release by `transaction_id` to exactly one owning Shard using existing placement. Do not create a cross-Shard quarantine registry.
  - [x] For unfiltered list, aggregate only local Shards exposed by this process and include bounded `shard_id` metadata so operators understand the local scope.
  - [x] Preserve existing admin metrics, health, pprof, eviction, rewrap, test-hook, TLS, authz, audit, and rate-limit behavior.

- [x] Update audit and security evidence. (AC: 1-3)
  - [x] Add bounded audit operation constants for quarantine list, inspect, confirm, and release; add them to audit validation.
  - [x] Ensure allowed, denied, rate-limited, method-not-allowed, validation-failed, and service-failed paths produce bounded audit events where existing admin patterns require them.
  - [x] Do not add raw `transaction_id`, `document_name`, scanner signatures, rule names, or request bodies to audit, logs, metrics, traces, or evidence.
  - [x] Add tests proving unauthorized or wrong-role confirm/release requests do not call the Shard service.

- [x] Prove release/read convergence. (AC: 2, 3)
  - [x] Add Shard tests: quarantine denies read, confirm keeps read denied, release through committed apply allows read, replay restores the same state.
  - [x] Add admin tests: list/inspect returns bounded JSON and no bytes; confirm/release call the service only after authz/rate-limit/audit checks pass.
  - [x] Add composition tests for route-to-owning-Shard behavior if adapter logic changes.
  - [x] Add redaction scans over story/evidence/admin/quarantine/shard/index/proto code.

- [ ] Update story, evidence, and sprint artifacts. (AC: 1-3)
  - [x] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [x] Update the evidence artifact and this story with debug log references, completion notes, review findings, and file list.
  - [ ] Run `bmad-code-review`; address critical/high findings before marking `done`.

## Dev Notes

### Current State

- Story 5.3 added scanner detection to `QuarantineDocument` Raft command flow and the sparse Content Quarantine Projection key `q\x01<transaction_id>\x00<document_name>`.
- Story 5.4 added public read denial and metadata scan status. `ReadDocument` now checks committed Content Quarantine state before returning bytes and wraps stream readers with a read-time recheck.
- Current Content Quarantine Projection values record `BlockID`, `DetectedAtUs`, `ScanType`, and `Reason`. They do not yet record operator confirm state or release state.
- `proto/scrap/v1/raft.proto` currently has `QuarantineDocument` but no confirm/release command.
- `internal/index/content_quarantine.go` currently has point-get and put helpers only. It has no list iterator and no delete/inactive helper.
- `internal/shard/content_quarantine.go` currently proposes/apply scanner detections only. Operator confirm/release must not call scanner internals.
- `internal/admin` already owns the HTTP operator surface. There is no admin gRPC surface for quarantine; ADR 0025 explicitly amends ADR 0008 on this point.
- `internal/admin/server.go` centralizes authorization, rate limiting, audit, and route registration. Endpoint implementation files such as `eviction.go` and `rewrap.go` keep handler-specific logic out of `server.go`.
- `internal/admin/server.go` already has a `quarantined_blocks` health field sourced from eviction/local Block lifecycle. That is Block Quarantine, not Content Quarantine. Do not reuse or reinterpret it for Content Quarantine counts.
- `internal/cmd/app.go` wires Shard-backed admin services through `appendShardAdminOptions`. Multi-Shard adapters route by `transaction_id` for Document-scoped admin operations such as rewrap.

### Existing Code To Reuse

- `internal/admin/server.go` - route registration, `authorize`, `authorizeMethod`, `checkRateLimit`, `recordAudit`, `recordFailedOperation`, and `adminAuditRoutes`.
- `internal/admin/eviction.go` - bounded JSON body parsing, method dispatch, and operator endpoint patterns.
- `internal/admin/rewrap.go` - Document-scoped admin service interface and HTTP error mapping.
- `internal/admin/audit_ratelimit_test.go` and `internal/admin/authorization_test.go` - authz, audit, rate-limit, and no-side-effect-before-auth test patterns.
- `internal/cmd/app.go` - admin option wiring and multi-Shard adapter pattern.
- `internal/shard/content_quarantine.go` and `internal/shard/content_quarantine_test.go` - quarantine proposal/apply, local apply wait, validation, and replay tests.
- `internal/shard/content_quarantine_read_test.go` - post-release read behavior should build from the quarantine/read-denial tests.
- `internal/index/content_quarantine.go` and `internal/index/content_quarantine_test.go` - Projection encoding, validation, corrupt-value tests, and streaming hash guard.
- `internal/index/upload_outbox.go` - bounded Pebble iterator pattern with copied results and iterator error checks.
- `internal/audit/audit.go` - bounded audit vocabulary and validation.
- `internal/security/roles.go` - admin role vocabulary.

### Implementation Guardrails

- Confirm/release are metadata lifecycle operations. They must not mutate Document bytes, Block files, Backend objects, scanner watermarks, or scanner scheduler state.
- Raft is the authority for confirm/release. Admin handlers and `scrapctl` are clients of authority, not authority.
- Release must be fail-closed: do not allow reads until the release command has committed and applied locally.
- Confirm must keep the Document quarantined and unreadable.
- Existing v1 quarantine Projection records must remain decodable after any value-format versioning.
- Do not add a new gRPC AdminService.
- Do not implement `scrapctl` commands in this story.
- Do not add free-form operator notes unless a bounded, redacted, ADR-covered field is introduced. Prefer no notes for Story 5.5.
- Keep Content Quarantine separate from Block Quarantine and Deep Scrub repair.
- Public/admin errors must be bounded. Internal dependency errors must not be reflected verbatim when they may contain paths, Backend keys, scanner payloads, or request details.
- Admin JSON responses may include the requested Document identity because operators need it to act. Logs, audit, metrics, traces, and evidence must not retain raw Document identity.

### Latest Tech Information

- No new external runtime dependency is needed for Story 5.5. Use repo-pinned Go/protobuf/Buf/Pebble versions from `go.mod`, `tools.go.mod`, `buf.gen.yaml`, and `Makefile`.
- The existing admin HTTP surface uses Go stdlib `net/http`. Go's `http.MaxBytesReader` is the right local pattern for limiting request bodies and preventing oversized admin requests from wasting server resources. Reference: https://pkg.go.dev/net/http#MaxBytesReader.
- Protobuf changes are additive source changes under `proto/`; regenerate generated code with `make proto` or the repo's existing proto target, then verify with `make proto-check`.
- `scrapd` remains `CGO_ENABLED=0` and `FROM scratch`; admin quarantine operations must not add shell/runtime tooling or native scanner dependencies.

### Project Structure Notes

Likely update during implementation:

- `proto/scrap/v1/raft.proto`
- `gen/go/scrap/v1/raft.pb.go`
- `internal/quarantine/*` only if shared DTOs are needed between Shard/admin/cmd; keep authority out of this package.
- `internal/index/content_quarantine.go`
- `internal/index/content_quarantine_test.go`
- `internal/shard/content_quarantine.go`
- `internal/shard/content_quarantine_test.go`
- `internal/shard/content_quarantine_read_test.go`
- `internal/shard/apply.go`
- `internal/shard/trace_propagation.go`
- `internal/admin/quarantine.go`
- `internal/admin/server.go`
- `internal/admin/*quarantine*_test.go`
- `internal/audit/audit.go`
- `internal/cmd/app.go`
- `internal/cmd/*quarantine*_test.go` if adapter routing changes
- `_bmad-output/implementation-artifacts/5-5-admin-http-quarantine-operations.md`
- `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`

Avoid unless a failing test proves it is required:

- `proto/scrap/v1/document.proto`, public DocumentService wire shape, scanner scheduler behavior, scanner engine adapters, `scrapctl`, Block format, Backend object identity, Local Block Lifecycle, deployment overlays, or production security config semantics.

### Testing Requirements

Run proto and focused tests after implementation:

```bash
make proto-check
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/index ./internal/shard ./internal/admin ./internal/cmd -run 'Quarantine|Admin|Audit|RateLimit|Authorization|ReadDocument' -count=1
```

Run targeted package gates:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/server ./internal/shard ./internal/index ./internal/admin ./internal/cmd ./internal/scrapctl -count=1
```

Run static and structural gates:

```bash
git diff --check
scripts/check-e2e-gates.sh
```

Run broad local gate before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, and touched quarantine/admin/status code:

```bash
secret_shape_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----)'
quarantine_sensitive_pattern='([t]ransaction_id=|[d]ocument_name=|[i]dempotency[_-]?[k]ey=|Backend [k]ey:|trace[_-]?[i]d=|request[_-]?[i]d=|[s]ignature=|[r]ule=|clamd_[e]rror=|yara_[e]rror=|[f]ile[_-]?[p]ath=|operator_[n]ote=)'
scan_scope='_bmad-output/implementation-artifacts/5-5-admin-http-quarantine-operations.md _bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md proto/scrap/v1/raft.proto gen/go/scrap/v1/raft.pb.go internal/index/content_quarantine.go internal/shard/content_quarantine.go internal/admin/quarantine.go internal/admin/server.go internal/audit/audit.go internal/cmd/app.go'
rg -n --pcre2 "$secret_shape_pattern" $scan_scope
rg -n --pcre2 "$quarantine_sensitive_pattern" $scan_scope
```

### Previous Story Intelligence

- Story 5.4 committed and pushed `414eef7 feat: deny quarantined document reads`; review fixes committed and pushed `de5fbba fix: address quarantined read review findings`.
- Review fixes that must not regress:
  - Shard rechecks Content Quarantine after restore/read setup and wraps returned readers with a read-time quarantine guard.
  - Public `FAILED_PRECONDITION` status messages are bounded to `failed precondition`.
  - Reader-time store precondition errors map to gRPC `FAILED_PRECONDITION`.
  - Production code must not contain test-only quarantine corruption helpers.
- Story 5.4 final gate passed after review fixes: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- Recent commit shape:
  - `de5fbba fix: address quarantined read review findings`
  - `414eef7 feat: deny quarantined document reads`
  - `3cc94e4 docs: create story 5.4 quarantined reads`
  - `672d7d7 fix: address quarantine raft review findings`
  - `6b08381 feat: add quarantine raft projection state`

### References

- `CONTEXT.md` - Content Quarantine glossary, Block Quarantine distinction, and read behavior vocabulary.
- `_bmad-output/project-context.md` - Go package boundaries, admin/security rules, testing rules, proto generation rules, no raw identifier telemetry, and static scratch image rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 5 and Story 5.5 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-12 Content Quarantine read gate and admin operations.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-1 scanner/quarantine architecture, admin HTTP amendment, boundary map, and evidence requirements.
- `docs/adr/0008-async-content-scanning-architecture.md` - quarantine store, read behavior, and original admin operations.
- `docs/adr/0025-content-quarantine-admin-surface.md` - accepted admin HTTP surface, roles, audit, rate-limit, and redaction requirements.
- `docs/adr/0026-multi-shard-v2-release-boundary.md` - scanner/quarantine remain Shard-local authority flows.
- `docs/go-style-guide.md` - proto source-of-truth, package boundaries, errors, concurrency, tests, and metrics conventions.
- `_bmad-output/implementation-artifacts/5-4-quarantined-read-denial-and-metadata-reconciliation.md` - previous story implementation record and review findings.
- `_bmad-output/implementation-artifacts/epic-5-quarantined-read-metadata-evidence.md` - previous story final evidence.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for implementation.

### Debug Log References

- 2026-06-12T15:32:07-04:00 - Story 5.5 created from sprint status after Story 5.4 implementation, BMAD code review, review fixes, commit, and push completed.
- 2026-06-12T15:38:11-04:00 - BMAD dev-story implementation started from clean `v2` head `a20b9a1ba97e67e42d3f3d2e103831e707bafec8`.
- 2026-06-12T15:38:11-04:00 - Verified pre-code evidence artifact exists with Story 5.5-only scope, baseline, endpoint contract, and pending verification matrix.
- 2026-06-12T15:40:00-04:00 - Red tests failed as expected for missing confirm/release Raft commands, Projection helpers, and Shard/admin methods: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/store ./internal/index ./internal/shard -run 'Quarantine|ContentQuarantine|ReadDocument' -count=1`.
- 2026-06-12T15:57:00-04:00 - Implementation completed for additive Raft lifecycle commands, Projection helpers, Shard authority methods, admin HTTP endpoints, audit operations, and composition wiring.
- 2026-06-12T15:57:29-04:00 - Full local gate passed: `env GOCACHE=/tmp/scrap-v2-go-build make check`.
- 2026-06-12T15:59:31-04:00 - Final static/proto/e2e gate checks and redaction scans passed with no matches.

### Completion Notes List

- Created Story 5.5 from the next backlog item in `sprint-status.yaml`.
- Scoped implementation to admin HTTP quarantine list, inspect, confirm, and release only.
- Preserved Story 5.6 `scrapctl` operator UX as future scope.
- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added metadata-only `ConfirmQuarantine` and `ReleaseQuarantine` Raft commands with committed local apply before operator success.
- Added bounded Content Quarantine list, inspect, confirm, and release Projection helpers while keeping existing v1 quarantine values decodable.
- Added Shard-facing operator methods that validate Document identity, preserve fail-closed read denial, and leave scanner/Block/Backend state untouched.
- Added admin HTTP quarantine endpoints with reader/operator or break-glass authorization, bounded JSON parsing, audit operations, and rate-limit integration.
- Wired quarantine operations through the composition root for single-Shard fallback and local multi-Shard routing.
- Verified focused tests, targeted package gates, `make check`, proto check, e2e policy gate, diff checks, and redaction scans.

### File List

- `_bmad-output/implementation-artifacts/5-5-admin-http-quarantine-operations.md`
- `_bmad-output/implementation-artifacts/epic-5-admin-http-quarantine-operations-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `proto/scrap/v1/raft.proto`
- `gen/go/scrap/v1/raft.pb.go`
- `internal/quarantine/doc.go`
- `internal/quarantine/types.go`
- `internal/index/content_quarantine.go`
- `internal/index/content_quarantine_test.go`
- `internal/shard/apply.go`
- `internal/shard/content_quarantine.go`
- `internal/shard/content_quarantine_read_test.go`
- `internal/shard/content_quarantine_test.go`
- `internal/store/proto_raft_contract_test.go`
- `internal/admin/quarantine.go`
- `internal/admin/server.go`
- `internal/admin/server_test.go`
- `internal/admin/authorization_test.go`
- `internal/admin/audit_ratelimit_test.go`
- `internal/audit/audit.go`
- `internal/cmd/app.go`
- `internal/cmd/app_test.go`

## Change Log

| Date | Version | Description | Author |
| --- | --- | --- | --- |
| 2026-06-12 | 0.1 | Initial ready-for-dev story created from Epic 5 Story 5.5. | GPT-5 Codex |
| 2026-06-12 | 0.2 | Implemented admin HTTP quarantine operations and Raft lifecycle authority. | GPT-5 Codex |
