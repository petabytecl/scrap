# Epic 4 Story 4.2 Evidence: Surface Authorization, Audit, and Rate Limits

Status: done

## Metadata

| Field | Value |
| --- | --- |
| Story | 4.2 - Surface Authorization, Audit, and Rate Limits |
| Baseline commit | `b79eddea88a75ada9a18a7f9368acc30696b6cc5` |
| Evidence started | 2026-06-12T01:12:07-04:00 |
| Owner | Codex |
| Scope | Public gRPC, peer RPC, admin HTTP, and `scrapctl`-initiated admin paths |
| Out of scope | OpenBao encrypted write/read, durable rewrap internals, OpenBao bootstrap, production rehearsal closure, release closure |

## Research And Reuse Record

| Check | Result |
| --- | --- |
| Repo-local reuse | Reuse `internal/security`, `internal/audit`, `internal/server`, `internal/peer`, `internal/admin`, `internal/cmd`, and `internal/scrapctl`; no new role/audit/limiter framework. |
| GitHub repo search | `gh search repos "go grpc authorization audit rate limit interceptor" --limit 5` returned no stronger reusable candidate. |
| GitHub code search | `gh search code "grpc authorization audit rate limit interceptor language:Go" --limit 5` found examples including Teleport, stackrox, grpc-go authz, and small interceptor samples; repo-local primitives remain the right implementation surface. |
| Package/dependency check | `go list -m google.golang.org/grpc google.golang.org/protobuf` returned `grpc v1.81.1` and `protobuf v1.36.11`; no dependency change needed. |
| Primary docs | gRPC status-code docs confirm `UNAUTHENTICATED`, `PERMISSION_DENIED`, and `RESOURCE_EXHAUSTED` are the correct classes. gRPC interceptor docs confirm unary/stream server interceptors are the correct shared boundary mechanism. |

Sources:

- https://grpc.io/docs/guides/status-codes/
- https://chromium.googlesource.com/external/github.com/grpc/grpc-go/+/refs/heads/master/examples/features/interceptor/
- https://github.com/grpc/grpc-go/blob/master/authz/grpc_authz_server_interceptors.go

## Files Reviewed Before Behavior Changes

- `CONTEXT.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture.md`
- `docs/adr/0019-production-security-boundary.md`
- `docs/adr/0024-production-topology-and-peer-scope-policy.md`
- `docs/phase-4.5-security-implementation-slices.md`
- `_bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md`
- `internal/security/authorization.go`
- `internal/security/grpc_authorization.go`
- `internal/security/grpc_identity.go`
- `internal/security/ratelimit.go`
- `internal/audit/audit.go`
- `internal/server/server.go`
- `internal/server/authorization_test.go`
- `internal/server/audit_ratelimit_test.go`
- `internal/peer/server.go`
- `internal/peer/authorization_test.go`
- `internal/peer/audit_ratelimit_test.go`
- `internal/admin/server.go`
- `internal/admin/eviction.go`
- `internal/admin/rewrap.go`
- `internal/admin/authorization_test.go`
- `internal/admin/audit_ratelimit_test.go`
- `internal/scrapctl/status.go`
- `internal/scrapctl/tls_test.go`
- `internal/scrapctl/evidencebundle/bundle.go`

## Initial Coverage Matrix

| AC | Surface | Initial status | Evidence / gap |
| --- | --- | --- | --- |
| AC-4.2.1 | Public gRPC | CONCERNS | `TestDocumentServerDeniesUnauthorizedOperationsBeforeStore` proves wrong-role/missing-principal denial before Store calls. Audit/redaction proof is only explicit for `HeadDocument` rate limit. |
| AC-4.2.1 | Peer RPC | PASS | `internal/peer/authorization_test.go` and `internal/peer/audit_ratelimit_test.go` prove wrong-role, wrong-Shard, route/sink/local-file/Block-transfer no-side-effect behavior. |
| AC-4.2.1 | Admin HTTP | PASS | `internal/admin/authorization_test.go` proves admin denial before handler side effects for operator, planner, rewrap, metrics, break-glass, pprof, and light scrub paths. |
| AC-4.2.1a | `scrapctl` admin path | CONCERNS | Production client TLS requirement is covered. Safe CLI rendering for admin denial/rate-limit response needs explicit test evidence. |
| AC-4.2.2 | Dangerous admin audit | CONCERNS | Allowed, denied, and method-denied audit exists. Post-authorization operation failure does not yet appear to emit `ResultFailed` audit evidence. |
| AC-4.2.3 | Rate limits | CONCERNS | Primitive per-surface isolation exists in `internal/security`; public/peer/admin ingress tests exist. Need explicit evidence tying ingress surfaces together and proving typed denials/redaction. |

## Red/Green Verification Log

| Time | Command | Expected | Actual | Status |
| --- | --- | --- | --- | --- |
| 2026-06-12T01:15:26-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestAdminAuditsDangerousOperationFailure|TestAdminDangerousOperationFailsClosedWhenFailureAuditRejected' -count=1 -v` | RED: fail on missing failed audit and missing fail-closed audit rejection | Failed as expected: one allowed event only; second test returned 412 instead of audit-failure 500 | PASS |
| 2026-06-12T01:16:00-04:00 | same focused admin command | GREEN | Passed | PASS |
| 2026-06-12T01:17:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestDocumentServerAuditsDeniedPublicOperationsWithoutRawIdentifierLeaks|TestDocumentServerAuditsAndRateLimitsPublicReads' -count=1 -v` | Public gRPC denied/rate-limited audit evidence passes | Passed | PASS |
| 2026-06-12T01:17:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'TestStatusAdminDenialsReturnBoundedErrors|TestStatusInProductionRequiresScrapctlTLS|TestStatusUsesMTLSClientCredentials' -count=1 -v` | `scrapctl` TLS and denial rendering evidence passes | Passed | PASS |
| 2026-06-12T01:17:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestAdminAuditsDangerousOperationFailure|TestAdminDangerousOperationFailsClosedWhenFailureAuditRejected|TestAdminAuditsDangerousOperationAndRateLimitDenial' -count=1 -v` | Admin dangerous audit/rate-limit evidence passes | Passed | PASS |
| 2026-06-12T01:18:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/audit -run 'TestRolePolicy|TestAuthorize|TestContextWith|TestPrincipal|TestRateLimiter|TestLoadRateLimit|TestNewEvent|TestLogger|TestMemory|TestPrincipal.*Interceptor|TestPeerIdentity.*Interceptor|Test.*Metric' -count=1 -v` | Primitive authz/audit/rate-limit tests pass | Passed | PASS |
| 2026-06-12T01:18:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server ./internal/peer ./internal/admin -run 'Authoriz|Audit|RateLimit|Denied|WrongShard|Pprof|BreakGlass|Metrics|Eviction|Rewrap|ShardDiagnostics' -count=1 -v` | Focused surface tests pass | Passed | PASS |
| 2026-06-12T01:18:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestAppSecurityRuntimeLoadsProductionAuthorizer|TestLoadConfig|TestNewApp|TestRunHealthcheck' -count=1 -v` | App wiring and security config tests pass | Passed | PASS |
| 2026-06-12T01:18:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./internal/scrapctl/evidencebundle -run 'Security|Production|Readiness|TLS|Evidence|Status|Doctor|Eviction|Audit|Rate|Denied' -count=1 -v` | CLI/evidence focused tests pass | Passed | PASS |
| 2026-06-12T01:19:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/audit ./internal/server ./internal/peer ./internal/admin ./internal/cmd ./internal/scrapctl ./internal/scrapctl/evidencebundle -count=1` | Affected package regression passes | Passed | PASS |
| 2026-06-12T01:21:00-04:00 | `git diff --check` | Whitespace check passes | Passed | PASS |
| 2026-06-12T01:21:00-04:00 | strict token literal scan over BMAD artifacts and touched security packages | No AWS/GitHub/Slack/private-key literals | No matches | PASS |
| 2026-06-12T01:21:00-04:00 | credential-shaped and identifier/path scans over changed files | Matches are expected vocabulary/artifact prose/test fixtures only | Credential-shaped: 75; identifier/path: 34 | PASS |
| 2026-06-12T01:21:39-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build make check` | Full local gate passes | Passed: lint 0 issues, `go test ./...`, `go test -race ./...`, integration tests, builds | PASS |
| 2026-06-12T01:34:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'TestAdminAuditsDangerousOperationFailures|TestAdminAuditsDangerousValidationFailure|TestAdminAuditsPprofProfileFailure|TestAdminDangerousFailureAuditUsesResolvedTLSPrincipal|TestAdminDangerousOperationFailsClosedWhenFailureAuditRejected' -count=1 -v` | Review-fix admin failure audit coverage passes | Passed | PASS |
| 2026-06-12T01:34:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'TestDocumentServerAuditsDeniedPublicOperationsWithoutRawIdentifierLeaks' -count=1 -v` | Review-fix public missing-principal and wrong-role evidence passes | Passed | PASS |
| 2026-06-12T01:35:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin -run 'Authoriz|Audit|RateLimit|Denied|Pprof|BreakGlass|Metrics|Eviction|Rewrap|ShardDiagnostics|LightScrub|Transit' -count=1 -v` | Affected admin surface tests pass after review fixes | Passed | PASS |
| 2026-06-12T01:35:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/server -run 'Authoriz|Audit|RateLimit|Denied' -count=1 -v` | Affected public server tests pass after review fixes | Passed | PASS |
| 2026-06-12T01:38:00-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/audit ./internal/server ./internal/peer ./internal/admin ./internal/cmd ./internal/scrapctl ./internal/scrapctl/evidencebundle -count=1` | Affected package regression passes after review fixes | Passed | PASS |
| 2026-06-12T01:38:00-04:00 | `git diff --check` | Whitespace check passes after review fixes | Passed | PASS |
| 2026-06-12T01:38:00-04:00 | strict token literal scan over BMAD artifacts and touched security packages | No AWS/GitHub/Slack/private-key literals | No matches | PASS |
| 2026-06-12T01:38:00-04:00 | credential-shaped and identifier/path scans over changed files | Matches are expected security vocabulary/artifact prose/test fixtures only | Credential-shaped: 461; identifier/path: 202 | PASS |
| 2026-06-12T01:39:47-04:00 | `env GOCACHE=/tmp/scrap-v2-go-build make check` | Full local gate passes after review fixes | Passed: lint 0 issues, `go test ./...`, `go test -race ./...`, integration tests, builds | PASS |

## Final Surface Matrix

| Surface | Denial artifact | No side effect proof | Audit proof | Rate-limit proof | Result |
| --- | --- | --- | --- | --- | --- |
| Public gRPC | `TestDocumentServerAuditsDeniedPublicOperationsWithoutRawIdentifierLeaks` | Store calls remain 0 for denied write/read/head/find cases | Denied audit events cover missing-principal and wrong-role cases for all Document methods using bounded surface/operation/target/result/reason and no raw tx/doc/principal fragments | `TestDocumentServerAuditsAndRateLimitsPublicReads` returns `codes.ResourceExhausted` and no extra Store call | PASS |
| Peer RPC | Existing peer auth/audit tests, including wrong-Shard denial matrix | Router/sink/local file/Block resolver side effects remain 0 for wrong-Shard cases | Wrong-Shard audit, log, and auth metric evidence excludes raw peer, Document, path, Backend key, and dependency error fixtures | `TestPeerServerAuditsAndRateLimitsPeerOperations` returns `codes.ResourceExhausted` after one allowed peer operation | PASS |
| Admin HTTP | Existing admin auth tests plus dangerous-failure review-fix tests | Wrong-role admin tests keep provider/service/handler counters at 0; failed-operation audit is emitted after authorized handler failure, malformed dangerous requests, and pprof profile failure | New failed audit uses `ResultFailed` and `ReasonInternalError`; TLS-resolved principal attribution is preserved; post-operation failed-audit sink rejection returns 500 before the operation-specific response | `TestAdminAuditsDangerousOperationAndRateLimitDenial` returns HTTP 429 before second handler call | PASS |
| `scrapctl` admin path | `TestStatusAdminDenialsReturnBoundedErrors` and production TLS tests | `scrapctl` makes no storage/server-side bypass and requires TLS before production HTTP calls | Admin 403/429 bodies containing raw secret fixtures are not copied to output/errors | 429 status is surfaced as bounded `GET healthz status: 429` | PASS |

## Redaction / Leak Scan

| Check | Command | Result | Classification |
| --- | --- | --- | --- |
| Strict token literals | `rg -n --pcre2 '(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-[A-Za-z0-9-]+|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----)' ...` | No matches | PASS |
| Credential-shaped terms | `rg --count-matches --pcre2 "$cred_pattern" ...` | 461 matches | Expected security vocabulary, BMAD prose, authz package identifiers, certificate/TLS test vocabulary, and deliberate redaction fixtures such as `secret-token`; no hardcoded credential. |
| Identifier/path terms | `rg --count-matches --pcre2 "$identifier_pattern" ...` | 202 matches | Expected test fixtures, JSON field names, BMAD prose, command examples, TLS certificate test vocabulary, and path examples; no leaked runtime identifier. |

## Known Closure Boundaries

- This story can prove local/package security boundary behavior and evidence artifacts.
- This story must not claim Story 4.7 production security rehearsal closure unless `make production-rehearsal-security` or equivalent live/prod-like evidence actually runs.
- This story must not claim OpenBao encrypted write/read, durable rewrap internals, or OpenBao bootstrap completion.
- `make production-rehearsal-security` was not run for this story; production rehearsal closure remains Story 4.7 scope.
