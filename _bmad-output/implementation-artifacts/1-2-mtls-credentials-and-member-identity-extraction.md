---
baseline_commit: 1bdeb844da41f986b1eaa92662d787801a84c663
---

# Story 1.2: mTLS Credentials and Member Identity Extraction

Status: review

## Story

As a platform operator,
I want public API, peer API, admin API, and `scrapctl`-invoked operations to use validated mTLS credentials and authenticated Member identity,
so that transport identity is explicit before authorization decisions run.

## Traceability

- Functional requirements: FR2, FR3
- Non-functional requirement: NFR2
- GitHub issues: #402 - https://github.com/petabytecl/scrap/issues/402, partial prerequisite for #403
- Governing ADRs: ADR 0019 and ADR 0020
- Phase boundary: Phase 4.5 must pass production security and encryption evidence before Phase 5 cold-only reads begin.

## Acceptance Criteria

1. Given production security mode is enabled, when public API, peer API, admin API, or `scrapctl` client/server credentials are missing or invalid, then the affected listener or client refuses insecure credentials, and the failure does not fall back to plaintext or test-only authority.
2. Given a peer API request presents authenticated transport credentials, when the request is accepted for authorization, then the request context contains the authenticated Cell and Member identity needed for policy evaluation, and identity extraction does not rely on hostname, network address, or certificate presence alone.
3. Given local development tests run with explicit development or test mode, when they use non-production credentials or test authority, then the mode remains visible in diagnostics, and the same evidence cannot satisfy production readiness.
4. Given TLS/mTLS credential loading or identity extraction fails, when logs, errors, admin health, `scrapctl`, or evidence output are inspected, then they contain no certificate/key material, raw cert contents, raw file contents, raw paths, Document bytes, Backend keys, Transit tokens, or unbounded identity strings.

## Tasks / Subtasks

- [x] Add RED tests for reusable TLS credential builders and authenticated identity extraction. (AC: 1, 2, 4)
  - [x] Added canonical generated certificate helpers under `test/fixtures/security` for CA, server cert, client cert, expired cert, wrong CA, and missing client credential cases.
  - [x] Added `internal/security` tests for server mTLS config, client mTLS config, bounded errors, expired client certs, and verified URI-SAN identity extraction.
  - [x] Added context/interceptor tests proving extracted peer identity is available to downstream request handling.

- [x] Implement focused TLS and identity primitives in `internal/security`. (AC: 1, 2, 4)
  - [x] Added `tls_config.go`, `identity.go`, and `grpc_identity.go` without expanding startup-gate ownership beyond EKU validation.
  - [x] Server configs require `tls.RequireAndVerifyClientCert`, client CA pools, server certs, and TLS 1.2+.
  - [x] Client configs require root CA, server name, client certificate, and never set `InsecureSkipVerify`.
  - [x] Peer Member identity is extracted only from verified certificate URI SANs using `spiffe://scrap/cell/<cell_id>/member/<member_hostname>/<member_id>`.
  - [x] Errors remain bounded by config class/key and do not include raw cert/key paths or identity values.

- [x] Wire public and peer gRPC listeners through per-surface server credentials. (AC: 1, 3, 4)
  - [x] Added `internal/cmd` runtime TLS construction helpers sharing the Story 1.1 env/startup-gate config.
  - [x] Production public and peer gRPC servers use `grpc.Creds(credentials.NewTLS(...))` and fail before serving on invalid TLS config.
  - [x] Development/test mode preserves existing insecure local test behavior.
  - [x] mTLS authentication remains separate from Story 1.3 role authorization.

- [x] Wire peer clients and shared Raft peer transport through per-surface client credentials. (AC: 1, 2, 3, 4)
  - [x] Added transport-credential options to `internal/peer.Client` and `internal/peer.SharedTransport` without changing Shard authority interfaces.
  - [x] Production peer client and Raft forwarding transport use peer mTLS client credentials.
  - [x] Existing package tests keep insecure defaults; new tests cover mTLS success, plaintext/missing client cert failure, wrong client CA failure, and expired client cert rejection.

- [x] Wire admin HTTP and `scrapctl` operator calls through mTLS-capable clients. (AC: 1, 3, 4)
  - [x] Added admin HTTP `ListenAndServeTLS` path using prebuilt TLS config without changing health payload shape or authorization semantics.
  - [x] Extended `scrapctl` common flags/env with `--tls-cert`, `--tls-key`, `--tls-ca`, `--tls-server-name`, and `SCRAP_TLS_SCRAPCTL_*`.
  - [x] `scrapctl` presents client certificates, validates server certificates, requires HTTPS when TLS is configured/production, and keeps Kubernetes-only commands independent of TLS.
  - [x] Preserved existing local `scrapctl` behavior when no TLS is requested and production mode is not set.

- [x] Surface bounded mTLS/authentication state in diagnostics and evidence. (AC: 3, 4)
  - [x] Reused existing bounded security mode/readiness diagnostics; no new cert-derived fields were needed for this slice.
  - [x] Evidence bundle HTTP client construction now passes through the TLS-aware wrapper without recording cert paths, subjects, SANs, or key material.
  - [x] Production readiness remains distinct from Kubernetes serving readiness.

- [x] Update deployment/test defaults deliberately. (AC: 1, 3)
  - [x] Base/prodlike overlays remain explicit non-production (`SCRAP_SECURITY_MODE=development`) until real certificate secrets are mounted in a later slice.
  - [x] No production manifest fallback to plaintext was added.
  - [x] No unsafe test hooks were enabled outside existing explicit environments.

- [x] Verify with targeted and broad-enough gates. (AC: 1, 2, 3, 4)
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/peer ./internal/admin ./internal/scrapctl`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./...`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/integration/ -v -timeout 120s`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/security/... internal/cmd/... internal/scrapctl/... internal/peer/... internal/admin/... test/fixtures/security/...`
  - [x] `git diff --check`
  - [x] `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make build`
  - [x] `make check` was attempted; it is blocked by pre-existing `internal/spike` lint findings unrelated to this story.

## Dev Notes

### Story Scope

This story wires live mTLS credential loading and authenticated transport identity. It may build TLS configs, gRPC transport credentials, admin HTTP TLS config, peer-client credentials, and `scrapctl` client TLS behavior. It must not implement role authorization, peer authorization policy, audit schema/sinks, rate-limit enforcement, OpenBao Transit calls, encrypted Document writes, durable rewrap, or Phase 5 cold-only reads.

Story 1.1 already added explicit security modes, production startup validation, bounded readiness output, and production gate config parsing. Reuse those primitives and avoid changing the accepted security-mode contract unless a new ADR is created. [Source: _bmad-output/implementation-artifacts/1-1-production-security-mode-startup-gates.md]

Do not treat NetworkPolicy, Cilium policy, Kubernetes RBAC, overlay names, or `SCRAP_ENVIRONMENT=prodlike` as an application security boundary. ADR 0019 explicitly rejects network-policy-only security. [Source: docs/adr/0019-production-security-boundary.md#Decision]

### Current State of UPDATE Files

- `internal/security/startup_gate.go` validates production TLS file presence, server cert/key parsing, client CA parsing, server cert validity, and configured server identity, but it does not build live runtime `tls.Config` or gRPC credentials. Keep startup-gate validation reusable and avoid duplicating parsing rules in unrelated packages. [Source: internal/security/startup_gate.go]
- `internal/cmd/config.go` reads `SCRAP_SECURITY_MODE`, `SCRAP_TLS_PUBLIC_*`, `SCRAP_TLS_PEER_*`, `SCRAP_TLS_ADMIN_*`, and `SCRAP_TLS_SCRAPCTL_*` into `security.StartupGateConfig`. Extend this shape carefully so runtime credential builders and startup validation share source data. Preserve the pattern that malformed explicit env values return an error naming the key. [Source: internal/cmd/config.go]
- `internal/cmd/app.go` currently creates public and peer gRPC servers with only `grpc.StatsHandler(...)`, and peer clients with constructors that use insecure credentials. Production mTLS must be wired before serving begins and before peer clients are used. [Source: internal/cmd/app.go]
- `internal/peer/client.go` and `internal/peer/transport.go` currently dial peers with `insecure.NewCredentials()`. Add constructor options or config injection so production uses mTLS and existing package tests can keep explicit non-production insecure clients. [Source: internal/peer/client.go, internal/peer/transport.go]
- `internal/admin/server.go` currently owns HTTP admin serving, `/healthz`, optional `/metrics`, optional test hooks, optional pprof, and eviction/admin handlers. Add TLS-capable serving without changing the current health schema unnecessarily. [Source: internal/admin/server.go]
- `internal/scrapctl` currently constructs operator HTTP/Kubernetes calls and renders output. It owns CLI request construction and display, not server-side enforcement. Add TLS client loading here instead of importing storage or Shard packages. [Source: internal/scrapctl]
- `internal/cmd/healthcheck.go`, E2E tests, integration tests, stress code, and package test helpers use `insecure.NewCredentials()` today. Preserve this only for explicit development/test paths, not production. [Source: repo search for `insecure.NewCredentials`]

### Architecture Guardrails

- `internal/cmd` owns startup validation, config defaults, dependency construction, TLS config loading, role-policy loading, and Transit/fake selection. It must not own runtime storage decisions, Shard authority, or ad hoc per-request policy logic. [Source: _bmad-output/planning-artifacts/architecture.md#Surface-Ownership]
- `internal/security` is the planned home for security mode invariants, mTLS principal parsing, role evaluation primitives, and rate-limit policy primitives. Keep reusable TLS and identity primitives here. [Source: _bmad-output/planning-artifacts/architecture.md#Requirements-to-Structure-Mapping]
- `internal/server` owns public gRPC boundary policy for served methods. This story can attach transport credentials, but #403 owns authorization decisions. [Source: _bmad-output/planning-artifacts/architecture.md#Surface-Ownership]
- `internal/peer` owns peer mTLS authentication and Cell/Member identity checks. This story may extract identity into request context; #403 owns allow/deny policy using that identity. [Source: _bmad-output/planning-artifacts/architecture.md#Surface-Ownership]
- `internal/admin` owns admin HTTP/future admin gRPC security boundary and admin status output. This story may add TLS-capable serving; dangerous-operation authorization remains later-story work. [Source: _bmad-output/planning-artifacts/architecture.md#Surface-Ownership]
- `scrapctl` loads and presents client credentials but is not a server-side enforcement point. [Source: _bmad-output/planning-artifacts/architecture.md#Ingress-and-Readiness-Rules]

### Identity Contract for This Story

For the first implementation, use one documented, bounded certificate identity format and test it through canonical fixtures. Preferred peer URI SAN format:

```text
spiffe://scrap/cell/<cell_id>/member/<member_hostname>/<member_id>
```

The extractor must reject missing URI SANs, malformed URI SANs, empty fields, cross-Cell identity, wrong Member hostname, wrong durable Member ID, and identity values that exceed bounded length or contain path/control characters. DNS SANs may be used for server-name validation, but peer Member identity must not be inferred from DNS SAN alone, common name, hostname, remote address, or certificate presence alone.

If implementation finds an existing repo convention that conflicts with this format, stop and require a documented decision before changing the identity contract.

### Testing Requirements

Follow TDD for this story. Start with `internal/security` tests because the main risk is accidentally enabling plaintext or unverifiable transport in production.

Minimum focused tests:

- production server config sets `tls.RequireAndVerifyClientCert`;
- production server config rejects missing cert, missing key, missing client CA, expired server cert, invalid CA, and wrong server identity;
- production client config rejects missing cert/key, missing CA, missing server name, wrong CA, expired client cert, and any `InsecureSkipVerify` path;
- development/test mode can still use explicit insecure credentials in existing package tests;
- public and peer gRPC server construction uses TLS credentials in production and no plaintext fallback;
- peer client and Raft peer transport use TLS credentials in production;
- peer identity extraction accepts only verified certificates with the documented URI SAN format;
- wrong Cell/Member identity is rejected before #403 policy evaluation;
- admin HTTP and `scrapctl` client TLS configuration can validate server certificates and present client certificates;
- errors and operator output remain bounded and redacted.

Use Go standard `testing` only. Do not add testify, gomock, gomega, or a new assertion/mocking dependency. Use `t.TempDir()`, table tests, `net/http/httptest` where appropriate, and reusable fixture helpers under `test/fixtures/security`.

### Latest Technical Information

- Official grpc-go encryption examples wire mTLS with `grpc.Creds(credentials.NewTLS(tlsConfig))`, `grpc.WithTransportCredentials(...)`, server `tls.Config.ClientCAs`, and `tls.RequireAndVerifyClientCert`. Source: https://github.com/grpc/grpc-go/blob/master/examples/features/encryption/README.md
- Go `crypto/tls` documents `RequireAndVerifyClientCert` as requiring at least one valid client certificate during the handshake, and documents that server `ClientCAs` are used to verify client certificates. Source: https://pkg.go.dev/crypto/tls
- grpc-go `credentials.NewClientTLSFromCert` docs say full `credentials.NewTLS` is needed when client certificates are required for mTLS. Source: https://github.com/grpc/grpc-go/blob/master/credentials/tls.go
- The local repo pins `google.golang.org/grpc v1.81.1`; do not upgrade dependencies for this story. [Source: go.mod]

### Research / Reuse Notes

- GitHub code search over grpc-go confirms the canonical mTLS pattern uses `credentials.NewTLS` and `tls.RequireAndVerifyClientCert`.
- No new library is needed for Story 1.2. Use Go standard library packages such as `crypto/tls`, `crypto/x509`, `encoding/pem`, `net/url`, `net/http`, and `strings`, plus existing `google.golang.org/grpc/credentials`.
- Reuse existing env parsing style from `internal/cmd/config.go`, startup-gate validation from `internal/security`, peer client constructors from `internal/peer`, and CLI option parsing from `internal/scrapctl`.

### Out of Scope

- Do not implement role authorization or peer allow/deny policy. Story 1.3 owns that.
- Do not add audit event schema/sinks or rate limiters. Story 1.4/1.5 own those.
- Do not implement OpenBao clients, fake Transit, envelope metadata, encrypted writes/reads, or rewrap. Epic 2 owns those.
- Do not change storage identity, add `tenant_id` to storage identity, change Backend object identity, or alter Block/Frame layout.
- Do not change the protobuf public API unless implementation proves request context cannot carry the required identity and a new ADR/proto decision is made.
- Do not implement certificate hot reload. Restart-based certificate/key rotation is acceptable for Phase 4.5.
- Do not start Phase 5 cold-only read behavior.

### Implementation Notes for the Dev Agent

- Recommended first new files: `internal/security/tls_config.go`, `internal/security/tls_config_test.go`, `internal/security/identity.go`, and `internal/security/identity_test.go`.
- Recommended fixture location: `test/fixtures/security`, with a small Go helper package if needed. Avoid committing generated private keys outside test fixture code unless they are clearly test-only and never referenced by production manifests.
- Prefer passing `credentials.TransportCredentials` or narrow TLS option structs into `internal/peer.Client` and `internal/peer.SharedTransport` instead of making those packages read environment variables.
- For admin HTTP, prefer explicit TLS server construction or `http.Server.ServeTLS` with prebuilt config. Avoid logging cert/key paths or material.
- For `scrapctl`, keep TLS flags/env common to commands that call admin/public APIs. Do not make Kubernetes-only commands require TLS.
- Keep security status data low-cardinality: use values like `enabled`, `disabled`, `configured`, `not_configured`, `production`, `development`, and bounded reasons such as `missing_client_certificate` or `invalid_peer_identity`.

## Project Structure Notes

The story aligns with the architecture route map:

- Shared TLS and identity primitives belong in `internal/security`.
- Runtime config and pre-serving credential construction belong in `internal/cmd`.
- Public gRPC TLS attachment belongs at the `internal/server` boundary as wired by `internal/cmd`.
- Peer gRPC server/client/transport TLS attachment belongs in `internal/peer` as wired by `internal/cmd`.
- Admin HTTP TLS behavior belongs in `internal/admin`.
- CLI TLS behavior belongs in `internal/scrapctl`.
- Canonical security fixtures belong under `test/fixtures/security`.
- Evidence artifacts belong under `_bmad-output/implementation-artifacts/phase-4.5/evidence/mtls/` if this story creates local evidence files.

No ADR is required if the implementation follows ADR 0019. Create or update an ADR only if the implementation changes the accepted production security mode, certificate identity contract, auth/encryption contract, dependency choices, wire protocol, storage format, or cross-package ownership boundary.

## References

- [Epics: Story 1.2 and Epic 1](../planning-artifacts/epics.md#story-12-mtls-credentials-and-member-identity-extraction)
- [PRD: Surface Authentication, Authorization, and Identity](../planning-artifacts/prds/prd-scrap-2026-06-07/prd.md#42-surface-authentication-authorization-and-identity)
- [Architecture: Authentication and Security](../planning-artifacts/architecture.md#authentication--security)
- [Architecture: Ingress and Readiness Rules](../planning-artifacts/architecture.md#ingress-and-readiness-rules)
- [Architecture: Requirements to Structure Mapping](../planning-artifacts/architecture.md#requirements-to-structure-mapping)
- [ADR 0019: Production security boundary](../../docs/adr/0019-production-security-boundary.md)
- [ADR 0020: OpenBao envelope encryption contract](../../docs/adr/0020-openbao-envelope-encryption-contract.md)
- [Phase 4.5 implementation slices](../../docs/phase-4.5-security-implementation-slices.md)
- [Project context](../project-context.md)
- [GitHub issue #402](https://github.com/petabytecl/scrap/issues/402)
- [grpc-go encryption examples](https://github.com/grpc/grpc-go/blob/master/examples/features/encryption/README.md)
- [Go crypto/tls package](https://pkg.go.dev/crypto/tls)
- [grpc-go TLS credentials](https://github.com/grpc/grpc-go/blob/master/credentials/tls.go)

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- Story created by BMAD create-story workflow on 2026-06-08.
- RED: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security` failed on missing TLS/identity APIs.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/peer ./internal/admin ./internal/scrapctl` passed.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle` passed.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./...` passed.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test -race ./...` passed.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/integration/ -v -timeout 120s` passed.
- GREEN: touched-path lint passed with `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m internal/security/... internal/cmd/... internal/scrapctl/... internal/peer/... internal/admin/... test/fixtures/security/...`.
- GREEN: `git diff --check` passed.
- GREEN: `env GOCACHE=/tmp/scrap-v2-go-build GOFLAGS=-buildvcs=false make build` passed.
- BLOCKED BASELINE: `env GOCACHE=/tmp/scrap-v2-go-build make check` fails only in pre-existing `internal/spike` lint findings.

### Completion Notes List

- Story created by BMAD create-story workflow on 2026-06-08.
- Context analysis completed for Story 1.2 using current repo state, Phase 4.5 planning artifacts, ADR 0019, Story 1.1 implementation notes, grpc-go examples, and Go `crypto/tls` documentation.
- Added reusable mTLS server/client builders and verified certificate URI-SAN Member identity extraction in `internal/security`.
- Added peer identity gRPC interceptors and wired them on the production peer server path only.
- Wired production public/peer gRPC servers, peer clients, Raft shared transport, admin HTTP, `scrapctl`, and `scrapd healthcheck` through mTLS-capable credentials.
- Preserved development/test insecure defaults unless production mode or explicit TLS flags/env request mTLS.
- Review pass found and fixed a plaintext `http://` gap for TLS-configured `scrapctl` admin/metrics calls.
- No deployment secret mounts were added because current base/prodlike overlays remain explicitly non-production.

### File List

- `_bmad-output/implementation-artifacts/1-2-mtls-credentials-and-member-identity-extraction.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/admin/server.go`
- `internal/admin/tls_test.go`
- `internal/cmd/app.go`
- `internal/cmd/healthcheck.go`
- `internal/cmd/healthcheck_test.go`
- `internal/cmd/tls.go`
- `internal/peer/client.go`
- `internal/peer/tls_test.go`
- `internal/peer/transport.go`
- `internal/scrapctl/eviction.go`
- `internal/scrapctl/evidence.go`
- `internal/scrapctl/run.go`
- `internal/scrapctl/status.go`
- `internal/scrapctl/tls.go`
- `internal/scrapctl/tls_test.go`
- `internal/security/grpc_identity.go`
- `internal/security/grpc_identity_test.go`
- `internal/security/identity.go`
- `internal/security/identity_test.go`
- `internal/security/startup_gate.go`
- `internal/security/tls_config.go`
- `internal/security/tls_config_test.go`
- `test/fixtures/security/certs.go`

## Change Log

- 2026-06-08: Created Story 1.2 with implementation context for mTLS credentials and Member identity extraction.
- 2026-06-08: Implemented mTLS credential loading, peer identity extraction, production runtime wiring, `scrapctl`/healthcheck TLS clients, tests, and review fixes.
