---
story: 4.1
story_key: 4-1-production-security-startup-gate
created: 2026-06-12T00:43:04-04:00
story_creation_baseline_commit: be714cb6fa9483b4c47e98316bc01c1757c9169b
implementation_baseline_commit: 93bd9ffc08bdda4bece3e223488561e31d222ebe
status: pass
---

# Epic 4 Production Security Startup Gate Evidence

## Scope

This artifact records Story 4.1 evidence for production `scrapd` startup gates.
It is not Epic 4 closure evidence and does not claim Story 4.2 authorization,
Story 4.3 encrypted write/read, Story 4.5/4.6 OpenBao bootstrap, Story 4.7
production rehearsal, real OpenBao outage drills, or final V2 release readiness.

## Baseline

| Field | Value |
| --- | --- |
| Story creation baseline | `be714cb6fa9483b4c47e98316bc01c1757c9169b` |
| Implementation baseline | `93bd9ffc08bdda4bece3e223488561e31d222ebe` |
| Initial evidence timestamp | `2026-06-12T00:43:04-04:00` |
| Tracker state at start | `4-1-production-security-startup-gate: in-progress` |
| Evidence owner | Story 4.1 |

## Files Reviewed

| File | Purpose | Initial status |
| --- | --- | --- |
| `CONTEXT.md` | Cell/Member identity, non-production visibility, OpenBao Transit and storage authority vocabulary. | Reviewed before implementation. |
| `_bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md` | Authoritative Story 4.1 requirements and task order. | Reviewed before implementation. |
| `_bmad-output/planning-artifacts/epics.md` | Epic 4 and AC-4.1.1 through AC-4.1.4 source. | Reviewed during story creation. |
| `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` | FR-9 and acceptance/evidence matrix. | Reviewed during story creation. |
| `_bmad-output/planning-artifacts/architecture.md` | Security Mode and Startup Gates, Surface Ownership, and #401 handoff. | Reviewed during story creation. |
| `docs/adr/0019-production-security-boundary.md` | Production security boundary. | Reviewed during story creation. |
| `docs/adr/0024-production-topology-and-peer-scope-policy.md` | TLS 1.3 and peer scope policy. | Reviewed during story creation. |
| `internal/security/startup_gate.go` | Startup gate validation implementation. | Reviewed before behavior changes. |
| `internal/security/mode.go` | Security mode parsing/readiness implementation. | Reviewed before behavior changes. |
| `internal/security/tls_config.go` | mTLS server/client builders. | Reviewed before behavior changes. |
| `internal/cmd/config.go` | `scrapd` env/config parsing and production gate assembly. | Reviewed before behavior changes. |
| `internal/cmd/app.go` | `newApp` construction ordering and listener setup. | Reviewed before behavior changes. |
| `internal/cmd/tls.go` | Runtime authorizer, audit, rate-limit, TLS, and Transit wiring. | Reviewed before behavior changes. |
| `internal/security/startup_gate_test.go` | Existing startup gate negative tests. | Reviewed before behavior changes. |
| `internal/security/mode_test.go` | Existing mode and missing-class tests. | Reviewed before behavior changes. |
| `internal/cmd/app_test.go` | Existing app startup and pre-subsystem test. | Reviewed before behavior changes. |
| `internal/cmd/authorization_test.go` | Existing production runtime wiring tests. | Reviewed before behavior changes. |

## Current Evidence Decision

| Area | Decision | Reason |
| --- | --- | --- |
| Story 4.1 startup gate evidence | `PASS` | Current focused tests, affected package regression, and leak scans prove the Story 4.1 config/startup-gate scope. Live OpenBao outage/sealed/unauthorized readiness and production rehearsal are not claimed here. |

## Acceptance Criteria Matrix

| AC | Evidence required | Current proof command or source | Decision | Concern or gap |
| --- | --- | --- | --- | --- |
| AC-4.1.1 | Production startup fails before public, peer, or admin traffic when required config is missing. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run TestNewAppRejectsProductionSecurityGatesBeforeSubsystems -count=1 -v` | `PASS` | App-level matrix covers missing security mode, TLS, role policy, peer identity policy, Transit config, audit policy, rate-limit policy, test hooks, and pprof before the invalid Backend sentinel can be reached. |
| AC-4.1.2 | Valid production config wires public, peer, admin, telemetry, and `scrapctl` explicit security posture. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestLoadConfig|TestNewAppRejectsProductionSecurityGatesBeforeSubsystems|TestAppSecurityRuntimeLoadsProductionAuthorizer|TestAppSecurityRuntimeRejectsProductionFakeTransit' -count=1 -v`; `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/scrapctl/evidencebundle -run 'Security|Production|Readiness|TLS|Evidence|Status|Doctor' -count=1 -v` | `PASS` | Production runtime wiring, admin security status, `scrapctl` TLS posture, doctor readiness, and evidencebundle security report paths all have current passing tests. |
| AC-4.1.3 | Startup errors are actionable and redacted. | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security -run 'TestParseMode|TestProductionStartupGates|TestBuildMTLS' -count=1 -v`; credential and identifier scans below. | `PASS` | Startup gate tests assert bounded classes and no TLS/Transit path/value leaks for invalid inputs; scans found only policy/test vocabulary, fixture labels, and bounded identifier terms. |
| AC-4.1.4 | Missing production settings do not fall back to development/plaintext/disabled auth/fake Transit/local-only overrides. | `TestParseModeRejectsUnsetOrUnknownMode`, `TestLoadConfigRejectsBadInput`, `TestProductionStartupGatesRejectMissingClassesIndependently`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems`, `TestAppSecurityRuntimeRejectsProductionFakeTransit`. | `PASS` | Unset/unknown mode fails closed, production missing classes return bounded security errors, and fake Transit is rejected in production. |

## Required Setting Matrix

| Required setting | Expected fail-closed class | Current source/test anchor | Decision | Concern or gap |
| --- | --- | --- | --- | --- |
| Security mode missing or invalid | `security_mode` | `TestParseModeRejectsUnsetOrUnknownMode`, `TestLoadConfigRejectsBadInput`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_security_mode` | `PASS` | Current tests prove no implicit development/test fallback. |
| Public, peer, admin, and `scrapctl` TLS | `tls_config` | `TestProductionStartupGatesRejectInvalidTLSInputsWithoutPathLeak`, `TestBuildMTLSServerConfigRequiresVerifiedClientCertificates`, `TestBuildMTLSClientConfigPresentsCertificateAndVerifiesServer`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_tls` | `PASS` | Current tests prove required TLS config, TLS 1.3 builder behavior, mTLS client verification, and app-level fail-before-subsystems. |
| Role policy | `role_policy` | `TestProductionStartupGatesRejectMissingClassesIndependently/role_policy`, `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/unknown_role`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_role_policy` | `PASS` | Missing and invalid role policy fail closed before later startup. |
| Peer identity policy | `peer_identity_policy` | `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/incomplete_peer_identity`, `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/contradictory_peer_identity`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_peer_identity_policy` | `PASS` | Missing, incomplete, and contradictory Cell/Member identity policy fail closed. |
| Transit config and token env | `transit_config` | `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/fake_transit`, `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/missing_transit_token_secret`, `TestAppSecurityRuntimeRejectsProductionFakeTransit`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_transit_config` | `PASS` | Config shape, token env, HTTPS address, path validation, and fake Transit rejection are proved. Live OpenBao outage/sealed/unauthorized proof is not claimed by Story 4.1. |
| Audit policy | `audit_config` | `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/invalid_audit_policy_json`, `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/empty_audit_policy_json`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_audit_policy` | `PASS` | Missing and invalid audit policy fail closed before later startup. |
| Rate-limit policy | `rate_limit_config` | `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/invalid_rate_limit_policy_json`, `TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs/null_rate_limit_policy_json`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/missing_rate_limit_policy` | `PASS` | Missing and invalid rate-limit policy fail closed before later startup. |
| `SCRAP_TEST_HOOKS=true` | `dangerous_hooks` | `TestProductionStartupGatesRejectMissingClassesIndependently/test_hooks`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/test_hooks_enabled` | `PASS` | Production test hooks cannot pass startup gates. |
| `SCRAP_PPROF_ENABLED=true` | `dangerous_hooks` | `TestProductionStartupGatesRejectMissingClassesIndependently/pprof`, `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems/pprof_enabled` | `PASS` | Production pprof cannot pass startup gates without future break-glass/audit enforcement. |

## No-Listener Proof

| Surface | Required proof | Current decision | Concern or gap |
| --- | --- | --- | --- |
| Public gRPC | Startup gate failure occurs before `clientLis` is opened or served. | `PASS` | `TestNewAppRejectsProductionSecurityGatesBeforeSubsystems` now covers every required missing class using a valid placement file and an invalid S3 Backend sentinel. A missed startup gate would continue past security validation and return the wrong error class before any listener proof could pass. |
| Peer gRPC | Startup gate failure occurs before `peerLis` is opened or served. | `PASS` | Same app-level matrix proves failure before `newApp` reaches listener construction. |
| Admin HTTP/TLS | Startup gate failure occurs before admin server is constructed or served. | `PASS` | Same app-level matrix proves failure before admin server construction and serving. |
| Telemetry/operator evidence | Valid config carries explicit security posture; invalid config cannot claim ready posture. | `PASS` | Focused `internal/cmd`, `internal/admin`, `internal/scrapctl`, and `internal/scrapctl/evidencebundle` tests passed. |
| `scrapctl` | CLI requires production TLS and reflects server readiness; it is not server-side enforcement. | `PASS` | `TestStatusInProductionRequiresScrapctlTLS`, `TestStatusUsesMTLSClientCredentials`, `TestStatusReportsSecurityModeFields`, and doctor/evidence tests passed under the focused command. |

## Redaction And Leak Scan Log

| Scan | Command | Current result | Decision | Classification |
| --- | --- | --- | --- | --- |
| Credential-shaped terms | `rg --count-matches --pcre2 '(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)' _bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md _bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md internal/security internal/cmd internal/admin internal/server internal/peer internal/scrapctl internal/encryption` | 459 matches. Strict literal token scan for `AKIA...`, `ghp_...`, and `xox...` returned no matches. | `PASS` | Matches are expected security policy vocabulary, authz/authorization identifiers, OpenBao token configuration names, test fixture labels, and prose. No hardcoded credential value found. |
| Raw identifier/path terms | `rg --count-matches --pcre2 '([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|/shards/|/tmp/|/home/)' _bmad-output/implementation-artifacts/4-1-production-security-startup-gate.md _bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md internal/security internal/cmd internal/admin internal/server internal/peer internal/scrapctl internal/encryption` | 217 matches. | `PASS` | Matches are expected bounded field names, policy vocabulary, fixture paths in tests, documented `/tmp`/`/home` command examples, and certificate/test helper terms. No new runtime raw identifier exposure introduced. |

## Verification Log

| Command | Result | Notes |
| --- | --- | --- |
| Evidence artifact creation | `PASS` | Artifact created before production code behavior changes. |
| `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run TestNewAppRejectsProductionSecurityGatesBeforeSubsystems -count=1 -v` | `PASS` | New app-level matrix covers security mode, TLS, role policy, peer identity policy, Transit, audit, rate limits, test hooks, and pprof. |
| `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security -run 'TestParseMode|TestProductionStartupGates|TestBuildMTLS' -count=1 -v` | `PASS` | Mode parsing, production startup gate classes, TLS redaction, policy/Transit validation, and mTLS builders passed. |
| `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/cmd -run 'TestLoadConfig|TestNewAppRejectsProductionSecurityGatesBeforeSubsystems|TestAppSecurityRuntimeLoadsProductionAuthorizer|TestAppSecurityRuntimeRejectsProductionFakeTransit' -count=1 -v` | `PASS` | Config default rejection, app-level gate matrix, production runtime wiring, and fake Transit rejection passed. |
| `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/scrapctl/evidencebundle -run 'Security|Production|Readiness|TLS|Evidence|Status|Doctor' -count=1 -v` | `PASS` | Admin security status, `scrapctl` TLS/status/doctor, and evidencebundle security checks passed. |
| `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/security ./internal/cmd ./internal/admin ./internal/server ./internal/peer ./internal/scrapctl ./internal/scrapctl/evidencebundle ./internal/encryption -count=1` | `PASS` | Affected package regression passed. |
| `git diff --check` | `PASS` | Whitespace/diff hygiene passed before broad validation. |
| `env GOCACHE=/tmp/scrap-v2-go-build make check` | `PASS` | Ran golangci-lint fmt diff, package-boundaries, buf lint/generate diff, golangci-lint run with 0 issues, `go test ./...`, `go test -race ./...`, integration-tagged tests, and `scrapd`/`scrapctl` builds. |
| Credential-shaped term scan | `PASS` | 459 expected vocabulary matches; no strict literal AWS/GitHub/Slack token match. |
| Raw identifier/path term scan | `PASS` | 217 expected bounded vocabulary, fixture, and command-example matches; no new runtime raw identifier exposure introduced. |
| `make production-rehearsal-security` | `SKIPPED` | Not required for Story 4.1 package/startup-gate scope. Production rehearsal, real mTLS/OpenBao outage drills, and final Epic 4 closure remain Story 4.7 scope and are not claimed by this artifact. |
