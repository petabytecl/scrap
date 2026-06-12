---
baseline_commit: e9148db5f9769a4f04876e94a1aaa4cf6d28326c
created: 2026-06-12T02:56:47-04:00
---

# Story 4.5: `scrapctl openbao bootstrap` Fresh Setup

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want `scrapctl openbao bootstrap` to initialize local/prod-like OpenBao Transit setup,
so that rehearsals do not depend on undocumented scripts.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-14 - `scrapctl` OpenBao bootstrap for local/prod-like operator workflows.
- Source slice: `docs/phase-4.5-security-implementation-slices.md` deferred OpenBao bootstrap follow-up.
- Governing decision: DG-4 in the master architecture - `scrapctl` owns OpenBao bootstrap helper workflows only, not production OpenBao deployment, secret custody, storage backend setup, HA topology, or lifecycle.
- Governing ADR: ADR 0023 - use `github.com/openbao/openbao/api` as the only application-level OpenBao client; do not add raw OpenBao HTTP calls or shell out to undocumented scripts.
- Current baseline: Story 4.4 is done at `e9148db5f9769a4f04876e94a1aaa4cf6d28326c`; durable rewrap implementation, review fixes, leak scans, `make check`, and OpenBao adapter integration were committed and pushed before this story was created.
- Related future stories: Story 4.6 owns compatible rerun and incompatible-state fail-closed semantics. Story 4.7 owns final prod-like real mTLS/OpenBao production security rehearsal and evidence-bundle aggregation.

## Acceptance Criteria

1. **AC-4.5.1 - Fresh target bootstrap succeeds and emits redacted evidence.** Given a fresh supported local/prod-like OpenBao target, when `scrapctl openbao bootstrap` runs, then it initializes or unseals as configured, mounts Transit, creates or verifies the S.C.R.A.P. Transit key, and emits redacted evidence. Evidence identifies the CLI command, environment, and artifact path.
2. **AC-4.5.2 - Sensitive values never leave the secret boundary.** Given OpenBao returns sensitive values, when output, logs, reports, or tracker-ready evidence are produced, then root tokens, unseal keys, Transit tokens, private keys, client cert material, wrapped keys, and raw dependency logs are excluded. Evidence records stdout, stderr, report, log, and artifact redaction checks.
3. **AC-4.5.3 - Official OpenBao Go API client only.** Given the command uses OpenBao APIs, when dependencies are reviewed, then it uses the official OpenBao Go API client rather than shelling out to undocumented scripts. Evidence records the dependency and changed-boundary list.

## Tasks / Subtasks

- [x] Create the Story 4.5 evidence artifact before behavior changes. (AC: 1-3)
  - [x] Create `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`.
  - [x] Record baseline commit, timestamp, files reviewed, exact commands, expected/actual results, redaction scans, source links, changed-boundary list, and the split from Story 4.6/4.7.
  - [x] Use strict result language per row: `PASS`, `CONCERNS`, or `FAIL`; do not mark an AC pass from design intent alone.

- [x] Add `scrapctl openbao bootstrap` command routing. (AC: 1, 3)
  - [x] Update `internal/scrapctl/run.go` usage and `runCommand` to route `scrapctl openbao bootstrap`.
  - [x] Add focused files under `internal/scrapctl/` such as `openbao.go`, `openbao_bootstrap.go`, and `openbao_bootstrap_test.go`; keep `cmd/scrapctl/main.go` as an entrypoint only.
  - [x] Preserve existing `scrapctl` common flag behavior (`--timeout`, `--output`, TLS defaults) where it applies, but do not make S.C.R.A.P. admin TLS flags stand in for OpenBao server TLS unless the implementation validates that boundary explicitly.

- [x] Implement fresh bootstrap with explicit configuration and no raw secret flags. (AC: 1, 2)
  - [x] Support address, mount path, key name, key type, key derivation, timeout, output format, and evidence path as validated flags/env inputs.
  - [x] Read tokens and unseal key shares only from named environment variables, not from raw CLI flag values. Prefer `--token-env` and repeatable `--unseal-key-env` style inputs over `--token` or `--unseal-key`.
  - [x] For an uninitialized target, require an explicit init mode and an explicit init-secret output path before calling `Sys().InitWithContext`; write returned root token/unseal material only to that 0600 secret file and never to stdout, stderr, logs, or evidence.
  - [x] For an initialized sealed target, call `Sys().UnsealWithOptionsWithContext` with env-provided shares until seal status reports unsealed or a bounded failure occurs.
  - [x] For an initialized unsealed target, require a token from the configured token env unless the current process just initialized the target and is using the freshly returned root token in memory.
  - [x] Fail closed with a bounded actionable reason when required env vars, init-secret path, address, mount path, key name, token, or unseal material are missing.

- [x] Mount Transit and create/verify the S.C.R.A.P. Transit key through the official client. (AC: 1, 3)
  - [x] Use `github.com/openbao/openbao/api` methods such as `Sys().InitStatusWithContext`, `Sys().InitWithContext`, `Sys().SealStatusWithContext`, `Sys().UnsealWithOptionsWithContext`, `Sys().MountWithContext`, `Sys().ListMountsWithContext`, and `Logical().WriteWithContext`.
  - [x] Create or verify a Transit mount with type `transit`; default to `transit` unless the operator supplies another validated mount path.
  - [x] Create or verify the S.C.R.A.P. Transit key; default to `scrap-documents`, `aes256-gcm96`, and `derived=true` to match `internal/encryption` and `test/integration/testinfra/openbao`.
  - [x] Treat deep compatible-rerun checks and incompatible-state mutation/failure proof as Story 4.6 scope. Story 4.5 may verify the mount/key immediately after creation or in a fresh already-ready target, but must not claim full idempotency closure.
  - [x] Do not shell out to `bao`, `vault`, `curl`, `kubectl`, or local scripts for OpenBao API operations.

- [x] Emit redacted operator output and evidence. (AC: 1, 2)
  - [x] Text and JSON output must include status, bounded phase results, OpenBao address identity reduced to a safe endpoint summary, Transit mount/key names when safe, evidence artifact path, and next action.
  - [x] The CLI-generated evidence report must include command name, sanitized args, environment variable names used, environment class (`local`, `prod-like`, or explicit operator value), artifact path, result, phase statuses, dependency/client version, and redaction scan results.
  - [x] Output, errors, evidence, and any logs must exclude root tokens, unseal keys, Transit tokens, private keys, client cert contents, wrapped keys, raw OpenBao response bodies, raw dependency logs, and unbounded paths.
  - [x] Redaction must be explicit for bootstrap; do not rely only on `diagnosticTextValue` because it redacts any word containing `key` and may hide safe operator labels while missing structured secret fields.

- [x] Preserve package, authority, and security boundaries. (AC: 1-3)
  - [x] Keep bootstrap behavior in `internal/scrapctl` or a small CLI-owned sub-boundary. Do not import Store, Shard, Backend, admin internals, or production encryption lifecycle authority.
  - [x] OpenBao client types must not flow into Shard, Backend, server, admin, public API, peer API, or generated protobuf contracts.
  - [x] Do not change storage format, wire protocol, Document identity, Backend object identity, Raft commands, envelope metadata format, production startup gates, or admin/public/peer authorization behavior in this story.
  - [x] Do not add a new OpenBao dependency or upgrade the existing module unless the story first records why ADR 0023's current dependency cannot satisfy the bootstrap API calls.

- [x] Add focused tests before implementation and integration proof after implementation. (AC: 1-3)
  - [x] Unit-test option parsing, env-secret loading, missing-config failures, init-secret file permissions, no raw secret flags, text/JSON output redaction, evidence report redaction, and official-client dependency boundaries.
  - [x] Add an integration test that exercises the `scrapctl openbao bootstrap` command against an OpenBao container without using the existing fixture's hidden `bootstrapTransit` as the behavior under test.
  - [x] Keep Testcontainers setup separate from the operator command implementation. The test may start OpenBao, but the command must perform mount/key bootstrap itself.

- [x] Update story, evidence, and tracker artifacts. (AC: 1-3)
  - [x] Update this story with debug logs, completion notes, review findings, and file list.
  - [x] Update `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md` with final AC matrix rows, command evidence, and redaction scan classification.
  - [x] Move `_bmad-output/implementation-artifacts/sprint-status.yaml` to `review` only when implementation and local verification are complete.
  - [x] Do not mark Story 4.6 idempotency/incompatible-state or Story 4.7 production rehearsal complete from Story 4.5 tests.

### Review Findings

- [x] [Review][Patch] Init secret material could be destroyed by `--evidence-path` and `--init-secrets-path` collision [internal/scrapctl/openbao.go:299]
- [x] [Review][Patch] Fresh init path did not prove or handle sealed-after-init unseal before Transit mount/key work [internal/scrapctl/openbao.go:380]
- [x] [Review][Patch] Integration evidence used OpenBao dev mode and did not test init/sealed unseal behavior [test/integration/openbao_bootstrap_scrapctl_test.go:25]
- [x] [Review][Patch] Evidence report lacked sanitized args, env var names, and dependency version fields required by the story [internal/scrapctl/openbao_report.go:39]
- [x] [Review][Patch] stderr/log redaction checks were recorded as pass without checking the actual emitted surfaces [internal/scrapctl/openbao_report.go:118]
- [x] [Review][Patch] Generic error sanitizer did not guard common root/unseal/token/key leakage shapes [internal/scrapctl/openbao.go:701]
- [x] [Review][Patch] Existing Transit key verification accepted missing type/derived metadata as compatible [internal/scrapctl/openbao.go:539]
- [x] [Review][Patch] Existing evidence files could keep permissive permissions after report rewrite [internal/scrapctl/openbao_report.go:172]
- [x] [Review][Patch] Evidence path control characters could forge text output lines [internal/scrapctl/openbao.go:612]
- [x] [Review][Patch] S.C.R.A.P. admin/public TLS flags were accepted on OpenBao bootstrap without configuring OpenBao TLS [internal/scrapctl/openbao.go:594]
- [x] [Review][Patch] Init secret write was not fsynced before continuing after one-time init material was generated [internal/scrapctl/openbao_report.go:218]
- [x] [Review][Patch] Slash-bearing Transit key names were accepted but reinterpreted as multiple path segments [internal/scrapctl/openbao.go:582]

## Dev Notes

### Current State

- `CONTEXT.md` defines OpenBao Transit as the encryption substrate and keeps S.C.R.A.P. a Document gateway, not an S3-compatible object store. Use Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, and OpenBao Transit terms exactly.
- FR-14 requires `scrapctl` OpenBao bootstrap for local/prod-like operator workflows. Production OpenBao deployment, secret custody, storage backend setup, HA topology, and lifecycle remain platform-owned.
- `docs/phase-4.5-security-implementation-slices.md` explicitly says the bootstrap command must initialize, unseal, mount Transit, create the S.C.R.A.P. key through the official OpenBao Go API client, emit redacted evidence, and stay separate from Testcontainers integration fixtures.
- ADR 0023 requires `github.com/openbao/openbao/api` as the only application-level OpenBao client and forbids raw OpenBao HTTP calls unless a future ADR explains why the official client cannot model an operation.
- Architecture says `internal/scrapctl` owns operator CLI UX, request construction, evidence display, and client credential loading; it must not become server-side enforcement, storage authority, or Shard/Backend/encryption lifecycle authority.

### Existing Code To Reuse

- `internal/scrapctl/run.go` owns command dispatch, usage text, common flags, defaults, output format validation, command timeout helpers, and dependency defaults.
- `internal/scrapctl/output.go` owns current JSON writer and diagnostic text redaction helpers. Reuse the writer pattern, but add bootstrap-specific redaction for structured OpenBao outputs.
- `internal/scrapctl/tls.go` owns S.C.R.A.P. admin/public HTTP mTLS client handling. Reuse patterns for validation and fail-closed behavior only after deciding whether OpenBao TLS config is the same boundary or needs separate flags.
- `internal/scrapctl/evidence.go` and `internal/scrapctl/evidencebundle/` show evidence command shape, env-default parsing, stdout bundle path behavior, and redacted evidence expectations.
- `test/integration/testinfra/openbao/openbao.go` already uses `github.com/openbao/openbao/api`, `MaxRetries=0`, `Timeout=10s`, `Sys().MountWithContext`, `Logical().WriteWithContext`, default mount `transit`, default key `scrap-documents`, key type `aes256-gcm96`, and `derived=true`.
- `internal/encryption/openbao.go` validates OpenBao address/mount/key, sets `MaxRetries=0`, classifies provider errors into bounded S.C.R.A.P. errors, and keeps raw provider bodies out of returned errors. Reuse patterns, not storage encryption types.
- `go.mod` already pins `github.com/openbao/openbao/api v1.100.0-development20240408.0.20240723142009-4164d19a925c`; `go doc` confirms that module exposes the required `Sys` init, seal, unseal, mount, and logical write APIs.

### Previous Story Intelligence

- Story 4.4 review tightened evidence language and leak-scan audit trails. Keep exact command scopes and final counts in the evidence artifact.
- Story 4.4 kept local OpenBao/bootstrap UX out of rewrap closure. This story should close the bootstrap UX only and avoid reopening durable rewrap or encrypted write/read behavior.
- Story 4.3 and 4.4 used OpenBao container evidence with `openbao/openbao:2.5.4`. Continue using current official-release context unless a newer verified source requires a change.
- Recent commits keep story creation, implementation/evidence, and review-fix commits separated. Commit and push this story before implementation.

### Scope Boundaries

In scope:

- `scrapctl openbao bootstrap` command group and fresh-target flow.
- CLI config/env validation, safe init/unseal/mount/key bootstrap, output/evidence redaction, and focused tests.
- Operator evidence proving the command, environment, artifact path, official client usage, and redaction checks.

Out of scope:

- Full idempotent rerun behavior for compatible pre-existing state.
- Incompatible-state failure matrix and unsafe-mutation proof.
- Production OpenBao deployment, HA topology, long-term secret custody, storage backend setup, and lifecycle.
- Production security rehearsal closure with real mTLS/OpenBao gates and final evidence-bundle aggregation.
- Any storage, Raft, Backend, public/peer/admin wire-contract, or envelope metadata changes.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md` - story status, debug log, completion notes, review findings, and file list.
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md` - AC matrix, command evidence, redaction checks, official-client proof, and remaining Story 4.6/4.7 scope.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` - status transitions.
- `internal/scrapctl/run.go` - usage text and `openbao` command routing.
- `internal/scrapctl/openbao*.go` - bootstrap options, official client adapter boundary, redaction, evidence report, and rendering.
- `internal/scrapctl/openbao*_test.go` - CLI unit tests.
- `test/integration/openbao_bootstrap_scrapctl_test.go` - command-level OpenBao container proof.
- `cmd/scrapctl/*_test.go` only if binary-level behavior needs coverage.

Likely avoid:

- `internal/shard`, `internal/backend`, `internal/server`, `internal/peer`, `internal/admin`, `internal/block`, `proto/`, `gen/`, deployment overlays, production security rehearsal scripts, and release closure docs.

No ADR is required if the implementation follows DG-4 and ADR 0023. Create or update an ADR only if the implementation changes dependency choices, security/auth contracts, production OpenBao ownership, storage format, wire protocol, or cross-package ownership boundaries.

### Testing Requirements

Run focused CLI tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Redact|Secret|Evidence' -count=1 -v
```

Run binary/CLI package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1
```

Run OpenBao command integration when Docker/Testcontainers are available:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationScrapctlOpenBaoBootstrapFreshSetup -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1
```

Run formatting and broad gate before code review:

```bash
git diff --check
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans with patterns kept in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|root [t]oken|unseal [k]ey|/shards/|/tmp/|/home/)'
strict_value_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey|[sb]\\.[A-Za-z0-9_-]{20,})'
scan_scope='_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md _bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md internal/scrapctl cmd/scrapctl test/integration/openbao_bootstrap_scrapctl_test.go'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$identifier_pattern" $scan_scope
rg -n --pcre2 "$strict_value_pattern" $scan_scope
```

## Latest Tech Information

- OpenBao 2.5.x `/sys/init` docs state that initialization returns root and unseal material; bootstrap evidence and output must treat all init responses as sensitive and keep them out of stdout, stderr, logs, and evidence. Source: https://openbao.org/api-docs/system/init/
- OpenBao 2.5.x `/sys/unseal` docs require repeated unseal key submissions until the threshold is reached; implementation should handle progress and bounded failure without printing key shares. Source: https://openbao.org/api-docs/system/unseal/
- OpenBao 2.5.x `/sys/mounts` docs cover enabling secrets engines at a path; use the official client's `Sys().MountWithContext` with type `transit`. Source: https://openbao.org/api-docs/system/mounts/
- OpenBao Transit docs define key creation at `/transit/keys/:name` and support `aes256-gcm96` plus `derived=true`; these match the current repo defaults. Source: https://openbao.org/api-docs/secret/transit/
- OpenBao 2.5.x release notes show `v2.5.4` released May 20, 2026 and include security fixes in 2.5.x. The repo's integration fixture currently uses `openbao/openbao:2.5.4`; keep it unless a deliberate upgrade is made. Source: https://openbao.org/community/release-notes/2-5-0/
- `pkg.go.dev` and local `go doc` confirm the official client exposes `InitWithContext`, `InitStatusWithContext`, `SealStatusWithContext`, `UnsealWithOptionsWithContext`, `MountWithContext`, `ListMountsWithContext`, and `Logical().WriteWithContext`. Source: https://pkg.go.dev/github.com/openbao/openbao/api
- GitHub code/repo search for reusable `scrapctl openbao bootstrap` or OpenBao init/unseal/mount bootstrap implementations did not surface an adoptable implementation. Exa research returned official OpenBao client/docs material rather than a project-specific implementation to port. Reuse local V2 `internal/scrapctl` and OpenBao fixture patterns.

### References

- `CONTEXT.md` - domain vocabulary and V2 process constraints.
- `_bmad-output/project-context.md` - Go style, package boundaries, testing rules, and critical security/privacy rules.
- `_bmad-output/planning-artifacts/epics.md` - Story 4.5 acceptance criteria and Story 4.6/4.7 scope split.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-14 and operator-surface consequences.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-4, package boundary map, and `internal/scrapctl` OpenBao bootstrap ownership.
- `_bmad-output/planning-artifacts/architecture.md` - Phase 4.5 security, evidence, and redaction requirements.
- `docs/phase-4.5-security-implementation-slices.md` - deferred bootstrap follow-up language.
- `docs/adr/0023-openbao-api-client.md` - official OpenBao Go API client boundary.
- `docs/adr/0015-prodlike-kind-cell-cilium-and-gates.md` - `scrapctl` operator tool role in prod-like evidence.
- `docs/adr/0019-production-security-boundary.md` - `scrapctl` security mode, TLS, authz, and evidence expectations.
- `_bmad-output/implementation-artifacts/4-4-durable-envelope-rewrap-workflow.md` - previous story intelligence and completed security/encryption evidence style.
- `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md` - evidence matrix and leak-scan style.
- `internal/scrapctl/run.go`, `output.go`, `tls.go`, `evidence.go`, and `eviction.go` - existing CLI routing, output, TLS, evidence, and subcommand patterns.
- `internal/encryption/openbao.go` - official client configuration, path validation, retry disablement, bounded error classification, and redaction pattern.
- `test/integration/testinfra/openbao/openbao.go` - current OpenBao Testcontainers fixture and Transit mount/key defaults.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- 2026-06-12: Created initial Story 4.5 evidence artifact before behavior changes; AC rows remain `CONCERNS` pending implementation and verification.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Redact|Secret|Evidence' -count=1 -v` - PASS. Covered option parsing, missing token, init secret output, unseal env shares, text/JSON report redaction, raw secret flag rejection, address userinfo rejection, and evidence file redaction.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationScrapctlOpenBaoBootstrapFreshSetup -count=1 -v` - PASS with Docker server `29.5.2` and `openbao/openbao:2.5.4`.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration` - PASS with `0 issues`.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after splitting the OpenBao bootstrap implementation into focused files under the 800-line file limit.
- Final credential, identifier, and strict shaped-value leak scans over the story, evidence artifact, touched `scrapctl` code, and bootstrap integration test - PASS with 0 filtered strict shaped-value matches.
- BMAD code review: Blind Hunter, Edge Case Hunter, and Acceptance Auditor reported actionable patch findings; no decision-needed or deferred findings remained after triage.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Redact|Secret|Evidence' -count=1 -v` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run TestIntegrationScrapctlOpenBaoBootstrapFreshSetup -count=1 -v` - PASS after changing the integration to start an uninitialized OpenBao file-storage server and exercise `--init`.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration` - PASS after review fixes with `0 issues`.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after review fixes.

### Completion Notes List

- Created `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md` with baseline scope, files reviewed, initial AC matrix, source evidence notes, and explicit Story 4.6/4.7 exclusions.
- Added `scrapctl openbao bootstrap` routing, validation, operator output, and evidence report generation inside the `internal/scrapctl` boundary.
- Implemented official OpenBao client bootstrap operations for init status, init, seal status, unseal, Transit mount verification/creation, and Transit key verification/creation without shelling out to `bao`, `vault`, `curl`, or local scripts.
- Kept sensitive values behind the secret boundary: token and unseal shares are read only from named env vars, init root/unseal material is written only to an explicit 0600 secret file, and output/evidence/errors run bootstrap-specific forbidden-value checks.
- Added focused unit tests plus an OpenBao container integration test that starts a plain dev target and verifies the command-created Transit mount and S.C.R.A.P. key through the official client.
- Preserved Story 4.5 scope boundaries; Story 4.6 still owns full compatible rerun and incompatible-state proof, and Story 4.7 still owns final prod-like production security rehearsal closure.
- Addressed code-review findings by reserving and fsyncing the init secret sink before OpenBao init, using returned init shares for in-memory unseal, rejecting evidence/init path collisions, enforcing 0600 evidence mode, recording sanitized args/env vars/dependency version, checking actual stderr/log redaction surfaces, rejecting missing key metadata, rejecting OpenBao-irrelevant admin TLS flags, and making the integration test exercise an uninitialized OpenBao server.

### File List

- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `cmd/scrapctl/main_test.go`
- `internal/scrapctl/doctor_test.go`
- `internal/scrapctl/run.go`
- `internal/scrapctl/openbao.go`
- `internal/scrapctl/openbao_bootstrap_test.go`
- `internal/scrapctl/openbao_client.go`
- `internal/scrapctl/openbao_report.go`
- `test/integration/openbao_bootstrap_scrapctl_test.go`

### Change Log

- 2026-06-12: Implemented `scrapctl openbao bootstrap`, added official OpenBao client unit/integration coverage, closed Story 4.5 evidence with PASS rows, and moved the story to review.
- 2026-06-12: Addressed Story 4.5 BMAD code-review findings and moved story to done.
