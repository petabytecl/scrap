---
baseline_commit: be714cb6fa9483b4c47e98316bc01c1757c9169b
created: 2026-06-12T00:39:25-04:00
---

# Story 4.1: Production Security Startup Gate

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want production `scrapd` startup to fail closed when required security config is missing or invalid,
so that unsafe Cells never serve production traffic.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-9 - Production security mode and surface boundaries.
- Governing ADRs: ADR 0019 and ADR 0024.
- Prior implementation intelligence: `_bmad-output/implementation-artifacts/1-1-production-security-mode-startup-gates.md` is an older Phase 4.5 story artifact whose code appears to be present in the current branch. Treat it as implementation intelligence only; it is not current Epic 4 tracker completion.
- Related current stories: Story 4.2 owns surface authorization, audit, and rate-limit side-effect evidence. Story 4.3 owns OpenBao-backed encrypted write/read behavior. Story 4.7 owns production security rehearsal closure with real mTLS/OpenBao evidence.
- Current baseline: Story 3.7 is done at `be714cb6fa9483b4c47e98316bc01c1757c9169b`. Epic 3 closure evidence still separates local/package proof from production OpenBao and real S3/IAM release gates.

## Acceptance Criteria

1. **AC-4.1.1 - Invalid production config fails before serving.** Given production mode lacks required TLS, role policy, peer identity policy, Transit config, or dangerous hook policy, when startup runs, then `scrapd` fails closed before serving public, peer, or admin traffic. Evidence proves no listener accepts production traffic after startup gate failure.
2. **AC-4.1.2 - Valid production config wires explicit security posture.** Given production mode has valid security config, when startup runs, then public, peer, admin, telemetry, and `scrapctl` paths are wired with explicit security posture. Evidence identifies the security config boundary and command.
3. **AC-4.1.3 - Startup errors are actionable and redacted.** Given startup errors are emitted, when evidence is captured, then messages are actionable and do not leak secrets, cert material, private paths, tokens, dependency logs, raw Document identifiers, or raw Backend keys. Evidence records the leak-scan command and result.
4. **AC-4.1.4 - Production never downgrades to unsafe defaults.** Given any required production security setting is absent, when startup evaluates defaults, then it does not fall back to development mode, plaintext mode, disabled auth, fake Transit, or local-only overrides. Evidence records one negative case for each required setting.

## Tasks / Subtasks

- [ ] Create the Story 4.1 evidence artifact before behavior changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md`.
  - [ ] Record baseline commit, evaluation timestamp, exact files reviewed, evidence owner, command, expected result, actual result, artifact status, and redaction proof.
  - [ ] Use strict result language per row: `PASS`, `CONCERNS`, or `FAIL`; do not use hybrid phrases like `PASS with concerns`.
  - [ ] If current code already satisfies a row, prove it with current tests or source evidence. Do not mark a row as pass from the old Story 1.1 artifact alone.

- [ ] Audit and reuse the existing startup gate implementation. (AC: 1-4)
  - [ ] Read and preserve `internal/security/startup_gate.go`, `internal/security/mode.go`, `internal/security/tls_config.go`, `internal/cmd/config.go`, `internal/cmd/app.go`, and `internal/cmd/tls.go`.
  - [ ] Reuse existing `security.ValidateStartupGates`, `security.ParseMode`, TLS builders, policy loaders, and `newApp` validation ordering unless a test proves a bug.
  - [ ] Confirm `newApp` validates production security gates before topology, Backend opening, telemetry construction, Shard opening, gRPC server construction, listeners, admin server creation, pprof, or test hooks.
  - [ ] Classify any missing live Transit readiness behavior explicitly. Story 4.1 must fail missing or contradictory Transit config and fake Transit; do not claim real OpenBao outage/sealed/unauthorized proof unless a current command proves it.

- [ ] Expand fail-before-serving proof for every required class. (AC: 1, 4)
  - [ ] Extend `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems` or add an equivalent table test covering missing TLS, role policy, peer identity policy, Transit config, audit policy, rate-limit policy, `SCRAP_TEST_HOOKS=true`, and `SCRAP_PPROF_ENABLED=true`.
  - [ ] The test must prove failure happens before serving surfaces are opened. Prefer deterministic construction-order assertions such as an invalid S3 Backend sentinel or injected listener/subsystem side-effect checks over sleeps or flaky port probing.
  - [ ] Include security mode defaults: unset, unknown, and malformed `SCRAP_SECURITY_MODE` fail closed and never imply development/test.
  - [ ] Preserve the existing development/test smoke behavior only when the mode is explicit and visible as non-production readiness.

- [ ] Keep startup gate validation broad and redacted. (AC: 1, 3, 4)
  - [ ] Preserve or extend negative tests in `internal/security` for TLS files, invalid PEM, mismatched key pair, invalid/expired CA, expired cert, wrong identity, missing role policy, invalid role, missing peer identity, contradictory peer identity, missing Transit config, HTTP Transit address, relative Transit path, missing Transit token env, fake Transit, invalid audit policy, invalid rate-limit policy, test hooks, and pprof.
  - [ ] Assert startup errors expose bounded classes and env/config keys, not absolute paths, cert/key material, token values, dependency logs, policy contents, or raw identifiers.
  - [ ] Keep TLS 1.3 and `tls.RequireAndVerifyClientCert` semantics through the shared TLS builders. Do not hand-roll per-surface TLS behavior.

- [ ] Prove valid production config wires the security posture. (AC: 2)
  - [ ] Reuse or extend `TestAppSecurityRuntimeLoadsProductionAuthorizer` to prove production public gRPC, peer gRPC, admin TLS, authorizer, audit sink, rate limiter, Transit, and telemetry security labels are explicitly configured.
  - [ ] Include `scrapctl` production client posture by testing existing production TLS requirements in `internal/scrapctl` rather than moving server-side enforcement into the CLI.
  - [ ] Confirm admin health/evidence surfaces show `security_mode=production` and production readiness only after startup gates pass; non-production remains `not_ready` with `non_production_security_mode`.

- [ ] Preserve package and authority boundaries. (AC: 1-4)
  - [ ] Keep startup composition and env parsing in `internal/cmd`.
  - [ ] Keep reusable security primitives in `internal/security`.
  - [ ] Keep public gRPC behavior in `internal/server`, peer checks in `internal/peer`, admin status in `internal/admin`, CLI display/client TLS loading in `internal/scrapctl`, and Transit operations in `internal/encryption`.
  - [ ] Do not change storage identity, Block/Frame layout, Backend object identity, protobuf wire contracts, Shard authority, Pebble Projection authority, or OpenBao bootstrap behavior for this story.
  - [ ] Do not introduce runtime dependencies, assertion libraries, mocking frameworks, package-level globals, or new telemetry labels unless an accepted ADR/story explicitly requires it.

- [ ] Record verification and leak scans. (AC: 1-4)
  - [ ] Run focused security and startup tests first, then affected package regression.
  - [ ] Run `git diff --check`.
  - [ ] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before code review because this story changes or verifies startup/security behavior.
  - [ ] Run credential and identifier leak scans over the new evidence artifact, this story, and touched code. Classify matches as forbidden, allowed test fixture, allowed policy vocabulary, or artifact prose.
  - [ ] If `make production-rehearsal-security` is not run, record it as skipped with closure impact. Do not claim production rehearsal readiness from package tests.

## Dev Notes

### Current State

- `CONTEXT.md` defines Cell and Member identity and forbids treating local non-production identity defaults as production ACK, peer, or admin gates.
- ADR 0019 requires production mTLS on public, peer, and admin surfaces; separate application authorization; explicit non-production escape hatches; startup fail-closed on missing cert/key/client CA, invalid role policy, peer identity gaps, dangerous hooks, and invalid mode; and independent rate limits.
- ADR 0024 requires production TLS 1.3 for SCRAP TLS builders and restart-based certificate rotation for this phase.
- `internal/security/startup_gate.go` already validates production TLS files, role policy path, peer identity policy, Transit config, audit policy, rate-limit policy, `SCRAP_TEST_HOOKS`, and pprof. It returns typed `StartupGateError` classes.
- `internal/security/mode.go` already parses only `production`, `development`, and `test`; unset/unknown modes fail closed.
- `internal/security/tls_config.go` already builds mTLS server/client configs with TLS 1.3 and server-side client certificate verification.
- `internal/cmd/config.go` already parses `SCRAP_SECURITY_MODE`, per-surface TLS env vars, role policy, peer identity policy, Transit env vars, audit/rate-limit policy paths, test hooks, and pprof into `Config.ProductionGates`.
- `internal/cmd/app.go` calls `validateStartupSecurityGates(cfg)` at the beginning of `newApp`, before Backend, telemetry, Shard, listeners, gRPC servers, or admin server construction.
- `internal/cmd/tls.go` already wires production authorizer, audit sink, rate limiter, Transit, public/peer gRPC TLS, and admin TLS. Non-production gets fake Transit and no authorizer unless explicit test controls are provided.
- Existing tests cover many primitive cases, but `internal/cmd/app_test.go` currently has only one app-level fail-before-subsystems test for missing TLS with an S3 Backend sentinel. This story should close the matrix and evidence gap.

### Previous Story Intelligence

- Old Story 1.1 review findings are important regression traps: keep base/prod overlays explicit, validate TLS identity and CA certificates, require Transit token env presence, compare peer identity policy with configured Cell/Member identity, and reject null/empty audit or rate-limit policies.
- Story 3.7 review showed closure artifacts must include current artifact status, exact proof commands/test names, scan counts or classifications, strict `PASS`/`CONCERNS`/`FAIL`, and unambiguous baseline scope.
- Security and evidence work must distinguish local/package proof, Tier 2/E2E proof, Tier 3 production-readiness proof, real OpenBao proof, and real S3/IAM proof. Do not blur these evidence classes.

### Implementation Guidance

- Start with evidence and tests. Most code likely already exists; the highest-risk failure mode is an unproven completion claim.
- Treat a missing production config value as fail-closed, not as "use development defaults".
- Keep errors useful but bounded. `security_mode: invalid SCRAP_SECURITY_MODE: must be production, development, or test` style is acceptable; raw paths, tokens, cert material, OpenBao dependency logs, and policy contents are not.
- For no-listener proof, prefer direct `newApp` construction with deterministic pre-listener sentinels. Avoid tests that rely on arbitrary sleeps, port reuse races, or probing after partial startup unless the test has deterministic synchronization and cleanup.
- If adding a live OpenBao readiness probe, keep it behind the existing `internal/encryption` OpenBao adapter and separate package/integration tests. Do not add a new OpenBao dependency for Story 4.1.
- `scrapctl` is not a server-side enforcement point. It loads client credentials, validates server certificates, renders status/doctor/evidence, and should reflect server truth.
- The production rehearsal target is evidence for later Epic 4 closure. Story 4.1 may reference it as future closure scope, but package tests alone must not claim production rehearsal readiness.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md` - story status, debug log, completion notes, review findings, and file list.
- `_bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md` - evidence matrix, commands, redaction checks, and remaining concerns.
- `internal/security/*_test.go` - startup gate, mode, TLS, and redaction tests if gaps remain.
- `internal/cmd/*_test.go` - full app construction ordering and production runtime wiring tests.
- `internal/admin/*_test.go`, `internal/scrapctl/*_test.go`, and `internal/scrapctl/evidencebundle/*_test.go` only if valid-production or non-production readiness evidence needs current coverage.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.

Likely avoid:

- `proto/`, `gen/`, Block/Frame code, Backend object key code, storage identity, Raft command shape, Shard read/write authority, OpenBao bootstrap CLI, encrypted write/read logic, durable rewrap, production deployment ownership, and release closure docs.

No ADR is required if the implementation follows ADR 0019 and ADR 0024. Create or update an ADR only if the implementation changes the production security contract, TLS/auth/encryption contract, dependency choices, wire protocol, storage format, or cross-package ownership boundary.

### Testing Requirements

Run focused gate tests first:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security -run 'TestParseMode|TestProductionStartupGates|TestBuildMTLS' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestLoadConfig|TestNewAppRejectsProductionSecurityGatesBeforeSubsystems|TestAppSecurityRuntimeLoadsProductionAuthorizer|TestAppSecurityRuntimeRejectsProductionFakeTransit' -count=1 -v
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/scrapctl/evidencebundle -run 'Security|Production|Readiness|TLS|Evidence|Status|Doctor' -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/admin ./internal/server ./internal/peer ./internal/scrapctl ./internal/scrapctl/evidencebundle ./internal/encryption -count=1
```

Run leak scans with patterns kept in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)'
rg -n --pcre2 "$cred_pattern" _bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md _bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md internal/security internal/cmd internal/admin internal/server internal/peer internal/scrapctl internal/encryption
rg -n --pcre2 "$identifier_pattern" _bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md _bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md internal/security internal/cmd internal/admin internal/server internal/peer internal/scrapctl internal/encryption
```

Run broad gates before review:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

If a command is skipped, record the skip reason and closure impact in the evidence artifact. Do not mark an AC as pass from intent alone.

### Latest Technical Information

- No new dependency or package-registry adoption is needed for Story 4.1. Reuse Go standard-library TLS/X.509 support and the repo's existing `internal/security`, `internal/cmd`, `internal/encryption`, `internal/admin`, and `internal/scrapctl` packages.
- The official OpenBao Transit docs for the current 2.5.x stream describe Transit as cryptography/encryption as a service that does not store application data sent to it. Story 4.1 uses that only as config/readiness context; encrypted write/read behavior remains Story 4.3. Source: https://openbao.org/docs/secrets/transit/
- The official OpenBao Transit API docs include encrypt, decrypt, rewrap, generate data key, rotate, and other Transit operations. Do not call these APIs directly from startup gate code unless an implementation test and existing adapter boundary justify a live readiness probe. Source: https://openbao.org/api-docs/secret/transit/
- OpenBao 2.5.4 release notes include security fixes in the 2.5.x stream. Story 4.1 should not change OpenBao version/dependency claims; re-check pinned repo dependencies before any later dependency change. Source: https://openbao.org/community/release-notes/2-5-0/
- The repo pins gRPC/protobuf in `go.mod`; do not upgrade dependencies for this story.

### References

- `CONTEXT.md` - Cell/Member identity, non-production visibility, OpenBao Transit boundary, and storage authority vocabulary.
- `_bmad-output/project-context.md` - package boundaries, testing rules, redaction rules, closure rules, and commit rules.
- `_bmad-output/planning-artifacts/epics.md` - Epic 4 and Story 4.1 acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-9 and acceptance/evidence matrix.
- `_bmad-output/planning-artifacts/architecture.md` - Security Mode and Startup Gates, Surface Ownership, claim-to-gate mapping, and #401 handoff.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - package ownership, evidence, privacy, and OpenBao bootstrap scope.
- `docs/adr/0019-production-security-boundary.md` - production security boundary.
- `docs/adr/0024-production-topology-and-peer-scope-policy.md` - TLS 1.3, peer Shard scope, and restart-based certificate rotation.
- `docs/phase-4.5-security-implementation-slices.md` - #401 startup gate slice context.
- `docs/production-rehearsal.md` - production security rehearsal scope and artifact handling.
- `_bmad-output/implementation-artifacts/1-1-production-security-mode-startup-gates.md` - old implementation intelligence and review traps.
- `_bmad-output/implementation-artifacts/3-7-backend-durability-and-cold-read-closure-evidence.md` - recent closure artifact quality pattern.
- `internal/security/startup_gate.go`
- `internal/security/mode.go`
- `internal/security/tls_config.go`
- `internal/cmd/config.go`
- `internal/cmd/app.go`
- `internal/cmd/tls.go`
- `internal/security/startup_gate_test.go`
- `internal/security/mode_test.go`
- `internal/cmd/app_test.go`
- `internal/cmd/authorization_test.go`

## Dev Agent Record

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

### Completion Notes List

### File List
