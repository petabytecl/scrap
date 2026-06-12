---
baseline_commit: 940008d5e59c89b54c7dcacea39c37e6901c4751
created: 2026-06-12T11:23:45-04:00
---

# Story 4.7: Production Security Rehearsal Evidence Closure

Status: ready-for-dev

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want production security, encryption, rewrap, and OpenBao bootstrap evidence linked,
so that Epic 4 cannot close from local happy-path security tests.

## Traceability

- Epic: Epic 4 - Operators Can Run Fail-Closed Security and OpenBao Workflows.
- Requirements: FR-9, FR-10, FR-14.
- Governing decisions: DG-4 and DG-5 in `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`.
- Governing ADRs and docs: ADR 0019 production security boundary, ADR 0023 OpenBao API client boundary, `docs/production-rehearsal.md`, `docs/prd-closure-policy.md`, and `docs/openbao-deployment-contract.md`.
- Current baseline: Stories 4.1 through 4.6 are done at `940008d5e59c89b54c7dcacea39c37e6901c4751`; each linked evidence artifact explicitly leaves final production rehearsal closure to Story 4.7.
- Related future scope: Story 6.6 / issue #429 owns real non-local S3/IAM production rehearsal closure. Story 4.7 can produce local production-security proof with filesystem Backend, but must not claim real S3/IAM readiness unless `make production-rehearsal` is run against real S3/IAM and linked.

## Acceptance Criteria

1. **AC-4.7.1 - Epic 4 evidence is linked with owners.** Given Epic 4 evidence is collected, when closure is evaluated, then startup fail-closed, authz, audit, rate limits, encrypted write/read, Transit outage, rewrap, bootstrap, idempotency, incompatible state, and redaction evidence are linked. Evidence records artifact paths and owning stories.
2. **AC-4.7.1a - Production rehearsal fail-closed drills are recorded.** Given Transit is unavailable, auth is denied, or key policy is wrong during production rehearsal, when security rehearsal runs, then the rehearsal records a fail-closed outcome without plaintext fallback or secret leakage. Evidence records the outage/drill artifact path.
3. **AC-4.7.2 - Rehearsal artifacts carry release-grade metadata.** Given `make production-rehearsal-security` runs, when artifacts are captured, then results include command, commit/ref, environment, expected result, actual result, artifact path, and redaction proof. Evidence proves LocalStack or local overrides are clearly marked when used.
4. **AC-4.7.3 - Closure fails when P0 evidence is missing.** Given any P0 security or secret-redaction evidence is missing, when closure is evaluated, then closure is `FAIL`, not deferred to Epic 6. Evidence records `PASS`, `CONCERNS`, or `FAIL` using V2 release gate language.

## Tasks / Subtasks

- [ ] Create the Epic 4 closure evidence artifact before script/code changes. (AC: 1-4)
  - [ ] Create `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`.
  - [ ] Record baseline commit, timestamp, command, commit/ref, environment, expected result, actual result, artifact path, redaction proof, and final `PASS`/`CONCERNS`/`FAIL` rows.
  - [ ] Link the existing Story 4.1 through 4.6 evidence artifacts and name the owning story for every linked claim.
  - [ ] Classify filesystem Backend, LocalStack, or other local/test endpoints as local/interim evidence. Do not mark them as real S3/IAM proof.

- [ ] Inventory prior Epic 4 evidence and closure gaps. (AC: 1, 4)
  - [ ] Link Story 4.1 startup fail-closed evidence: `_bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md`.
  - [ ] Link Story 4.2 public/peer/admin/`scrapctl` authz, audit, and rate-limit evidence: `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md`.
  - [ ] Link Story 4.3 encrypted write/read, Transit/key failure, OpenBao adapter, and redaction evidence: `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md`.
  - [ ] Link Story 4.4 durable rewrap, idempotency, interruption, and redaction evidence: `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md`.
  - [ ] Link Story 4.5 fresh OpenBao bootstrap, official-client use, and redaction evidence: `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md`.
  - [ ] Link Story 4.6 compatible rerun, incompatible-state no-mutation, and bootstrap slice closure evidence: `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md`.
  - [ ] Keep closure status `FAIL` or `CONCERNS` until the production-security rehearsal command and drill artifacts are actually present and leak-scanned.

- [ ] Bring `make production-rehearsal-security` artifacts up to AC-4.7 metadata. (AC: 2, 3)
  - [ ] Reuse `scripts/production-rehearsal.sh` and `Makefile` targets instead of creating a parallel rehearsal runner.
  - [ ] Extend the rehearsal report if needed so `artifacts/production-rehearsal/report.json` includes the invoked command, commit/ref, timestamp, environment name, Backend classification, OpenBao image, expected result, actual result, artifact/report paths, redaction proof, and local override classification.
  - [ ] Prefer routing OpenBao init/mount/key setup through the supported `scrapctl openbao bootstrap` path where practical. If the script keeps direct setup for a narrow reason, record the reason in evidence and keep DG-4 boundaries explicit.
  - [ ] Ensure report fields stay machine-readable, low-cardinality, and redacted. Do not include root tokens, unseal keys, Transit tokens, private keys, client cert material, wrapped keys, raw Document payloads, raw Backend keys, raw provider bodies, or raw logs.
  - [ ] Update `scripts/check-e2e-gates.sh` only when new rehearsal-report invariants need a structural guard.

- [ ] Add or validate production-rehearsal fail-closed drills. (AC: 2, 4)
  - [ ] Record a Transit unavailable drill in the production-rehearsal artifact path. The expected result is a failed write/read or readiness operation with no plaintext fallback and no secret leakage.
  - [ ] Record an OpenBao auth denied drill using a bounded/no-capability token or equivalent policy-safe setup. The expected result is a typed fail-closed denial without raw provider body, token, or key material leakage.
  - [ ] Record a wrong key policy, missing key, or incompatible key drill that proves the production path fails closed instead of creating unsafe state or falling back to plaintext.
  - [ ] Keep every drill deterministic and self-contained under `artifacts/production-rehearsal/`, which is ignored by Git.
  - [ ] If a drill cannot be safely exercised in this local rehearsal target, record `FAIL` or `CONCERNS` with the exact missing artifact; do not defer a P0 security gap to Epic 6.

- [ ] Verify redaction and no-overclaim behavior. (AC: 1-4)
  - [ ] Run leak scans over the story, closure evidence, rehearsal script, generated report schema tests, and any touched code.
  - [ ] Prove public tracker-ready evidence excludes tokens, private keys, generated certificate material, raw logs, raw Backend keys, raw Document payloads, wrapped keys, and raw dependency errors.
  - [ ] Ensure the closure artifact states exactly what the filesystem-backed security rehearsal proves and what remains for real S3/IAM.
  - [ ] Do not mark Epic 4 or release closure done if production-rehearsal artifacts are missing, local-only, or leak-scanning is incomplete.

- [ ] Preserve package, authority, and evidence boundaries. (AC: 1-4)
  - [ ] Expected touch points are `_bmad-output/implementation-artifacts`, `scripts/production-rehearsal.sh`, `scripts/check-e2e-gates.sh`, `Makefile` only if target wiring changes, and narrow tests or evidence parsing helpers if needed.
  - [ ] Keep OpenBao client behavior behind `internal/encryption` and `internal/scrapctl`; do not pass OpenBao client types into Shard, Backend, server, admin, or public API contracts.
  - [ ] Do not change Document identity, Backend object identity, storage format, Block/Frame layout, Raft command shape, envelope metadata format, public/peer/admin wire contracts, or production OpenBao lifecycle ownership.
  - [ ] Create an ADR only if the implementation changes deployment ownership, security/auth contracts, wire/storage format, dependency choices, or cross-package boundaries.

- [ ] Update story, evidence, and tracker artifacts. (AC: 1-4)
  - [ ] Move this story to `in-progress` when implementation starts and to `review` only after local verification is complete.
  - [ ] Update this story with debug log references, completion notes, review findings, and file list.
  - [ ] Update `_bmad-output/implementation-artifacts/sprint-status.yaml` through `review` and then `done` only after code review and fixes.
  - [ ] Do not move Epic 4 to `done` from this story unless every Epic 4 closure row is `PASS` and no required P0 evidence is missing.

## Dev Notes

### Current State

- `make production-rehearsal-security` exists and runs `scripts/production-rehearsal.sh run` with `SCRAP_PROD_REHEARSAL_BACKEND=fs`, built `scrapd`/`scrapctl`, and `openbao/openbao:2.5.4`.
- The current rehearsal script starts TLS OpenBao in Docker, initializes/unseals it, mounts Transit, creates `scrap-documents`, starts one production-mode `scrapd` Member, runs mTLS health/status/write/head/read checks, scans the local data directory for plaintext, forces a sealed Block upload, waits for a committed ConfirmUpload marker, and writes `artifacts/production-rehearsal/report.json`.
- Current `report.json` fields are: `status`, `security_mode`, `production_readiness_status`, `backend`, `openbao_image`, `openbao_transit`, `test_hooks_enabled`, `pprof_enabled`, `encrypted_write_read_ok`, `plaintext_leak_scan_ok`, `backend_upload_confirmed`, `confirmed_upload_count`, and `log_dir`.
- That report does not yet satisfy all AC-4.7.2 metadata. It does not include command, commit/ref, timestamp, expected/actual result details, explicit artifact path, drill rows, or explicit local-override classification.
- `scripts/check-e2e-gates.sh` already guards important production rehearsal invariants: production mode, real OpenBao marker, per-run Cell ID, upload trigger, streaming read verification, committed Backend upload confirmation, S3/security Backend target split, pinned OpenBao image, and rejection of `SCRAP_TRANSIT_FAKE`.
- Stories 4.1 through 4.6 each mark scoped evidence `PASS`, but all explicitly avoid claiming final production rehearsal closure.

### Existing Code To Reuse

- `scripts/production-rehearsal.sh` - current production security rehearsal runner and report writer.
- `Makefile` targets `production-rehearsal-security`, `production-rehearsal`, and `production-rehearsal-down`.
- `scripts/check-e2e-gates.sh` - structural gate for E2E, Tier, and production rehearsal target wiring.
- `docs/production-rehearsal.md` - operator-facing target contract and closure-use language.
- `docs/prd-closure-policy.md` - production rehearsal closure policy and real S3/IAM distinction.
- `docs/openbao-deployment-contract.md` - platform-managed OpenBao and evidence rules.
- `internal/scrapctl/openbao.go`, `openbao_client.go`, and `openbao_report.go` - supported `scrapctl openbao bootstrap` command and redacted evidence behavior.
- `internal/encryption/openbao.go` and `internal/encryption/*_test.go` - OpenBao adapter error taxonomy and redaction tests.
- `internal/scrapctl/evidencebundle` - evidence bundle parsing/rendering patterns if the closure artifact needs bundle ingestion.

### Previous Story Intelligence

- Story 4.1 proved startup gates and production fake-Transit rejection with package/integration evidence, but skipped `make production-rehearsal-security`.
- Story 4.2 tightened evidence-overclaim language and proved authz/audit/rate-limit behavior locally. Keep the same precision: package tests do not automatically become production rehearsal evidence.
- Story 4.3 proved encrypted write/read and Transit/key fail-closed behavior in local crypto paths and OpenBao adapter integration. Story 4.7 must add real production-mode rehearsal evidence instead of reusing the local crypto-path evidence as closure.
- Story 4.4 proved durable rewrap across Raft and local three-Member harnesses, but did not run production rehearsal.
- Story 4.5 added `scrapctl openbao bootstrap` with official OpenBao client use, 0600 init secret handling, sanitized args, env-name-only token references, and redaction checks.
- Story 4.6 proved compatible rerun preservation and incompatible-state no-mutation with unit and real OpenBao integration tests. Link it, but do not treat it as production OpenBao lifecycle or HA proof.
- The user explicitly requested commits and pushes before continuing to the next work. Keep story creation, implementation, and review-fix commits separated when practical.

### Latest Tech Information

- OpenBao 2.5.x Transit remains a cryptographic service: callers store encrypted data, and Transit does not become storage authority. Source: https://openbao.org/docs/secrets/transit/
- OpenBao `/sys/health` returns distinct initialized/unsealed/active, standby, uninitialized, and sealed statuses. Use those semantics for outage or sealed-state drills when needed. Source: https://openbao.org/api-docs/system/health/
- OpenBao Transit key `type` and `derived` are creation-time settings on `/transit/keys/:name`; incompatible key state should fail closed rather than be silently repaired. Source: https://openbao.org/api-docs/secret/transit/
- OpenBao policies constrain path capabilities. Use capability-limited tokens for auth-denied drills, and never paste token values or raw policy-secret material into evidence. Source: https://openbao.org/docs/concepts/policies/

### Project Structure Notes

Likely update during implementation:

- `_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md`
- `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `scripts/production-rehearsal.sh`
- `scripts/check-e2e-gates.sh`
- `docs/production-rehearsal.md` only if the operator contract changes.
- Narrow tests or helpers near existing script/evidence boundaries if needed.

Avoid:

- `internal/shard`, `internal/backend`, `internal/block`, `internal/server`, `internal/peer`, `internal/admin`, `proto/`, `gen/`, deployment overlays, storage/encryption wire contracts, and production OpenBao ownership docs unless a concrete AC gap requires them and an ADR-level boundary change is accepted.

### Testing Requirements

Run structural gates:

```bash
scripts/check-e2e-gates.sh
git diff --check
```

Run the production security rehearsal and cleanup:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make production-rehearsal-security
make production-rehearsal-down
```

Validate the generated report:

```bash
jq . artifacts/production-rehearsal/report.json
```

Run targeted package/script tests added or affected by the implementation. If only shell/doc/evidence changes are made, explain why no Go package test changed.

Run affected Go tests when touching `internal/scrapctl`, `internal/encryption`, or evidencebundle parsing:

```bash
env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/scrapctl ./internal/scrapctl/evidencebundle ./internal/encryption -count=1
```

Run the broad local gate before code review:

```bash
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run leak scans over story, evidence, script, generated report, and touched code. Keep patterns in shell variables so the command does not self-match copied secrets:

```bash
cred_pattern='(?i)(api[_-]?[k]ey|[s]ecret|[p]assword|[t]oken|[b]earer|[a]uthorization|aws_access_key_[i]d|aws_[s]ecret_access_[k]ey|private [k]ey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-)'
identifier_pattern='([t]ransaction_id|[d]ocument_name|[i]dempotency|Backend [k]ey|Backend object [k]ey|wrapped[- ][k]ey|data [k]ey|Transit [t]oken|plaintext data [k]ey|Frame payload|trace [I]D|request [I]D|gRPC [m]etadata|auth [c]laims|peer [a]ddress|[c]ertificate|root [t]oken|unseal [k]ey|/shards/|/tmp/|/home/)'
strict_value_pattern='(AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9_]{36,}|xox[baprs]-|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|aws_access_[k]ey_id|aws_[s]ecret_access_[k]ey|[sb]\.[A-Za-z0-9_-]{20,})'
scan_scope='_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md _bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md scripts/production-rehearsal.sh docs/production-rehearsal.md'
rg -n --pcre2 "$cred_pattern" $scan_scope
rg -n --pcre2 "$identifier_pattern" $scan_scope
rg -n --pcre2 "$strict_value_pattern" $scan_scope
```

### References

- `CONTEXT.md` - domain vocabulary, Cell/Member identity, OpenBao Transit substrate, and V2 process constraints.
- `_bmad-output/project-context.md` - Go style, package boundaries, testing rules, and critical privacy/security rules.
- `_bmad-output/planning-artifacts/epics.md` - Story 4.7 acceptance criteria and Epic 4 story split.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - production security, encryption/rewrap, bootstrap, and real S3/IAM evidence matrix.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-4 OpenBao bootstrap ownership and DG-5 release evidence standard.
- `_bmad-output/planning-artifacts/architecture.md` - Phase 4.5 closure, real mTLS/OpenBao, outage injection, and evidence redaction requirements.
- `docs/adr/0019-production-security-boundary.md` - production mTLS, authorization, audit, rate-limit, and fail-closed startup boundary.
- `docs/adr/0023-openbao-api-client.md` - official OpenBao Go API client boundary.
- `docs/production-rehearsal.md` - production rehearsal target contract and closure use.
- `docs/prd-closure-policy.md` - production rehearsal and real S3/IAM closure rules.
- `docs/openbao-deployment-contract.md` - platform-managed OpenBao and evidence rules.
- `scripts/production-rehearsal.sh` and `scripts/check-e2e-gates.sh` - current rehearsal behavior and structural invariants.
- Existing Story 4.1 through 4.6 story and evidence artifacts under `_bmad-output/implementation-artifacts/`.
- OpenBao Transit docs: https://openbao.org/docs/secrets/transit/
- OpenBao health API docs: https://openbao.org/api-docs/system/health/
- OpenBao Transit API docs: https://openbao.org/api-docs/secret/transit/
- OpenBao policy docs: https://openbao.org/docs/concepts/policies/

## Dev Agent Record

### Agent Model Used

GPT-5 Codex for story creation. The implementation agent should append the execution model used during dev-story.

### Debug Log References

- 2026-06-12T11:23:45-04:00 - Story 4.7 created from sprint status after Story 4.6 implementation, review, `make check`, commit, and push completed.

### Completion Notes List

- Story context created. Implementation pending.

### File List

- `_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
