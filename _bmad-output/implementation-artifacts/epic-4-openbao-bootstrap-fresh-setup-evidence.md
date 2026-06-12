---
story: 4.5-scrapctl-openbao-bootstrap-fresh-setup
status: done
created: 2026-06-12T03:00:18-04:00
story_context_baseline: e9148db5f9769a4f04876e94a1aaa4cf6d28326c
implementation_start_commit: a0aac0d5cdc9e382d398f5a6c419c710c3308374
owner: Coto
---

# Epic 4 OpenBao Bootstrap Fresh Setup Evidence

## Scope

Story 4.5 closes the fresh-target `scrapctl openbao bootstrap` workflow for FR-14. It proves:

- `scrapctl openbao bootstrap` can initialize or unseal a supported local/prod-like OpenBao target as configured;
- the command mounts Transit, creates or verifies the S.C.R.A.P. Transit key, and emits operator evidence;
- output, errors, reports, logs, and evidence exclude root tokens, unseal keys, Transit tokens, private keys, client cert material, wrapped keys, and raw dependency logs; and
- OpenBao API operations use the official `github.com/openbao/openbao/api` client rather than shelling out to undocumented scripts.

Story 4.5 does not claim full idempotent rerun behavior, incompatible-state failure closure, production OpenBao deployment/lifecycle ownership, or final prod-like real mTLS/OpenBao production security rehearsal. Story 4.6 owns idempotency and incompatible state. Story 4.7 owns final production rehearsal and evidence-bundle aggregation.

## Files Reviewed

- `CONTEXT.md`
- `_bmad-output/project-context.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`
- `_bmad-output/planning-artifacts/architecture.md`
- `docs/phase-4.5-security-implementation-slices.md`
- `docs/adr/0015-prodlike-kind-cell-cilium-and-gates.md`
- `docs/adr/0019-production-security-boundary.md`
- `docs/adr/0023-openbao-api-client.md`
- `_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md`
- `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md`
- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `cmd/scrapctl/main.go`
- `cmd/scrapctl/main_test.go`
- `internal/scrapctl/run.go`
- `internal/scrapctl/openbao.go`
- `internal/scrapctl/openbao_client.go`
- `internal/scrapctl/openbao_report.go`
- `internal/scrapctl/openbao_bootstrap_test.go`
- `internal/scrapctl/doctor_test.go`
- `internal/scrapctl/output.go`
- `internal/scrapctl/tls.go`
- `internal/scrapctl/evidence.go`
- `internal/scrapctl/eviction.go`
- `internal/encryption/openbao.go`
- `test/integration/openbao_bootstrap_scrapctl_test.go`
- `test/integration/testinfra/openbao/openbao.go`

## Coverage Matrix

| AC | Status | Current proof | Remaining evidence needed |
| --- | --- | --- | --- |
| AC-4.5.1 fresh target bootstrap succeeds and emits redacted evidence | PASS | `scrapctl openbao bootstrap` now validates address/env/mount/key/evidence inputs, supports explicit init with reserved/fsynced 0600 secret output, unseals from either env-provided shares or init-returned shares kept only in memory, mounts Transit, creates/verifies `scrap-documents`, writes a redacted evidence report, and renders text/JSON output. `TestOpenBaoBootstrapFreshInitializedTargetWritesEvidence`, `TestOpenBaoBootstrapInitializesAndWritesSecretFile0600`, `TestOpenBaoBootstrapUnsealsFromEnvironmentWithoutLeakingShares`, and `TestIntegrationScrapctlOpenBaoBootstrapFreshSetup` prove fresh initialized/unsealed, init-plus-unseal, sealed/unseal, and an uninitialized OpenBao container command path. | None for Story 4.5. Story 4.6 owns deep idempotent rerun and incompatible-state closure. |
| AC-4.5.2 sensitive values never leave the secret boundary | PASS | Unit tests prove root tokens, unseal shares, token env values, raw secret flags, URL userinfo, and init response material stay out of stdout, stderr, errors, reports, and evidence. Init material is written only to explicit 0600 `--init-secrets-path`; evidence rewrites enforce 0600. Reports now record sanitized args, env var names, dependency version, and redaction checks over actual stdout, stderr, report/artifact, and command log surfaces. | None for Story 4.5. Story 4.7 owns final prod-like full evidence-bundle leak aggregation. |
| AC-4.5.3 official OpenBao Go API client only | PASS | `internal/scrapctl/openbao_client.go` uses `github.com/openbao/openbao/api` for `InitStatusWithContext`, `InitWithContext`, `SealStatusWithContext`, `UnsealWithOptionsWithContext`, `ListMountsWithContext`, `MountWithContext`, and `Logical().Read/WriteWithContext`. Boundary scan found no new shell-based OpenBao operations; matches were existing generic command runners and the integration readiness probe. | None. |

## Source Evidence Notes

- `internal/scrapctl/run.go` currently routes `doctor`, `status`, `upload-pressure`, `peers`, `leader`, `fault`, `evidence`, and `eviction`; Story 4.5 must add `openbao bootstrap`.
- `internal/scrapctl/output.go` has generic diagnostic text redaction but intentionally redacts any value containing `key`; bootstrap needs structured redaction to preserve safe operator labels while excluding secret material.
- `internal/encryption/openbao.go` validates OpenBao address/mount/key, uses `baoapi.DefaultConfig()`, sets `MaxRetries=0`, sets a bounded timeout, and classifies provider errors without returning raw provider bodies.
- `test/integration/testinfra/openbao/openbao.go` proves the official client can mount Transit with `Sys().MountWithContext` and create `scrap-documents` using `Logical().WriteWithContext` with `type=aes256-gcm96` and `derived=true`.
- Local `go doc` confirms the pinned `github.com/openbao/openbao/api` module exposes `InitStatusWithContext`, `InitWithContext`, `SealStatusWithContext`, `UnsealWithOptionsWithContext`, `ListMountsWithContext`, `MountWithContext`, and `Logical().WriteWithContext`.
- `internal/scrapctl.openBaoBootstrapOptions` rejects raw secret flags by absence, rejects OpenBao address userinfo, and reads sensitive values only from named env vars.
- `internal/scrapctl.writeOpenBaoInitSecrets` writes init root token/unseal material only to an explicit operator-provided file with mode `0600`; evidence and operator output record only that the secret file was written.
- `test/integration/openbao_bootstrap_scrapctl_test.go` starts an uninitialized OpenBao file-storage server without the repo's pre-bootstrapped Transit fixture, then runs `scrapctl openbao bootstrap --init`, verifies init secret mode/redaction, and verifies the CLI-created `transit/` mount and `scrap-documents` key through the official client.

## Command Evidence

- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Redact|Secret|Evidence' -count=1 -v` - PASS. Covered option parsing, missing token, init secret output, unseal env shares, text/JSON report redaction, raw secret flag rejection, address userinfo rejection, and evidence file redaction.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS. Covered binary help output plus full `scrapctl` package regression.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS. Covered affected package regression after the official-client adapter and CLI changes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationScrapctlOpenBaoBootstrapFreshSetup -count=1 -v` - PASS. Testcontainers started an uninitialized `openbao/openbao:2.5.4` file-storage server; `scrapctl openbao bootstrap --init` initialized, wrote init material to a 0600 secret file, unsealed from returned shares in memory, mounted Transit, and created/verified `scrap-documents` without using the pre-bootstrapped Transit fixture.
- `rg -n 'exec\.Command|\bbao\b|\bvault\b|\bcurl\b|RawRequest|/v1/sys|/v1/transit' internal/scrapctl cmd/scrapctl test/integration/openbao_bootstrap_scrapctl_test.go` - PASS after classification. Matches were existing generic command runners, existing evidencebundle command adapters, test token variable names, and the integration health wait path; no new shell-based OpenBao operation or raw OpenBao HTTP client path was added.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after the file split. Covered formatter diff, package-boundary checks, buf lint/generate diff, golangci-lint with `0 issues`, `go test ./...`, `go test -race ./...`, integration tests including LocalStack and both OpenBao container tests, and `scrapd`/`scrapctl` builds.
- BMAD code review - PASS after fixes. Blind Hunter, Edge Case Hunter, and Acceptance Auditor findings were triaged into patch items; no decision-needed or deferred findings remained.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Redact|Secret|Evidence' -count=1 -v` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS after review fixes.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration` - PASS after review fixes with `0 issues`.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after review fixes. Covered formatter diff, package-boundary checks, buf lint/generate diff, golangci-lint with `0 issues`, `go test ./...`, `go test -race ./...`, integration tests including LocalStack and both OpenBao container tests, and `scrapd`/`scrapctl` builds.

## Leak Scan Evidence

Final scans covered `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md`, `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`, `internal/scrapctl`, `cmd/scrapctl`, and `test/integration/openbao_bootstrap_scrapctl_test.go`.

| Scan | Status | Classification |
| --- | --- | --- |
| credential vocabulary scan | PASS | 226 broad matches. Matches are allowed story/evidence/security-policy vocabulary, env var names, redaction test fixture names, OpenBao client references, and existing evidencebundle tests. No hardcoded credential values were found. |
| sensitive identifier vocabulary scan | PASS | 76 broad matches. Matches are allowed story/evidence prose, OpenBao bootstrap redaction tests, CLI label names, and security vocabulary. |
| strict shaped-value raw scan | PASS | 7 raw matches. All 7 are false positives from the OpenBao-style `[sb].` branch matching Go selectors: `signals.*` in existing evidencebundle tests and the OpenBao client factory selector in the new bootstrap code. |
| strict shaped-value filtered scan | PASS | 0 matches after excluding the selector false positives with `rg -v 'signals\.|deps\.OpenBaoClientFactory'`. No shaped cloud keys, GitHub tokens, Slack tokens, private-key blocks, AWS credential assignment forms, or OpenBao-style token values were found. |

## Final Decision

PASS. Story 4.5 acceptance criteria are closed for fresh local/prod-like `scrapctl openbao bootstrap` setup, official OpenBao client usage, and redacted command/evidence output. Story 4.6 remains open for full idempotent rerun and incompatible-state failure proof. Story 4.7 remains open for final prod-like real mTLS/OpenBao production security rehearsal and evidence-bundle aggregation.
