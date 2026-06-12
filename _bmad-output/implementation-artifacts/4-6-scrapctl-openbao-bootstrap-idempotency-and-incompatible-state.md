---
baseline_commit: 6ecde6efc1032ea68bb482e03304de55769d3bf5
created: 2026-06-12T03:47:54-04:00
---

# Story 4.6: `scrapctl openbao bootstrap` Idempotency and Incompatible State

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a platform operator,
I want OpenBao bootstrap to be idempotent for compatible state and fail closed for incompatible state,
so that rehearsal setup can be repeated safely.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirement: FR-14 - `scrapctl` OpenBao bootstrap for local/prod-like operator workflows.
- Governing decision: DG-4 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - `scrapctl` owns local/prod-like OpenBao bootstrap helper workflows only.
- Governing ADR: ADR 0023 - use `github.com/openbao/openbao/api` as the only application-level OpenBao client.
- Current baseline: Story 4.5 is done at `6ecde6efc1032ea68bb482e03304de55769d3bf5`; fresh setup, init/unseal, redaction, official-client use, and review fixes have been committed and pushed.
- Related future story: Story 4.7 owns final production security rehearsal and evidence-bundle aggregation. This story must link 4.5 fresh setup evidence but must not claim production OpenBao lifecycle or HA readiness.

## Acceptance Criteria

1. **AC-4.6.1 - Compatible rerun succeeds without unsafe mutation.** Given Transit and the S.C.R.A.P. key already exist with compatible settings, when bootstrap reruns, then the command succeeds without unsafe mutation. Evidence proves the existing compatible state is preserved.
2. **AC-4.6.2 - Incompatible state fails closed and is not repaired in place.** Given existing OpenBao state is incompatible, when bootstrap runs, then it fails closed with an actionable, redacted reason. Evidence proves incompatible state is not mutated into an unsafe configuration.
3. **AC-4.6.3 - Bootstrap slice closure is explicit.** Given bootstrap evidence is reviewed, when closure is evaluated, then fresh setup, idempotency, incompatible-state failure, and redaction evidence are linked. Closure records `PASS`, `CONCERNS`, or `FAIL` for the bootstrap slice.

## Tasks / Subtasks

- [x] Create the Story 4.6 evidence artifact before behavior changes. (AC: 1-3)
  - [x] Create `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`.
  - [x] Include baseline commit, timestamp, exact commands, expected and actual results, source links, redaction scans, changed-boundary list, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [x] Link Story 4.5 fresh setup evidence and keep Story 4.7 production rehearsal evidence as future scope.

- [x] Prove compatible rerun preservation in unit tests. (AC: 1)
  - [x] Extend the local fake OpenBao bootstrap client with operation counters or immutable call snapshots for `MountTransit`, `CreateTransitKey`, `ReadTransitKey`, `ListMounts`, and token changes.
  - [x] Seed an initialized, unsealed target with `transit/` mount type `transit` and `scrap-documents` key metadata `type=aes256-gcm96`, `derived=true`, `latest_version>=1`.
  - [x] Run `scrapctl openbao bootstrap` once against the compatible state and assert status `ok`, phases include verified mount/key reasons, and `MountTransit`/`CreateTransitKey` were not called.
  - [x] Assert stdout, stderr, evidence JSON, and report redaction checks still pass and do not leak token or unseal material.

- [x] Prove compatible rerun preservation against real OpenBao. (AC: 1, 3)
  - [x] Add or extend integration coverage so an uninitialized file-storage OpenBao target is bootstrapped once, then rerun against the same target using the saved root token through `--token-env`.
  - [x] Capture mount and key metadata before and after the second run using the official OpenBao Go API client, not shell commands.
  - [x] Assert the second run succeeds, reports verified existing state, preserves mount type, key type, derived setting, and latest version, and does not require `--init`.
  - [x] Keep Testcontainers setup outside command implementation; the command remains the behavior under test.

- [x] Prove incompatible state fails closed in unit tests. (AC: 2)
  - [x] Cover an existing mount at the configured path with non-`transit` type; assert failure reason is actionable and redacted, `MountTransit` is not called, and key operations are not attempted.
  - [x] Cover an existing Transit key with wrong type; assert failure reason is `existing transit key type is incompatible` or an equivalently bounded reason and `CreateTransitKey` is not called.
  - [x] Cover an existing Transit key with `derived=false` when the requested configuration expects `derived=true`; assert failure reason is bounded and no repair mutation is attempted.
  - [x] Preserve Story 4.5 review regressions: missing type metadata, missing derived metadata, and `latest_version<1` must remain incompatible.

- [x] Prove incompatible state is not mutated against real OpenBao where practical. (AC: 2, 3)
  - [x] Add an integration test that seeds an incompatible-but-readable OpenBao state, then runs `scrapctl openbao bootstrap` and verifies the same incompatible state remains afterward.
  - [x] Prefer one real OpenBao case that OpenBao supports deterministically, such as `transit/` with `scrap-documents` created as a different Transit key type, or `scrap-documents` created with `derived=false`.
  - [x] Optionally add a second real mount-conflict case by mounting a non-Transit secrets engine at `transit/`, if OpenBao's API and test setup make the before/after assertion deterministic.
  - [x] Record any real-OpenBao incompatible case that cannot be exercised as a `CONCERNS` row with reason, not as a hidden pass.

- [x] Update evidence/report behavior only if current fields cannot prove AC-4.6. (AC: 1-3)
  - [x] Reuse existing phase reasons (`transit mount verified`, `transit key verified`, bounded incompatibility reasons) when they are sufficient.
  - [x] If adding report fields, keep them machine-readable, low-cardinality, redacted, and scoped to `internal/scrapctl`.
  - [x] Do not add raw OpenBao response bodies, dependency logs, root tokens, unseal keys, Transit tokens, private keys, client cert contents, wrapped keys, file paths beyond explicit artifact paths, or unbounded provider errors.

- [x] Preserve package, authority, and security boundaries. (AC: 1-3)
  - [x] Keep implementation under `internal/scrapctl` plus focused tests in `internal/scrapctl` and `test/integration`.
  - [x] Use only the official OpenBao Go API client through the existing bootstrap client boundary.
  - [x] Do not import Store, Shard, Backend, server, peer, admin internals, or production encryption lifecycle authority into `internal/scrapctl`.
  - [x] Do not change storage format, wire protocol, Document identity, Backend object identity, Raft commands, envelope metadata format, public/peer/admin contracts, or deployment overlays.
  - [x] No ADR is required if scope stays within DG-4 and ADR 0023. Create an ADR only for dependency choice, OpenBao lifecycle ownership, security/auth contract, storage format, wire protocol, or cross-package boundary changes.

- [x] Update story, evidence, and tracker artifacts. (AC: 1-3)
  - [x] Update this story with debug log references, completion notes, review findings, and file list.
  - [x] Update `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md` with the final AC matrix and redaction scan results.
  - [x] Move `_bmad-output/implementation-artifacts/sprint-status.yaml` to `review` only after implementation and local verification are complete.
  - [x] After code review fixes, move Story 4.6 to `done`; do not move Story 4.7 or Epic 4 to done from this story.

## Dev Notes

### Current State

- `internal/scrapctl/openbao.go` already implements the bootstrap state machine: parse/validate options, initialize when requested, unseal, apply token, verify or mount Transit, verify or create the Transit key, then emit report/evidence.
- Compatible mount behavior already exists in `ensureOpenBaoTransitMount`: if `ListMounts` returns the configured mount path with type `transit`, the command records `transit mount verified` and does not call `MountTransit`.
- Incompatible mount behavior already exists: if the mount exists with any type other than `transit`, the command fails with a bounded `existing mount is not transit` reason before key work.
- Compatible key behavior already exists in `ensureOpenBaoTransitKey`: if `ReadTransitKey` returns matching type, derived setting, and valid latest version, the command records `transit key verified` and does not call `CreateTransitKey`.
- Incompatible key behavior already exists in `validateOpenBaoTransitKey`: missing/wrong type, missing/wrong derived setting, or `latest_version<1` fail closed.
- Story 4.6 should therefore extend coverage and evidence first. Change production code only where the tests expose a real gap.

### Existing Code To Reuse

- `internal/scrapctl/openbao.go` - bootstrap options, validation, state machine, safe errors, path/key validation, and mount/key decision logic.
- `internal/scrapctl/openbao_client.go` - official OpenBao API client adapter, `MaxRetries=0`, mount listing, key read/create, and path escaping.
- `internal/scrapctl/openbao_report.go` - evidence/report rendering, redaction checks, 0600 evidence writes, init secret sink, and dependency evidence.
- `internal/scrapctl/openbao_bootstrap_test.go` - fake client, unit tests, report assertions, redaction helpers.
- `test/integration/openbao_bootstrap_scrapctl_test.go` - current uninitialized OpenBao file-storage integration test for fresh setup.
- `test/integration/testinfra/openbao/openbao.go` - current dev-mode fixture and defaults (`openbao/openbao:2.5.4`, mount `transit`, key `scrap-documents`, type `aes256-gcm96`, `derived=true`).
- `internal/encryption/openbao.go` - official-client configuration and bounded OpenBao error-classification patterns. Reuse patterns only; do not couple bootstrap to storage encryption internals.

### Previous Story Intelligence

- Story 4.5 review fixes must stay intact:
  - Init secret output is reserved with `O_EXCL`, written with `0600`, fsynced, and parent directory synced.
  - Fresh init uses returned unseal shares in memory to unseal before mount/key operations.
  - `--evidence-path` and `--init-secrets-path` collision is rejected.
  - Existing evidence files are rewritten as `0600`.
  - Evidence/init paths with control characters are rejected.
  - S.C.R.A.P. admin/public TLS flags are rejected for OpenBao bootstrap because they do not configure OpenBao TLS.
  - Reports include sanitized args, env var names, dependency version, and actual redaction checks for stdout, stderr, report, artifact, and logs.
  - Sensitive token/key-shaped error text is redacted without hiding safe operator labels such as `token env BAO_TOKEN is required`.
  - Transit key names are single path segments; mount paths may contain multiple safe segments.
  - The integration test starts an uninitialized file-storage OpenBao server rather than relying on dev-mode pre-bootstrap.
- Do not regress Story 4.5's strict split: fresh setup is complete, but idempotency, incompatible-state no-mutation proof, and final production rehearsal closure are separate slices.

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `internal/scrapctl/openbao.go` only if tests reveal behavior gaps.
- `internal/scrapctl/openbao_bootstrap_test.go`
- `test/integration/openbao_bootstrap_scrapctl_test.go`
- `internal/scrapctl/openbao_report.go` only if evidence fields need narrow extension.

Avoid:

- `internal/shard`, `internal/backend`, `internal/server`, `internal/peer`, `internal/admin`, `internal/block`, `proto/`, `gen/`, deployment overlays, storage/encryption lifecycle changes, and production rehearsal closure docs.

### Testing Requirements

Run focused CLI tests:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Idempot|Incompatible|Redact|Evidence' -count=1 -v
```

Run binary/CLI package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1
```

Run OpenBao command integration when Docker/Testcontainers are available:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run 'TestIntegrationScrapctlOpenBaoBootstrap(FreshSetup|CompatibleRerun|IncompatibleState)' -count=1 -v
```

Run affected package regression:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1
```

Run formatting/static gates:

```bash
git diff --check
go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration
```

Run broad gate before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, and touched code. Keep patterns in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|root [t]oken|unseal [k]ey|/shards/|/tmp/|/home/)'
strict_value_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey|[sb]\.[A-Za-z0-9_-]{20,})'
scan_scope='_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md _bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md internal/scrapctl cmd/scrapctl test/integration/openbao_bootstrap_scrapctl_test.go'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$identifier_pattern" $scan_scope
rg -n --pcre2 "$strict_value_pattern" $scan_scope
```

### Latest Tech Information

- OpenBao 2.5.x `/sys/mounts` docs list and enable secrets engines; mount entries expose a `type` field that bootstrap can compare with `transit`. Source: https://openbao.org/api-docs/system/mounts/
- OpenBao 2.5.x Transit `/keys/:name` docs say key type and `derived` are set at creation, and key reads return metadata including type, derived, versions, and capability fields. Source: https://openbao.org/api-docs/secret/transit/
- OpenBao 2.5.4 was released May 20, 2026 with security fixes. The repo fixture already uses `openbao/openbao:2.5.4`; keep that fixture unless a deliberate upgrade is made. Source: https://openbao.org/community/release-notes/2-5-0/
- `pkg.go.dev` and local `go doc` confirm the official client exposes `Sys().InitWithContext`, `Sys().MountWithContext`, `Logical().ReadWithContext`, and `Logical().WriteWithContext`. Source: https://pkg.go.dev/github.com/openbao/openbao/api
- GitHub code/repo search did not return an adoptable `scrapctl openbao bootstrap` implementation. Reuse the local `internal/scrapctl` bootstrap boundary and OpenBao test fixtures.

### References

- `CONTEXT.md` - domain vocabulary and V2 process constraints.
- `_bmad-output/project-context.md` - Go style, package boundaries, testing rules, and critical security/privacy rules.
- `_bmad-output/planning-artifacts/epics.md` - Story 4.6 acceptance criteria and Story 4.5/4.7 scope split.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-4 OpenBao bootstrap ownership and evidence requirements.
- `_bmad-output/planning-artifacts/architecture.md` - security boundary, operator CLI, evidence, redaction, and package-boundary rules.
- `docs/adr/0023-openbao-api-client.md` - official OpenBao Go API client boundary.
- `docs/adr/0019-production-security-boundary.md` - production security boundary, `scrapctl` client-side config expectations, and non-production escape hatch rules.
- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md` - previous story implementation and review-fix intelligence.
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md` - fresh setup AC matrix and redaction evidence style.
- `internal/scrapctl/openbao.go`, `openbao_client.go`, `openbao_report.go`, and `openbao_bootstrap_test.go` - existing bootstrap implementation and tests.
- `test/integration/openbao_bootstrap_scrapctl_test.go` and `test/integration/testinfra/openbao/openbao.go` - current OpenBao integration setup.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation. The implementation agent should append the execution model used during dev-story.

### Debug Log References

- 2026-06-12: Story 4.6 created from sprint status after Story 4.5 implementation, review fixes, `make check`, commit, and push completed.
- 2026-06-12: Created initial Story 4.6 evidence artifact before test/code changes; AC rows remain `CONCERNS` pending implementation and verification.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao.*(Compatible|Incompatible)' -count=1 -v` - RED: failed on missing fake-client call counters and phase assertion helper.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao.*(Compatible|Incompatible)' -count=1 -v` - PASS after adding fake-client operation counters and compatible/incompatible unit coverage.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run 'TestIntegrationScrapctlOpenBaoBootstrap(FreshSetup|CompatibleRerun|IncompatibleState)' -count=1 -v` - RED: compatible/incompatible tests reached expected OpenBao states, but shared redaction helper incorrectly required every run to be a successful init run.
- `env GOCACHE=/tmp/scrap-v2-go-build go test -tags integration ./test/integration -run 'TestIntegrationScrapctlOpenBaoBootstrap(FreshSetup|CompatibleRerun|IncompatibleState)' -count=1 -v` - PASS after narrowing the helper to redaction checks and asserting status/init per test.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl -run 'OpenBao|Bootstrap|Idempot|Incompatible|Redact|Evidence' -count=1 -v` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./cmd/scrapctl ./internal/scrapctl -count=1` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./cmd/scrapctl ./internal/encryption -count=1` - PASS.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./internal/scrapctl ./cmd/scrapctl ./test/integration` - initial `gocognit` failure fixed by extracting a per-case helper; rerun PASS with `0 issues`.
- `git diff --check` - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS.
- Final leak scans over story, evidence artifact, touched `scrapctl` code, and bootstrap integration test - PASS with 0 filtered strict shaped-value matches.
- BMAD code-review: Blind Hunter, Edge Case Hunter, and Acceptance Auditor subagents failed with usage-limit errors before returning findings; local fallback review of the Story 4.6 diff found no actionable patch, decision, or deferred findings.

### Change Log

- 2026-06-12: Added Story 4.6 unit and integration evidence for compatible OpenBao rerun preservation and incompatible-state fail-closed no-mutation behavior.

### Completion Notes List

- Story context created. Implementation pending.
- Initial idempotency/incompatible-state evidence scaffold created before behavior changes.
- Added unit proof that compatible existing Transit mount/key state verifies without calling mount/key creation.
- Added integration proof that a real OpenBao compatible rerun preserves mount/key metadata and does not rerun init.
- Added unit and integration proof that incompatible mount/key state fails closed without repair mutation; real OpenBao proof uses an incompatible Transit key type.
- Updated Story 4.6 evidence with PASS rows, verification commands, changed-boundary list, and redaction scan results.
- Completed local fallback code review after BMAD review subagents were unavailable due usage limits; no code changes were required from review.

### File List

- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
- `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`
- `internal/scrapctl/openbao_bootstrap_test.go`
- `test/integration/openbao_bootstrap_scrapctl_test.go`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
