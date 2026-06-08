---
baseline_commit: bfe258f3b044cc03465486b5ae8db8ddd46a2c96
---

# Story 1.1: Production Security Mode Startup Gates

Status: done

## Story

As a platform operator,
I want `scrapd` to reject unsafe production security configuration at startup,
so that production Cells cannot accidentally run with development security settings.

## Traceability

- Functional requirement: FR1
- Non-functional requirements: NFR1, NFR3, NFR4
- GitHub issue: #401 - https://github.com/petabytecl/scrap/issues/401
- Governing ADRs: ADR 0019 and ADR 0020
- Phase boundary: Phase 4.5 must pass production security and encryption evidence before Phase 5 cold-only reads begin.

## Acceptance Criteria

1. Given `scrapd` is configured for production security mode, when required TLS, role policy, peer identity, Transit, audit/rate-limit, or unsafe admin hook configuration is missing, invalid, or contradictory, then startup fails before public, peer, admin, metrics, pprof, test-hook, or operator-facing surfaces can serve traffic, and the startup error names the invalid configuration class without logging secrets.
2. Given `scrapd` is configured for development or test security mode, when the process starts successfully, then admin health, `scrapctl status`, metrics, diagnostics, and evidence bundles identify the non-production mode, and the mode does not satisfy production readiness or Phase 5 entry checks.
3. Given a production readiness check runs against a Cell in development or test security mode, when the check evaluates write ACK or Phase 5 readiness, then readiness fails with a `non_production_security_mode` reason, and no production gate treats the mode as equivalent to production.
4. Given production startup validation rejects configuration, when logs, errors, metrics, admin health, `scrapctl`, or evidence output are inspected, then they contain no secrets, certificate/key material, Transit token values, Document bytes, raw Document identifiers, Backend keys, raw file paths, or high-cardinality values.

## Tasks / Subtasks

- [x] Add RED tests for security mode parsing and startup gate validation. (AC: 1, 2, 3)
  - [x] Add package-local tests for accepted modes: `production`, `development`, and `test`.
  - [x] Assert empty or unknown `SCRAP_SECURITY_MODE` is rejected; do not silently default to development.
  - [x] Assert production mode rejects each missing config class independently: TLS, role policy, peer identity policy, Transit config, audit sink config, rate-limit config, `SCRAP_TEST_HOOKS`, and unsafe pprof/dangerous-hook policy.
  - [x] Assert development/test mode can start without production credentials but returns production-readiness failure reason `non_production_security_mode`.

- [x] Implement security mode primitives in `internal/security`. (AC: 1, 2, 3, 4)
  - [x] Create a focused `internal/security` package only for shared security primitives; do not create `util`, `common`, `shared`, or `helpers`.
  - [x] Model `Mode` as a small value type with explicit string parsing, `IsProduction()`, `IsNonProduction()`, and bounded readiness reason helpers.
  - [x] Return typed or sentinel errors that let `internal/cmd` name the failing configuration class without parsing provider strings.
  - [x] Keep the zero value fail-closed: unset mode must not accidentally mean development.

- [x] Wire `SCRAP_SECURITY_MODE` and production gate config through `internal/cmd`. (AC: 1, 2, 4)
  - [x] Add `Config.SecurityMode` and a nested production gate config shape to `internal/cmd/config.go`.
  - [x] Preserve current env parsing behavior: malformed explicit values return errors naming the offending key, and unrelated defaults remain with owning packages.
  - [x] Validate production gates before `newApp` opens upload Backend, Shard, telemetry exporters, listeners, gRPC servers, admin HTTP server, metrics, pprof, or test hooks.
  - [x] Preserve existing local/dev startup smoke tests by setting explicit development or test mode in tests and manifests.

- [x] Validate required production configuration without implementing later-story enforcement. (AC: 1)
  - [x] TLS gate: require per-surface server cert, server key, and client CA configuration for public, peer, admin, and future `scrapctl` client paths; parse cert/key/CA enough to catch missing files, invalid PEM, mismatched key pair, invalid CA bundle, expired certs, and obvious identity mismatch.
  - [x] Role policy gate: require a role policy path and parse a minimal schema containing only ADR 0019 role names: `document_writer`, `document_reader`, `peer_member`, `admin_reader`, `admin_operator`, and `admin_break_glass`.
  - [x] Peer identity gate: require configured Cell/Member identity policy sufficient for Story 1.2/1.3 to verify `cell_id`, `member_hostname`, and durable `member_id`; do not authorize by hostname, address, or certificate presence alone.
  - [x] Transit gate: require production Transit config presence and reject fake Transit in production, but do not implement OpenBao calls here; Story 2.1 owns the `internal/encryption` Transit client/fake behavior.
  - [x] Dangerous hook gate: reject `SCRAP_TEST_HOOKS=true` in production; reject pprof or future dangerous hooks until the required `admin_break_glass` policy, server-side enforcement, and audit path are active.

- [x] Surface security mode and production readiness through existing operator outputs. (AC: 2, 3, 4)
  - [x] Extend admin `/healthz` with bounded fields such as `security_mode`, `production_readiness_status`, and `production_readiness_reason`.
  - [x] Preserve existing admin health fields and eviction/upload health behavior.
  - [x] Extend `internal/scrapctl.Health` and `scrapctl status` JSON/text output by reusing the admin health schema; do not make `scrapctl` enforce server-side policy.
  - [x] Add a `scrapctl doctor` check for production readiness so development/test mode reports a clear non-production reason without breaking ordinary Kubernetes serving readiness.
  - [x] Do not make the existing gRPC health `scrap.v1-readiness` probe fail solely because the Cell is in development/test mode; Kubernetes readiness and production/Phase 5 readiness must stay distinct.

- [x] Make security mode visible in metrics, diagnostics, and evidence bundles. (AC: 2, 3, 4)
  - [x] Add only low-cardinality mode/status data, for example an OpenTelemetry resource attribute or bounded gauge; do not add raw config paths or identifiers as labels.
  - [x] Ensure the evidence bundle captures admin health with the active security mode and production-readiness reason.
  - [x] If `config.json` in evidence bundles is extended, include only bounded mode/status fields and omit cert/key paths, Transit tokens, policy contents, and raw identifiers.

- [x] Update deployment/test defaults deliberately. (AC: 2, 3)
  - [x] Set explicit non-production security mode in local, existing prod-like/evidence, and test manifests that currently lack production credentials.
  - [x] Keep `deploy/kustomize/environments/prodlike/statefulset-prodlike-patch.yaml` free of `SCRAP_TEST_HOOKS`.
  - [x] Add a focused fixture or test command proving production mode rejects missing config instead of making the current prod-like Cell intentionally fail to roll out.
  - [x] Leave production mTLS rollout to Story 1.2 and production authorization rollout to Story 1.3.

- [x] Verify with targeted and broad-enough gates. (AC: 1, 2, 3, 4)
  - [x] Run focused package tests first: `go test ./internal/security ./internal/cmd ./internal/admin ./internal/scrapctl`.
  - [x] Run evidence bundle tests if touched: `go test ./internal/scrapctl/evidencebundle`.
  - [x] Run manifest gate if deployment files changed: `make manifests-check`.
  - [x] Run `make static` before review when adding `internal/security` or changing package boundaries.
  - [x] Run `make tier1-check` before broad review if startup, deployment, or evidence behavior changed across packages.

### Review Findings

- [x] [Review][Patch] Shared base manifest sets development mode instead of keeping production overlays fail-closed [deploy/kustomize/base/statefulset.yaml:36]
- [x] [Review][Patch] TLS gate can skip server identity and accepts unvalidated CA certificates [internal/security/startup_gate.go:158]
- [x] [Review][Patch] Transit token environment variable presence is not validated [internal/security/startup_gate.go:250]
- [x] [Review][Patch] Peer identity policy is not compared with configured Cell/Member identity [internal/security/startup_gate.go:235]
- [x] [Review][Patch] Audit and rate-limit policies can be null or empty JSON objects [internal/security/startup_gate.go:260]

## Dev Notes

### Story Scope

This story is the first Phase 4.5 architecture gate. It makes security mode explicit, fails production startup closed, and exposes non-production status. It must not implement live mTLS enforcement, role authorization, peer authorization, audit sink semantics, rate-limit enforcement, OpenBao Transit calls, encrypted Document writes, rewrap, or Phase 5 cold-only reads. Those belong to later stories.

Do not treat NetworkPolicy, Cilium policy, Kubernetes RBAC, overlay names, or `SCRAP_ENVIRONMENT=prodlike` as an application security boundary. ADR 0019 explicitly rejects network-policy-only security. [Source: docs/adr/0019-production-security-boundary.md#Decision]

### Current State of UPDATE Files

- `internal/cmd/config.go` currently owns scrapd flag/env parsing and validates typed/ranged env values. It reads `SCRAP_CELL_ID`, upload settings, raw telemetry IDs, `SCRAP_TEST_HOOKS`, `SCRAP_PPROF_ENABLED`, peer resolution, scrub, upload pressure, and eviction config. Preserve the pattern that malformed explicit env values return an error naming the key. [Source: internal/cmd/config.go]
- `internal/cmd/app.go` currently constructs Backend, telemetry, Shard, peer transport/client/server, public gRPC server, peer gRPC server, admin server, metrics, test hooks, and pprof. New production-gate validation must happen before any listener or serving surface is opened. [Source: internal/cmd/app.go]
- `internal/admin/server.go` currently serves `/healthz`, optional `/metrics`, optional test hooks, optional pprof, and eviction/admin handlers. `/healthz` returns upload pressure and eviction lifecycle data. Add security-mode fields without removing or renaming current fields. [Source: internal/admin/server.go]
- `internal/scrapctl/status.go` currently decodes admin `/healthz` into `Health` and renders status/upload-pressure output. Add health fields here so `scrapctl status` inherits server truth rather than duplicating policy. [Source: internal/scrapctl/status.go]
- `internal/scrapctl/doctor.go` currently checks host/Kubernetes/NodePort/admin health readiness for prod-like cells. Add a production-readiness check that can fail development/test mode distinctly, but do not make `admin.health` fail merely because upload/eviction health is OK and mode is non-production. [Source: internal/scrapctl/doctor.go]
- `internal/scrapctl/evidencebundle/bundle.go` currently writes `config.json`, emits an admin evidence marker through `/healthz`, and writes `logs/evidence-probe-health.json`. Preserve this flow and add security mode only as bounded evidence. [Source: internal/scrapctl/evidencebundle/bundle.go]
- `deploy/kustomize/environments/prodlike/statefulset-prodlike-patch.yaml` currently sets `SCRAP_CELL_ID=kind-prodlike`, `SCRAP_ENVIRONMENT=prodlike`, and `SCRAP_PPROF_ENABLED=true`, with no `SCRAP_TEST_HOOKS`. Do not infer production security mode from `SCRAP_ENVIRONMENT`. [Source: deploy/kustomize/environments/prodlike/statefulset-prodlike-patch.yaml]

### Architecture Guardrails

- `internal/cmd` owns startup validation, config defaults, dependency construction, TLS config loading, role-policy loading, and Transit/fake selection. It must not own per-request policy logic, Shard authority, or crypto primitives. [Source: _bmad-output/planning-artifacts/architecture.md#Authentication-and-Security]
- `internal/security` is the planned home for security mode invariants, mTLS principal parsing, role evaluation primitives, and rate-limit policy primitives. For this story, keep it focused on mode/startup gate primitives only. [Source: _bmad-output/planning-artifacts/architecture.md#Project-Structure-and-Boundaries]
- `internal/admin` owns admin health/status output, but not public Document authorization or peer membership authority. [Source: _bmad-output/planning-artifacts/architecture.md#Surface-Ownership]
- `internal/scrapctl` owns CLI request construction and display; it must not import Store, Shard, Backend, or encryption internals, and it must not be server-side enforcement. [Source: _bmad-output/planning-artifacts/architecture.md#Boundary-Matrix]
- Production and prod-like overlays must not enable unsafe test hooks except in the explicitly named prod-like E2E environment. [Source: _bmad-output/planning-artifacts/architecture.md#Deployment-Structure]

### Production Gate Classes

Production startup rejection classes for this story:

- `security_mode`: missing or invalid `SCRAP_SECURITY_MODE`.
- `tls_config`: missing/invalid required cert/key/client-CA config for a production surface.
- `role_policy`: missing/invalid role policy file or role name outside ADR 0019's role set.
- `peer_identity_policy`: missing/invalid Cell/Member identity policy.
- `transit_config`: missing production Transit config or fake Transit selected in production.
- `audit_config`: missing/invalid audit sink policy once production mode requires audit evidence.
- `rate_limit_config`: missing/invalid required surface budget config.
- `dangerous_hooks`: `SCRAP_TEST_HOOKS`, unsafe pprof, fault hooks, or future dangerous hooks enabled before the required break-glass policy, server-side enforcement, and audit path are active.

Error messages must name the class and env/config key, but must not include secret values, cert/key contents, Transit tokens, raw file contents, Document bytes, or raw Document identifiers.

### Testing Requirements

Follow TDD for this story. The first meaningful RED tests should be config/security tests, because the main risk is accidentally starting a serving Cell with unsafe production config.

Minimum focused tests:

- mode parser accepts only `production`, `development`, `test`;
- unset/invalid mode fails closed;
- production rejects missing TLS config before listeners are opened;
- production rejects invalid cert/key/CA inputs without logging contents;
- production rejects missing role policy and peer identity policy;
- production rejects missing Transit config or fake Transit selection;
- production rejects `SCRAP_TEST_HOOKS=true`;
- production rejects unsafe pprof/dangerous hooks without break-glass policy;
- development/test mode starts in existing smoke tests when explicitly configured;
- admin health and `scrapctl status` include security mode and production-readiness reason;
- `scrapctl doctor` reports non-production readiness as a distinct check;
- evidence bundle health/config output records security mode without forbidden data.

Use Go standard `testing` only. Do not add testify, gomock, gomega, or a new assertion/mocking dependency. Use `t.TempDir()`, `t.Setenv()`, local fixture files, and table tests where multiple invalid config classes share a pattern.

### Latest Technical Information

- OpenBao 2.5.4 is the current OpenBao 2.5.x release found during story creation on 2026-06-08; its release notes include security fixes. This story should not add an OpenBao dependency or call Transit, but production Transit config must not be treated as optional production readiness. Source: https://openbao.org/community/release-notes/2-5-0/
- OpenBao Transit remains an external cryptographic operation service. It does not store application data and supports datakey generation, key versioning, and rewrap behavior. Story 2.1 owns the real Transit boundary; this story only gates production config. Source: https://openbao.org/docs/secrets/transit/
- Current gRPC authentication guidance still separates transport authentication from application authorization. Use TLS/mTLS for authenticated transport, then enforce S.C.R.A.P. roles separately. Source: https://grpc.io/docs/guides/auth/
- In Go `crypto/tls`, server-side client certificate verification should use behavior equivalent to `tls.RequireAndVerifyClientCert` for production mTLS. Story 1.2 wires live mTLS credentials; Story 1.1 may parse and validate certificate inputs for startup gating. Source: https://pkg.go.dev/crypto/tls
- The local repo pins `google.golang.org/grpc v1.81.1` and `google.golang.org/protobuf v1.36.11`; do not upgrade dependencies for this story. [Source: go.mod]

### Research / Reuse Notes

- GitHub repository/code search found no reusable `SCRAP_SECURITY_MODE` implementation or compelling external Go template to adopt.
- No new library is needed for Story 1.1. Use Go standard library packages such as `crypto/tls`, `crypto/x509`, `encoding/json`, `errors`, `fmt`, `os`, and `strings`, plus existing repo packages.
- Reuse existing env parsing style from `internal/cmd/config.go`, health rendering from `internal/admin`, and CLI/evidence structs from `internal/scrapctl`.

### Out of Scope

- Do not implement public, peer, admin, or `scrapctl` live mTLS enforcement. Story 1.2 owns that.
- Do not implement role authorization or peer identity enforcement. Story 1.3 owns that.
- Do not add audit event schema/sinks or rate limiters beyond startup config gate placeholders. Story 1.4/1.5 own those.
- Do not implement OpenBao clients, fake Transit, envelope metadata, encrypted writes/reads, or rewrap. Epic 2 owns those.
- Do not change storage identity, add `tenant_id` to storage identity, change Backend object identity, or alter Block/Frame layout.
- Do not change the gRPC/protobuf public API unless a later story explicitly requires it.
- Do not start Phase 5 cold-only read behavior.

### Implementation Notes for the Dev Agent

- Recommended first new files: `internal/security/mode.go`, `internal/security/mode_test.go`, and possibly `internal/security/startup_gate.go` if validation needs to stay reusable and focused.
- Recommended updates: `internal/cmd/config.go`, `internal/cmd/config_test.go`, `internal/cmd/app.go` or `internal/cmd/run.go` for pre-listener validation, `internal/admin/server.go`, `internal/admin/server_test.go`, `internal/scrapctl/status.go`, `internal/scrapctl/doctor.go`, `internal/scrapctl/*_test.go`, `internal/scrapctl/evidencebundle/*`, and relevant Kustomize manifests.
- Keep the security mode status data low-cardinality: use values like `production`, `development`, `test`, `ready`, `not_ready`, and bounded reasons such as `non_production_security_mode` or `missing_tls_config`.
- Preserve current K8s liveness/readiness semantics. Production readiness for Phase 5 is a security gate, not the same thing as the existing `scrap.v1-readiness` serving probe.
- Treat startup-gate failures as returned errors, not logs plus continued startup. Log or return errors, not both.

## Project Structure Notes

The story aligns with the architecture route map:

- New shared primitives belong in `internal/security`.
- Runtime config and pre-serving validation belong in `internal/cmd`.
- Admin status exposure belongs in `internal/admin`.
- CLI display and evidence bundle capture belong in `internal/scrapctl`.
- Deployment mode defaults belong in `deploy/kustomize`.
- Evidence artifacts belong under `_bmad-output/implementation-artifacts/phase-4.5/evidence/startup-gates/` if this story creates any local evidence files.

No ADR is required if the implementation follows ADR 0019. Create or update an ADR only if the implementation changes the accepted production security mode, auth/encryption contract, dependency choices, wire protocol, storage format, or cross-package ownership boundary.

## References

- [Epics: Story 1.1 and Epic 1](../planning-artifacts/epics.md#story-11-production-security-mode-startup-gates)
- [Architecture: Security Mode and Startup Gates](../planning-artifacts/architecture.md#security-mode-and-startup-gates)
- [Architecture: Project Structure and Boundaries](../planning-artifacts/architecture.md#project-structure--boundaries)
- [PRD: Production Security Mode and Startup Gates](../planning-artifacts/prds/prd-scrap-2026-06-07/prd.md#41-production-security-mode-and-startup-gates)
- [ADR 0019: Production security boundary](../../docs/adr/0019-production-security-boundary.md)
- [ADR 0020: OpenBao envelope encryption contract](../../docs/adr/0020-openbao-envelope-encryption-contract.md)
- [Phase 4.5 implementation slices](../../docs/phase-4.5-security-implementation-slices.md)
- [Project context](../project-context.md)
- [GitHub issue #401](https://github.com/petabytecl/scrap/issues/401)
- [OpenBao 2.5.x release notes](https://openbao.org/community/release-notes/2-5-0/)
- [OpenBao Transit docs](https://openbao.org/docs/secrets/transit/)
- [gRPC authentication guide](https://grpc.io/docs/guides/auth/)
- [Go crypto/tls package](https://pkg.go.dev/crypto/tls)

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security` failed RED before implementation with missing `Mode`, `ParseMode`, and gate symbols.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security` passed after implementing `internal/security`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd` failed RED before cmd wiring with missing `Config.SecurityMode`.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl` failed RED before operator output wiring with missing `admin.WithSecurityStatus`, missing health fields, and missing doctor production-readiness failure.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/telemetry ./internal/cmd ./internal/scrapctl/evidencebundle` failed RED before telemetry resource wiring with missing security/readiness resource fields.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/admin ./internal/scrapctl` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle` passed.
- `make manifests-check` passed.
- `env GOCACHE=/tmp/scrap-v2-go-build make static` initially failed on new-code lint complexity/nolint/unparam issues; after refactor it passed with 0 issues.
- `env GOCACHE=/tmp/scrap-v2-go-build make tier1-check` passed, including static checks, `go test ./...`, race tests, integration tests, builds, and `govulncheck`.
- `git diff --check` passed.
- Code review found 5 patch findings; all were fixed and checked off in the Review Findings section.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/admin ./internal/scrapctl` passed after review fixes.
- `make manifests-check` passed after moving security mode out of the base manifest and adding explicit rendered-mode checks.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl/evidencebundle` passed after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build make static` initially caught `newApp` cognitive complexity after review fixes; extracting startup gate composition made static pass with 0 issues.
- `env GOCACHE=/tmp/scrap-v2-go-build make tier1-check` passed after review fixes, including static checks, `go test ./...`, race tests, integration tests, builds, and `govulncheck`.
- `git diff --check` passed after review fixes.

### Completion Notes List

- Story created by BMAD create-story workflow on 2026-06-08.
- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added package-local RED tests for explicit security modes, fail-closed missing/unknown mode parsing, production startup gate classes, and non-production production-readiness reason.
- Implemented focused `internal/security` primitives for mode parsing, bounded readiness output, typed startup gate errors, and production-only validation of TLS, role, peer identity, Transit, audit, rate-limit, and dangerous-hook inputs.
- Wired `SCRAP_SECURITY_MODE` through `internal/cmd`, required explicit mode parsing, captured production gate config from environment, and ran production gate validation before Backend, telemetry, Shard, listener, gRPC, admin, metrics, pprof, or test-hook setup.
- Tightened review-found startup gates: required TLS server names, validated client CA certificates and CA validity windows, checked referenced Transit token env presence without storing token values, compared peer identity policy with configured Cell/Member identity, and rejected null/empty audit and rate-limit policies.
- Added admin health security-mode/readiness fields, extended `scrapctl status`, and added a distinct `scrapctl doctor` `production.readiness` check while preserving ordinary serving health.
- Added bounded OpenTelemetry resource attributes for `scrap.security_mode` and production-readiness status/reason, and verified evidence bundle health capture includes those bounded fields.
- Set explicit non-production security modes in local/prod-like/evidence/test deployment overlays without adding `SCRAP_TEST_HOOKS` to the prod-like overlay, and kept base fail-closed by leaving security mode unset there.
- Verified no real hardcoded secrets were introduced; changed-file scan only found story text, env names, and existing test/localstack placeholder values.

### File List

- `_bmad-output/implementation-artifacts/1-1-production-security-mode-startup-gates.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `deploy/kustomize/base/statefulset.yaml`
- `deploy/kustomize/components/scrub-fast/statefulset-scrub-patch.yaml`
- `deploy/kustomize/components/stress-tuning/statefulset-stress-patch.yaml`
- `deploy/kustomize/environments/local/kustomization.yaml`
- `deploy/kustomize/environments/local/statefulset-security-mode-patch.yaml`
- `deploy/kustomize/environments/prodlike-e2e/statefulset-test-hooks-patch.yaml`
- `deploy/kustomize/environments/prodlike/statefulset-prodlike-patch.yaml`
- `internal/admin/server.go`
- `internal/admin/server_test.go`
- `internal/cmd/app.go`
- `internal/cmd/app_test.go`
- `internal/cmd/config.go`
- `internal/cmd/config_test.go`
- `internal/cmd/telemetry.go`
- `internal/cmd/telemetry_test.go`
- `internal/scrapctl/doctor.go`
- `internal/scrapctl/doctor_test.go`
- `internal/scrapctl/evidencebundle/bundle_test.go`
- `internal/scrapctl/status.go`
- `internal/security/doc.go`
- `internal/security/mode.go`
- `internal/security/mode_test.go`
- `internal/security/startup_gate.go`
- `internal/security/startup_gate_test.go`
- `internal/telemetry/resource.go`
- `internal/telemetry/resource_test.go`
- `scripts/check-kustomize-manifests.sh`

## Change Log

- 2026-06-08: Added security mode RED tests and `internal/security` startup gate primitives.
- 2026-06-08: Wired startup gates, operator outputs, telemetry/evidence visibility, deployment defaults, and completed validation for review.
- 2026-06-08: Fixed code-review findings, reran validation, and moved story to done.
