---
baseline_commit: 06f2b4f120a165f7034afe74e822d5e7f4ad294c
---

# Story 6.5: Tier 2 and Tier 3 Release Evidence Gates

Status: review

## Story

As a release owner,
I want Tier 2 prod-like and Tier 3 telemetry/evidence gates linked into closure,
so that deployed behavior and telemetry privacy are proven before V2 release.

## Acceptance Criteria

1. **AC-6.5.1 - Tier 2 prod-like Cilium evidence is linked.** Given Tier 2 prod-like Cilium evidence is collected, when artifacts are reviewed, then deployed gateway behavior, security posture, and relevant feature evidence are linked. Evidence records command, commit/ref, environment, expected result, actual result, and artifact path.
2. **AC-6.5.2 - Tier 3 evidence bundle is linked and redacted.** Given Tier 3 evidence bundle is collected, when artifacts are reviewed, then logs, metrics, traces, profiles, and leak-scan results are present and redacted. Evidence proves telemetry privacy constraints are enforced.
3. **AC-6.5.3 - Stale, missing, or inconsistent gates block PASS.** Given either gate is stale, missing, or inconsistent with the current commit/ref, when closure is evaluated, then V2 readiness is `FAIL` or `CONCERNS`. Evidence records the owner and mitigation for each gap.
4. **AC-6.5.4 - Weak release proof is rejected.** Given Tier 2 or Tier 3 evidence is a screenshot, stale artifact, local-only run, or unlinked output, when release closure reviews the gate, then the gate fails or records `CONCERNS` using hard criteria, not advisory language. Evidence records pass/fail criteria and artifact retention rules.

## Tasks / Subtasks

- [x] Add red-phase checks for the missing Story 6.5 evidence surface. (AC: 1-4)
  - [x] First assert that `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md` does not yet exist or does not contain required Tier 2/Tier 3 rows.
  - [x] Add a lightweight validation path that fails when a Tier 2 or Tier 3 row lacks command, commit/ref, environment, expected result, actual result, artifact path, timestamp, redaction proof, freshness decision, owner, mitigation, and retention decision.
  - [x] Make stale, local-only, screenshot-only, or unlinked evidence fail validation or appear as `CONCERNS`/`FAIL` in the evidence artifact.
- [x] Create the Tier 2/Tier 3 release evidence artifact. (AC: 1-4)
  - [x] Create `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md`.
  - [x] Record one row for Tier 2 prod-like Cilium evidence and one row for Tier 3 evidence bundle evidence using the full DG-5 evidence schema.
  - [x] For Tier 2, link the exact `make tier2-e2e-up` command or GitHub Actions run that executed it, the tested commit/ref, the artifact name/path, and the prod-like Kind/Cilium environment.
  - [x] For Tier 3, link the exact `make tier3-evidence-up STRESS_SCENARIO=<scenario>` command or `evidence-gate.yml` run, the generated bundle path, `manifest.json`, `gates.json`, `privacy-scan.json`, and the artifact name/path.
  - [x] Record artifact retention rules: GitHub Actions artifacts expire unless retained or copied to durable release evidence, and local ignored artifacts are not final release proof by themselves.
- [x] Review or collect current Tier 2 gate evidence. (AC: 1, 3, 4)
  - [x] Prefer a green GitHub Actions run for `.github/workflows/prodlike-e2e.yml` or a CI carrier run that executes the same `make tier2-e2e-up` gate if the dedicated workflow cannot be dispatched yet.
  - [x] If only local `make tier2-e2e-up` output is available, record it as local/prod-like development evidence and keep release status at `CONCERNS` unless a reviewable durable artifact is linked.
  - [x] Confirm the Tier 2 artifact contains `artifacts/tier2-e2e.log`, Kind diagnostics, the security evidence report path, and the tested commit/ref.
  - [x] Do not treat screenshots, copied terminal snippets, or unlinked local output as PASS evidence.
- [x] Review or collect current Tier 3 gate evidence. (AC: 2, 3, 4)
  - [x] Prefer a green `evidence-gate.yml` run because it runs Tier 2 first, resets the prod-like context, then runs `make tier3-evidence-up` and uploads `artifacts` plus `evidence`.
  - [x] If local `make tier3-evidence-up STRESS_SCENARIO=throughput` is used, record the generated bundle path and keep final release status below PASS unless the artifact is made reviewable and durable.
  - [x] Verify the linked bundle includes logs, metrics, traces, profiles, `manifest.json`, `gates.json`, `privacy-scan.json`, and a passing privacy/leak-scan status.
  - [x] Confirm `privacy-scan.json` was generated after final metadata exists, per Story 6.4 review fixes.
- [x] Update release-facing matrix rows without over-claiming closure. (AC: 1-4)
  - [x] Update `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` for FR-15, FR-16, Story 6.5, and any directly affected release-gate rows.
  - [x] Keep real S3/IAM proof as Story 6.6/issue `#429`; do not mark FR-6, real S3/IAM, or final V2 release PASS from Story 6.5.
  - [x] Keep Story 6.7 as the final closure-policy and gate-decision owner.
- [x] Preserve Epic 6 aggregation scope and redaction discipline. (AC: 2-4)
  - [x] Do not add production feature behavior, new storage authority, new telemetry instruments, new admin endpoints, or replacement `scrapctl` diagnostics unless a missing validator can be implemented as docs/evidence-only support.
  - [x] Do not paste raw workflow logs, credentials, private keys, generated certificate material, Document payloads, raw Document identifiers, Backend keys, trace IDs, request IDs, auth claims, host-absolute local paths, data keys, wrapped-key ciphertext, or raw dependency output into committed artifacts.
  - [x] Record missing proof honestly as `FAIL` or `CONCERNS` with owner and mitigation.
- [x] Run verification and update BMAD tracking. (AC: 1-4)
  - [x] `git diff --check`
  - [x] `scripts/check-e2e-gates.sh`
  - [x] Run any focused tests for changed validators, for example `go test ./scripts` if script tests are touched.
  - [x] Run `env GOCACHE=/tmp/scrap-v2-go-build make check` before moving the story to review if code, scripts, workflows, or validation policy changed.
  - [x] Run release-sensitive scans over this story, the Tier gates evidence artifact, the updated matrix, and any changed runbook/policy/script files.
  - [x] Move this story to `review`; leave `done` for BMAD code review.

## Dev Notes

### Source Requirements

- FR-15 requires OpenTelemetry metrics, logs, traces, and profiles sufficient to prove runtime behavior, production safety, and evidence gates. New metrics use low-cardinality OTel attributes, logs use `slog`, and pprof stays opt-in for admin/evidence paths. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-15: OTel evidence plane`]
- FR-16 requires linked, current, reviewable evidence and operator documentation for every required release claim. Required evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, production security rehearsal, and real S3/IAM production rehearsal when Backend claims depend on S3. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#FR-16: Major-release evidence and documentation closure`]
- Final release closure fails if any required requirement lacks current linked evidence. SM-3 requires green Tier 2, Tier 3, production security rehearsal, and real S3/IAM rehearsal evidence where applicable. SM-4 requires no secrets, raw Document identifiers, raw Backend keys, Document bytes, data keys, or wrapped-key ciphertext in evidence bundles or tracker comments. [Source: `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#11. Acceptance and Evidence Matrix`; `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md#12. Success Metrics`]
- DG-5 requires every release claim to record command, commit/ref, environment, expected result, actual result, artifact path, timestamp, and redaction proof. Final evidence includes Tier 2 prod-like Cilium, Tier 3 evidence bundle, `make production-rehearsal-security`, and real S3/IAM `make production-rehearsal`. LocalStack/test endpoints are interim only. [Source: `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md#DG-5: Release Documentation and Evidence Standard`]
- Tier guidance says use Tier 2 when behavior crosses process/runtime boundaries and Tier 3 when claims involve deployment, networking, resilience, load, observability, security/privacy evidence, or production readiness. [Source: `_bmad-output/planning-artifacts/architecture.md#Architectural Decisions Provided by Existing Foundation`; `_bmad-output/planning-artifacts/architecture.md#Infrastructure & Deployment`]

### Existing Gate Surfaces to Reuse

| Gate | Existing surface | Expected evidence |
| --- | --- | --- |
| Tier 2 prod-like Cilium | `make tier2-e2e-up`; `.github/workflows/prodlike-e2e.yml` | Green run of `make tier2-e2e-up`, `artifacts/tier2-e2e.log`, Kind diagnostics, tested commit/ref, artifact name/path, prod-like Kind/Cilium environment. |
| Tier 3 evidence bundle | `make tier3-evidence-up STRESS_SCENARIO=throughput`; `.github/workflows/evidence-gate.yml` | Green run of Tier 3 evidence gate, uploaded `artifacts` and `evidence`, `artifacts/tier3-bundle-path.txt`, generated bundle `manifest.json`, `gates.json`, and `privacy-scan.json`. |
| Gate wiring policy | `scripts/check-e2e-gates.sh` | Static validation of Makefile targets, Tier 2/Tier 3 workflow wiring, failure diagnostics, security report wiring, and closure-policy references. |
| Operator docs | `docs/prd-closure-policy.md`; `docs/runbooks/v2-evidence-collection.md`; `docs/observability/v2-alert-query-references.md` | Current instructions and explicit gap language for Tier 2, Tier 3, evidence bundle privacy, and local-only evidence limits. |
| Story 6.4 bundle contract | `_bmad-output/implementation-artifacts/v2-release-evidence-bundle.md` | Manifest, privacy scan, final-metadata leak scan, broad evidence rows, missing-evidence semantics, and final release blockers. |

### Current Makefile and Workflow Facts

- `TIER2_E2E_TEST_RUN` covers `WriteReadHead`, `LeaderFailover`, `BackendUploadHappyPath`, `BackendUploadLeaderChange`, `BackendUploadAdmissionPressure`, `MultiShardRestartDeterminism`, `MultiShardBackendUploadUsesNonZeroShard`, `LightScrub`, and `ProdlikeSecurityEncryptionEvidence`. These tests are implemented under `test/e2e/`.
- `tier2-e2e` sets `SCRAP_E2E_CELL_ID="kind-prodlike"`, uses the prod-like E2E kube context, writes `SCRAP_E2E_SECURITY_REPORT`, runs `go test ./test/e2e/ -count=1`, and prints `TIER2_E2E_STATUS=passed`.
- `tier3-evidence-up` depends on `evidence-up` and `tier3-evidence`; `tier3-evidence` delegates to `evidence-bundle`.
- `evidence-bundle` exports `BUNDLE_DIR`, `STRESS_ADDR`, `STRESS_WORKERS`, `STRESS_DURATION`, `STRESS_DOC_SIZE`, and `SECURITY_EVIDENCE_REPORT` before calling `scripts/evidence-bundle.sh`.
- `evidence-gate.yml` runs Tier 2 first to produce the security report, deletes the prod-like Kind Cell, unsets prod-like contexts, then runs `make tier3-evidence-up` and uploads both `artifacts` and `evidence`.
- `prodlike-e2e.yml` can run manually or on schedule and uploads `tier2-prodlike-e2e-<run_id>` artifacts.

### Evidence Semantics and Boundaries

- A PASS row needs reviewable current evidence, not just target existence. Target wiring can pass while release evidence remains `CONCERNS` because the gate has not run for the current commit/ref.
- A GitHub Actions run link must identify the run, tested ref/SHA, status, uploaded artifact name, and artifact path. If artifact retention expires, the row must become stale unless the evidence was copied into durable release storage.
- Local ignored paths under `evidence/` and `artifacts/` can support development, but they are not final release proof by themselves. They need a durable reviewable link or a conscious `CONCERNS` decision.
- Screenshots, copied terminal snippets, and unlinked output are weak proof. They should never produce a `PASS` row.
- Story 6.5 does not own real non-local S3/IAM proof. Keep issue `#429` and `make production-rehearsal` as Story 6.6 inputs.
- Story 6.5 does not make the final V2 release decision. Story 6.7 owns final closure policy and release gate decision.

### Previous Story Intelligence

- Story 6.4 review found that final metadata artifacts (`gates.json`, `manifest.json`) must be privacy-scanned after they exist. Tier 3 evidence review must inspect `privacy-scan.json` and not accept a pre-final scan.
- Story 6.4 tightened manifest rows so broad domains require all relevant underlying gate checks, not file presence or one representative check. Do not summarize Tier 2/Tier 3 as PASS unless the underlying required evidence exists.
- Story 6.4 added run parameters and security-report presence to the bundle manifest. Tier 3 evidence rows should record workers, duration, document size, stress address classification, security report configured/present, and scenario.
- Story 6.3 left evidence leak-scan status as `CONCERNS` until Story 6.4 and Story 6.5 produce final bundle/gate scans. Completing this story should update that gap honestly.
- Story 6.2 clarified that passive evidence collection and environment-mutating gates are different workflows. Tier 2/Tier 3 cleanup and diagnostics should be recorded separately from passive bundle capture.

### Research and Reuse Notes

- Local reuse scan found the Tier 2/Tier 3 gate orchestration already exists in `Makefile`, `.github/workflows/prodlike-e2e.yml`, `.github/workflows/evidence-gate.yml`, `scripts/check-e2e-gates.sh`, and Story 6.4 evidence bundle code. Extend or validate these surfaces instead of creating parallel gate commands.
- GitHub code search for `evidence-gate.yml`, `tier2-e2e`, and `tier3-evidence` returned no directly reusable SCRAP-compatible implementation. Keep the implementation repo-local.
- GitHub Actions docs state workflow artifacts can be uploaded/downloaded, artifact retention can be configured, and artifacts automatically expire if not retained. The `upload-artifact` action exposes a SHA-256 digest, and GitHub CLI can download run artifacts with `gh run download`. Use this as provenance context; do not overfit the story to a non-local artifact format. [Source: `https://docs.github.com/en/actions/tutorials/store-and-share-data`; `https://docs.github.com/en/actions/how-tos/manage-workflow-runs/download-workflow-artifacts`]

### Redaction and Security Notes

- Public docs, committed evidence, and tracker-ready summaries must not include credential values, private key material, generated certificate material, Document payloads, raw Document identifiers, Backend object keys, tokens, trace IDs, request IDs, auth claims, host-absolute paths, data keys, wrapped-key ciphertext, or raw dependency/log output.
- Keep scan patterns bracket-split in story/evidence artifacts so the scan commands do not self-match.
- Negative examples should use impossible placeholders such as `<redacted-artifact-name>`, `<workflow-run-url>`, `<artifact-name>`, `<bundle-path>`, `<commit-sha>`, and `<scenario>`.
- If a runtime artifact contains sensitive material, do not commit it. Record a sanitized summary and the remediation owner.

### Testing Requirements

Minimum checks before creating the implementation commit:

```bash
git diff --check
scripts/check-e2e-gates.sh
```

If scripts, Go code, workflows, or validation policy changed, also run:

```bash
go test ./scripts
env GOCACHE=/tmp/scrap-v2-go-build make check
```

Run release-sensitive scans over touched story/evidence/docs/script files:

```bash
secret_shape='(?i)([a]ccess[_-]?[k]ey|[p]assword|[t]oken|Bearer[[:space:]]+|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]+\.|PRIVATE [K]EY|BEGIN [A-Z ]*KEY)'
identity_shape='(?i)(transaction[_-]?[i]d|transactionId|document[_-]?[n]ame|documentName|trace[_-]?[i]d|traceID|request[_-]?[i]d|requestID|x-request-id)[[:space:]]*[:=]|Backend [k]ey|raw [l]og|auth [c]laim|wrapped-[k]ey|data [k]ey'
path_shape='(^|[[:space:]])(/(home|Users|var|opt|private|tmp)/|[A-Za-z]:\\)|host-[a]bsolute'
rg -n --pcre2 "$secret_shape" <touched-files>
rg -n --pcre2 "$identity_shape" <touched-files>
rg -n --pcre2 "$path_shape" <touched-files>
```

Classify every match as required negative guidance, safe placeholder, safe path policy, or a bug to fix before review.

### Project Structure Notes

- Expected new durable artifact: `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md`.
- Expected updates: `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`, this story file, and `_bmad-output/implementation-artifacts/sprint-status.yaml`.
- Possible validation touch points: `scripts/check-e2e-gates.sh` and matching script tests if hard criteria are missing from the existing static gate.
- Possible docs touch points: `docs/prd-closure-policy.md` or `docs/runbooks/v2-evidence-collection.md` only if artifact retention/pass-fail criteria need clarification. Avoid Story 6.7 final-decision policy changes.
- Do not edit protobuf contracts, generated files, storage packages, Raft/Shard authority code, Backend implementations, deployment overlays, ADRs, or production feature behavior for this story unless a direct blocker is discovered and documented honestly.

### References

- `CONTEXT.md` - glossary and authority boundaries.
- `_bmad-output/planning-artifacts/epics.md` - Epic 6 and Story 6.5 source requirements.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md` - FR-15, FR-16, evidence matrix, and success metrics.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md` - DG-5 release documentation and evidence standard.
- `_bmad-output/planning-artifacts/architecture.md` - Tier 1/Tier 2/Tier 3 gate guidance.
- `Makefile` - Tier 2, Tier 3, evidence bundle, and production rehearsal targets.
- `.github/workflows/prodlike-e2e.yml` - Tier 2 GitHub Actions carrier.
- `.github/workflows/evidence-gate.yml` - Tier 3 GitHub Actions carrier.
- `scripts/check-e2e-gates.sh` - static gate wiring and closure-policy validation.
- `scripts/evidence-bundle.sh` - evidence bundle wrapper used by Tier 3.
- `scripts/collect-kind-artifacts.sh` - Kind diagnostics captured by workflows.
- `docs/prd-closure-policy.md` - Tier 2, Tier 3, production rehearsal, and real S3/IAM closure rules.
- `docs/runbooks/v2-evidence-collection.md` - operator evidence collection path.
- `docs/observability/v2-alert-query-references.md` - release-risk observability references and evidence leak-scan gap.
- `_bmad-output/implementation-artifacts/v2-release-evidence-bundle.md` - Story 6.4 bundle contract.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` - current release matrix and Story 6.5 gap row.
- `_bmad-output/implementation-artifacts/6-4-scrapctl-release-evidence-bundle.md` - previous story intelligence and review fixes.
- GitHub Actions artifact docs: `https://docs.github.com/en/actions/tutorials/store-and-share-data`; `https://docs.github.com/en/actions/how-tos/manage-workflow-runs/download-workflow-artifacts`.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Implementation Plan

- Create the Tier 2/Tier 3 evidence artifact with hard PASS/CONCERNS/FAIL criteria.
- Link or collect current Tier 2 and Tier 3 gate evidence without treating local-only output as final proof.
- Update the release matrix rows for Story 6.5, FR-15, and FR-16 while keeping Story 6.6 and Story 6.7 blockers visible.
- Run gate wiring checks and release-sensitive scans before review.

### Debug Log References

- 2026-06-12T20:01:01-04:00 - Story context created from Epic 6, FR-15, FR-16, DG-5, existing Tier 2/Tier 3 Makefile targets, GitHub Actions workflows, closure policy, evidence runbook, Story 6.4 bundle/review lessons, current sprint status, GitHub code search, and GitHub Actions artifact docs.
- 2026-06-12T20:05:25-04:00 - Story marked in-progress after creation commit `d2a9bcc`.
- 2026-06-12T20:08:34-04:00 - Red phase confirmed: `scripts/check-release-tier-gates.sh` and `scripts/check-e2e-gates.sh` failed because `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md` was missing.
- 2026-06-12T20:12:15-04:00 - Verification passed: `scripts/check-release-tier-gates.sh`, `scripts/check-e2e-gates.sh`, `go test -count=1 ./scripts`, `git diff --check`, release-sensitive scans with classified safe matches, and `env GOCACHE=/tmp/scrap-v2-go-build make check`.

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- Added `scripts/check-release-tier-gates.sh` and wired it into `scripts/check-e2e-gates.sh` so Tier 2/Tier 3 release evidence must exist and include hard DG-5 fields before gate wiring passes.
- Added script tests proving missing evidence fails, complete evidence passes, and weak PASS evidence based on local-only proof fails.
- Created `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md` with Tier 2/Tier 3 rows, live GitHub Actions state, artifact retention rules, and explicit `CONCERNS`/`FAIL` outcomes for missing current runtime evidence.
- Updated the release matrix for Story 6.5, FR-15, FR-16, and the Epic 6 current release decision without marking final V2 release PASS.
- Release-sensitive scans matched only negative guidance, existing validation-pattern text, and safe evidence-policy wording; no secrets or raw runtime artifact contents were committed.

### Change Log

- 2026-06-12 - Implemented Story 6.5 Tier 2/Tier 3 evidence validator, evidence artifact, and matrix updates.

### File List

- `_bmad-output/implementation-artifacts/6-5-tier-2-and-tier-3-release-evidence-gates.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
- `_bmad-output/implementation-artifacts/v2-release-tier-gates-evidence.md`
- `scripts/check-e2e-gates.sh`
- `scripts/check-release-tier-gates.sh`
- `scripts/release_tier_gates_test.go`
