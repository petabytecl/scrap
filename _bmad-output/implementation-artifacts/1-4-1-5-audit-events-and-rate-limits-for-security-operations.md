---
baseline_commit: 7f818b3f403b36639e082002ace6129c714d8bd4
---

# Stories 1.4 and 1.5: Audit Events and Rate Limits for Security Operations

Status: review

## Story

As a platform operator,
I want security-sensitive operations to emit bounded audit records and respect independent request budgets,
so that production security decisions are attributable and noisy callers cannot starve critical control surfaces.

## Traceability

- Functional requirement: FR4
- Non-functional requirements: NFR2, NFR4
- GitHub issue: #404 - https://github.com/petabytecl/scrap/issues/404
- Governing ADR: ADR 0019
- Prerequisites: #399 and #403 are complete; #403 merged as `7f818b3f403b36639e082002ace6129c714d8bd4`.
- Phase boundary: Do not begin Transit, encryption, rewrap, or Phase 5 cold-only reads in this story.

## Acceptance Criteria

1. Audit events include a bounded principal handle, role, operation, target class, result, and low-cardinality reason for allowed, denied, and rate-limited public, peer, and admin security decisions.
2. Audit events and rate-limit metrics do not log secrets, certificate material, raw Document identifiers, Document bytes, data keys, wrapped-key ciphertext, or unbounded operator notes.
3. Public, peer, and admin surfaces have independent request budgets; exhaustion on one surface does not consume another surface's budget.
4. Rate-limit failures are observable through bounded audit events and low-cardinality metrics.
5. Dangerous admin operations such as eviction apply, pprof profile/trace, and test hooks are denied or audited according to role before side effects.
6. Production startup validates explicit audit and rate-limit policy files using the same runtime policy shape.

## Tasks / Subtasks

- [x] Add bounded audit schema and sink boundary in `internal/audit`. (AC: 1, 2)
- [x] Add deterministic fixed-window rate-limit primitives in `internal/security`. (AC: 3, 4)
- [x] Tighten production startup validation for audit and rate-limit policy shapes. (AC: 6)
- [x] Wire audit and rate limits into public Document RPC handlers before storage side effects. (AC: 1, 3, 4)
- [x] Wire audit and rate limits into peer RPC handlers before Raft, replication, rebuild, scrub, or Block transfer side effects. (AC: 1, 3, 4)
- [x] Wire audit and rate limits into admin HTTP handlers before dangerous admin side effects. (AC: 1, 4, 5)
- [x] Add OTel rate-limit denial metrics with low-cardinality labels. (AC: 2, 4)
- [x] Run focused, full, race, lint, build, and repository gates. (AC: 1-6)
- [x] Run local BMAD code review pass. (AC: 1-6)
- [ ] Run GitHub Codex review loop and address findings. (AC: 1-6)

## Dev Notes

- `internal/audit` owns bounded audit record schema, principal hashing, event validation, and sink boundaries.
- `internal/security` owns rate-limit policy parsing, fixed-window limiter state, rate-limit errors, and OTel denial metrics.
- Public, peer, and admin packages own operation and target classification for their respective boundaries.
- The first runtime policy shape for rate limits is explicit and per surface:

```json
{
  "surfaces": [
    {"surface": "public", "limit": 100, "window": "1m"},
    {"surface": "peer", "limit": 100, "window": "1m"},
    {"surface": "admin", "limit": 100, "window": "1m"}
  ]
}
```

- The first runtime audit policy shape is:

```json
{"sink": "stderr", "failure_mode": "fail_closed", "max_event_bytes": 1024}
```

## References

- [Epics: Stories 1.4 and 1.5](../planning-artifacts/epics.md#story-14-bounded-audit-records-for-security-decisions)
- [PRD: FR-4 security operations are auditable and rate-limited](../planning-artifacts/prds/prd-scrap-2026-06-07/prd.md#fr-4-security-operations-are-auditable-and-rate-limited)
- [Architecture: Audit and rate limits](../planning-artifacts/architecture.md#authentication--security)
- [ADR 0019: Production security boundary](../../docs/adr/0019-production-security-boundary.md)
- [Phase 4.5 implementation slices](../../docs/archive/obsolete-pre-bmad/phase-4.5-security-implementation-slices.md)
- [GitHub issue #404](https://github.com/petabytecl/scrap/issues/404)

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- RED phase: focused package tests initially failed because `internal/audit` and rate-limit APIs did not exist yet.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/audit ./internal/security ./internal/server ./internal/peer ./internal/admin ./internal/cmd` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./...` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/audit/... internal/security/... internal/server/... internal/peer/... internal/admin/... internal/cmd/...` passed with 0 issues.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -coverprofile=/tmp/issue404.cover ./internal/audit ./internal/security ./internal/server ./internal/peer ./internal/admin ./internal/cmd` passed; `internal/audit` coverage is 89.1%.
- `git diff --check` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make build` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` failed only on the existing repo-wide lint baseline outside the touched packages: stale `/tmp/scrap-v2-issue-402` cache warning plus `internal/spike` and generated `gen/go/scrap/v1` lint.
- Local BMAD code review pass completed with no patch-required findings.

### Completion Notes List

- Added bounded audit events with hashed principal handles and strict low-cardinality fields.
- Added in-memory and slog audit sinks.
- Added strict audit and rate-limit production policy loaders.
- Added deterministic per-surface fixed-window rate limiting with OTel denial metrics.
- Wired public, peer, and admin security decisions to emit audit records and enforce independent budgets.
- Tightened production startup validation so audit and rate-limit policy files parse the same runtime shapes.
- Documented the unchanged repo-wide `make check` lint baseline; touched-path lint is clean.

### File List

- `_bmad-output/implementation-artifacts/1-4-1-5-audit-events-and-rate-limits-for-security-operations.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/admin/audit_ratelimit_test.go`
- `internal/admin/server.go`
- `internal/audit/audit.go`
- `internal/audit/audit_test.go`
- `internal/cmd/app.go`
- `internal/cmd/authorization_test.go`
- `internal/cmd/telemetry.go`
- `internal/cmd/tls.go`
- `internal/peer/audit_ratelimit_test.go`
- `internal/peer/server.go`
- `internal/peer/transfer.go`
- `internal/security/authorization.go`
- `internal/security/mode_test.go`
- `internal/security/ratelimit.go`
- `internal/security/ratelimit_test.go`
- `internal/security/startup_gate.go`
- `internal/security/testutil_test.go`
- `internal/server/audit_ratelimit_test.go`
- `internal/server/server.go`
- `internal/server/telemetry.go`
