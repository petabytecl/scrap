---
baseline_commit: 794c0f16e951c2186aeea573a448c39123736ed8
---

# Story 6.6: Real S3/IAM Production Rehearsal Closure

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a release owner,
I want real non-local S3/IAM rehearsal evidence captured after feature scope is complete,
so that Backend production claims do not rely only on LocalStack or local filesystem evidence.

## Acceptance Criteria

1. Given all required feature epics are complete, when real S3/IAM `make production-rehearsal` runs, then evidence uses non-local S3/IAM credentials and environment. Evidence records command, commit/ref, environment, expected result, actual result, artifact path, and redaction proof.
2. Given LocalStack or localhost endpoints appear in evidence, when final closure is evaluated, then they are marked interim only and cannot close the real S3/IAM gate. Evidence proves final Backend claims are not closed by local emulation.
3. Given issue `#429` is linked, when closure is reviewed, then the evidence artifact, command, environment, redaction proof, and result are traceable from the matrix. AC records the issue linkage and final gate status.
4. Given real S3/IAM evidence is vague, screenshot-only, localhost-only, LocalStack-only, or missing IAM provenance, when the final Backend claim is reviewed, then the S3/IAM gate is `FAIL` or `CONCERNS`. Evidence records hard pass/fail criteria for issue `#429`.

## Tasks / Subtasks

- [x] Create a durable Story 6.6 evidence artifact for issue `#429` (AC: 1, 3, 4)
  - [x] Add `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`.
  - [x] Record the tested command, commit/ref, environment summary, expected result, actual result, artifact path, issue `#429` state/link, and redaction proof.
  - [x] If real credentials are unavailable, record the gate honestly as `FAIL` or `CONCERNS`; do not fabricate a PASS from LocalStack, filesystem Backend, screenshots, stale output, or intent.
- [x] Add a static gate validator for the real S3/IAM evidence contract (AC: 1, 2, 4)
  - [x] Prefer a focused script such as `scripts/check-real-s3-iam-gate.sh` over embedding this logic into the runtime rehearsal script.
  - [x] Accept `PASS` only when the evidence points at a sanitized `artifacts/production-rehearsal/report.json` whose fields prove a real S3 Backend run: `status=passed`, `command=production-rehearsal`, `environment=production-rehearsal`, `evidence_tier=real-s3-iam`, `backend=s3`, `local_overrides.real_s3_iam=true`, `local_overrides.local_s3_endpoint_allowed=false`, `security_mode=production`, `production_readiness_status=ready`, `openbao_transit=real`, `test_hooks_enabled=false`, `pprof_enabled=false`, `encrypted_write_read_ok=true`, `plaintext_leak_scan_ok=true`, `backend_upload_confirmed=true`, `confirmed_upload_count >= 1`, and `redaction_proof.status=passed`.
  - [x] Reject any `PASS` evidence that mentions `localhost`, `127.0.0.1`, `localstack`, `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true`, screenshots-only proof, missing IAM provenance, missing issue `#429` linkage, missing command, missing commit/ref, missing artifact path, or missing redaction proof.
  - [x] Keep `FAIL` and `CONCERNS` rows valid when they clearly state the missing real S3/IAM proof, owner, and mitigation.
- [x] Add focused tests for the validator (AC: 1, 2, 4)
  - [x] Cover a valid real-S3 report/evidence fixture that passes.
  - [x] Cover weak PASS cases: prose-only fields, LocalStack/localhost endpoint, local S3 override, filesystem Backend report, missing IAM provenance, missing issue linkage, missing report fields, missing redaction proof, and `confirmed_upload_count=0`.
  - [x] Keep fixtures sanitized; never include real credentials, bucket names, raw Backend object keys, validation tokens, raw logs, Document payloads, or generated certificate material.
- [x] Update the release evidence matrix for Story 6.6 (AC: 3, 4)
  - [x] Update the current release decision, FR-6, FR-16, ADR-0009, Story 6.6, and issue `#429` rows.
  - [x] If issue `#429` remains open, keep the relevant rows `FAIL` or `CONCERNS`; do not mark final release `PASS`.
  - [x] Link the Story 6.6 evidence artifact and the real report path or the explicit missing-proof decision.
- [x] Run the real rehearsal only when a real non-local S3/IAM environment is intentionally available (AC: 1, 2)
  - [x] Use `env GOFLAGS=-buildvcs=false make production-rehearsal` with `SCRAP_S3_BUCKET`, `SCRAP_S3_REGION`, and AWS credentials from the default provider chain, configured profile, or workload identity.
  - [x] Leave `SCRAP_S3_ENDPOINT` unset unless it points at a real non-local endpoint.
  - [x] Do not use `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true` for release-gate evidence.
  - [x] Preserve the generated report under ignored `artifacts/production-rehearsal/report.json`; commit only the sanitized Story 6.6 evidence summary and matrix links.
- [x] Validate and close out the story safely (AC: 1, 2, 3, 4)
  - [x] Run the validator and its tests.
  - [x] Run `scripts/check-e2e-gates.sh`, `git diff --check`, and the narrowest relevant Go/package gates. Use `env GOCACHE=/tmp/scrap-v2-go-build make check` before broad review if scripts or release evidence gates changed.
  - [x] Run release-sensitive scans over the committed Story 6.6 artifacts, matrix, validator, and tests for credentials, tokens, raw Document identifiers, raw Backend keys, raw logs, private material, data keys, wrapped-key ciphertext, and host-absolute paths.
  - [x] Update this story status and sprint status only after verification reflects the actual gate state.

## Dev Notes

### Current Gate State

- Story 6.5 is complete and pushed at baseline commit `ab74f281627949cda9d61d4aa469cbb3361defda`; Story 6.6 starts from a clean `v2` branch synced to `origin/v2`.
- Live issue `#429` is open with labels `ready-for-human`, `production-readiness`, `v2`, and `e2e`. Its acceptance criteria require a real non-local S3 bucket/region, credentials from the default provider chain/profile/workload identity, no local S3 override, `env GOFLAGS=-buildvcs=false make production-rehearsal`, a sanitized report, tested commit/ref, and no pasted secrets or raw Backend keys.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` currently marks real S3/IAM Backend proof, FR-6 final Backend proof, FR-16 closure, ADR-0009 final proof, Story 6.6, and issue `#429` as missing/`FAIL`. Preserve that honesty until real evidence exists.
- The likely AFK/local state has no real S3 credentials. If `SCRAP_S3_BUCKET`, `SCRAP_S3_REGION`, and AWS credentials are absent, implement the validator/evidence contract and record the gate as not closed; do not stop waiting for credentials.

### Existing Runtime Rehearsal Path To Reuse

- `Makefile` already defines `production-rehearsal-security` with filesystem Backend and `production-rehearsal` with S3 Backend. Do not add a parallel target unless the existing target cannot express the required proof.
- `scripts/production-rehearsal.sh` already fails fast when `SCRAP_S3_BUCKET` or `SCRAP_S3_REGION` are missing for S3, rejects local/test `SCRAP_S3_ENDPOINT` unless the explicit local override is set, and rejects the static `SCRAP_PROD_REHEARSAL_CELL_ID=production-rehearsal` because it would reuse Backend object keys.
- The script report already emits the DG-5 fields needed by this story: `command`, `commit_ref`, `git_worktree_state`, `git_diff_sha256`, `timestamp`, `environment`, `evidence_tier`, `expected_result`, `actual_result`, `artifact_path`, `backend`, `local_overrides.real_s3_iam`, `local_overrides.local_s3_endpoint_allowed`, `confirmed_upload_count`, and `redaction_proof`.
- `assert_report_invariants` currently proves generic production rehearsal invariants. Story 6.6 should add release-gate validation around the evidence/report rather than weakening `production-rehearsal-security` or requiring real cloud credentials in CI.

### S3/IAM Technical Requirements

- `internal/backend/s3.go` uses AWS SDK for Go v2 and `config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))`; credentials should come from the AWS default chain rather than being embedded in code or committed artifacts.
- The S3 Backend contract includes `PutObject`, `HeadObject`, `GetObject`, `DeleteObject`, and `ListObjectsV2`. Minimum IAM provenance for a full Backend proof should cover the operations actually exercised or required by the Backend: `s3:PutObject`, `s3:GetObject`/`HeadObject`, `s3:DeleteObject` if cleanup/deletion is used, and `s3:ListBucket` for list operations. `HeadObject` requires `s3:GetObject`; listing requires `s3:ListBucket`.
- Public evidence must not include AWS access key IDs, secret access keys, session tokens, real bucket object keys, raw Backend keys, validation tokens, request IDs, trace IDs, or raw AWS/dependency error output. It may describe credential source class, region, non-local endpoint status, command, commit/ref, and sanitized report booleans.

### Architecture Compliance

- Backend remains an opaque durability boundary. Do not use S3 inventory/object existence as release authority outside the explicit upload/restore/rehearsal workflows.
- Evidence is not source of truth for storage state; it is release proof. Keep storage behavior behind `internal/backend` and Shard/upload authority paths.
- Keep exact domain terms from `CONTEXT.md`: Document, Transaction, Block, Frame, Shard, Cell, Member, Backend, Upload Outbox, Confirmed Upload Catalog.
- Do not add an ADR unless this story changes storage format, wire protocol, dependency/runtime choice, security/encryption/auth contracts, or cross-package ownership. A validator, evidence artifact, and matrix update are normal Story 6.6 scope.
- Application logging remains `log/slog`; avoid adding zap-native application logging.

### File Structure Requirements

- Likely NEW:
  - `scripts/check-real-s3-iam-gate.sh`
  - `scripts/real_s3_iam_gate_test.go` or additional cases in the closest existing script test file
  - `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`
- Likely UPDATE:
  - `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
  - `scripts/check-e2e-gates.sh` if the new static validator should be part of the existing evidence gate bundle
  - `_bmad-output/implementation-artifacts/sprint-status.yaml`
  - this story file
- Avoid committing ignored runtime output under `artifacts/production-rehearsal/`.

### Testing Requirements

- Prefer deterministic fixture tests for validator behavior. Do not require real S3/IAM credentials for unit/script tests.
- If a real S3/IAM environment is available, run `env GOFLAGS=-buildvcs=false make production-rehearsal` and validate the generated `artifacts/production-rehearsal/report.json`.
- Required local gates for implementation:
  - `scripts/check-real-s3-iam-gate.sh` against the committed Story 6.6 evidence artifact
  - `scripts/check-e2e-gates.sh`
  - `go test -count=1 ./scripts`
  - `git diff --check`
  - `env GOCACHE=/tmp/scrap-v2-go-build make check` before review/commit if script gates changed

### Previous Story Intelligence

- Story 6.5 review found that global grep-style validation could allow missing per-row fields and inconsistent release PASS states. Story 6.6 validation must inspect the relevant row/report fields directly and reject weak PASS evidence.
- Story 6.5 established that screenshots, stale artifacts, local-only runs, unlinked output, missing retention/provenance, and advisory-only language cannot satisfy release gates. Reuse that standard for S3/IAM.
- Story 6.5 explicitly kept real non-local S3/IAM proof out of Tier 2/Tier 3 closure and assigned it to Story 6.6 / issue `#429`; do not mark FR-6, ADR-0009, issue `#429`, or final V2 release PASS without this proof.

### Research Notes

- Repo-local GitHub code search for exact `production-rehearsal` / `SCRAP_S3_BUCKET` and `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` patterns found no additional implementation to reuse beyond the current checkout.
- AWS SDK for Go v2 documentation confirms `config.LoadDefaultConfig` uses the default credential chain, including environment variables, web identity, shared configuration files, and role-based providers. This matches the current `internal/backend/s3.go` approach.
- AWS S3 IAM documentation maps `ListObjectsV2` to `s3:ListBucket`, `PutObject` to `s3:PutObject`, `GetObject` to `s3:GetObject`, `DeleteObject` to `s3:DeleteObject`, and `HeadObject` to `s3:GetObject`.

### Project Structure Notes

- This story is a release evidence/closure story. Keep durable evidence under `_bmad-output/implementation-artifacts/`; promote only durable operator policy into `docs/`.
- Runtime reports and generated secrets stay under ignored `artifacts/production-rehearsal/`.
- No UX artifacts are relevant.

### References

- `_bmad-output/planning-artifacts/epics.md:1723` - Story 6.6 source story and acceptance criteria.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md:466` - FR-16 evidence and documentation closure.
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md:571` - Real S3/IAM Backend proof minimum gate.
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md:383` - DG-5 release evidence standard.
- `docs/production-rehearsal.md:31` - `make production-rehearsal` requirements and local override warning.
- `docs/production-rehearsal.md:61` - report path and report fields.
- `docs/production-rehearsal.md:142` - closure use for real S3/IAM claims.
- `docs/prd-closure-policy.md:47` - production rehearsal closure policy.
- `docs/runbooks/v2-evidence-collection.md:31` - real S3/IAM final Backend proof guidance.
- `Makefile:584` - existing S3 production rehearsal target.
- `scripts/production-rehearsal.sh:283` - S3 Backend config validation.
- `scripts/production-rehearsal.sh:1017` - report field generation.
- `scripts/production-rehearsal.sh:1113` - report invariant validation.
- `internal/backend/s3.go:54` - AWS SDK default config loading.
- `internal/backend/s3.go:103` - S3 put/head/get operations used by Backend.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:67` - current release decision.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:85` - FR-6 real S3/IAM gap.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:95` - FR-16 remaining blockers.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:170` - Story 6.6 row.
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md:185` - issue `#429` row.
- GitHub issue `#429`: https://github.com/petabytecl/scrap/issues/429
- AWS SDK for Go v2 configuration docs: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html
- AWS S3 required permissions docs: https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-with-s3-policy-actions.html
- AWS S3 IAM resource docs: https://docs.aws.amazon.com/AmazonS3/latest/userguide/security_iam_service-with-iam.html

## Change Log

- 2026-06-12: Added real S3/IAM release gate validator, tests, evidence artifact, matrix updates, and wired the validator into the E2E static gate.

## Dev Agent Record

### Agent Model Used

GPT-5 Codex

### Debug Log References

- `go test -count=1 ./scripts` - RED failed before `scripts/check-real-s3-iam-gate.sh` existed.
- `go test -count=1 ./scripts` - PASS after adding validator and fixtures.
- `scripts/check-real-s3-iam-gate.sh` - PASS against committed Story 6.6 evidence artifact.
- `scripts/check-e2e-gates.sh` - PASS with real S3/IAM validator wired in.
- `git diff --check` - PASS.
- Release-sensitive secret-shape scan - PASS; only negative policy/fixture text matched in the explanatory redaction scan.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS.
- Real S3/IAM env presence check - `SCRAP_S3_BUCKET`, `SCRAP_S3_REGION`, `AWS_PROFILE`, `AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` absent; real rehearsal not run.

### Completion Notes List

- Story context created from sprint backlog after Story 6.5 completion.
- Live issue `#429` verified open during story creation.
- Story intentionally treats missing real S3/IAM credentials as a release gate gap, not a blocker to creating validator/evidence contract work.
- Added `scripts/check-real-s3-iam-gate.sh` to validate the Story 6.6 evidence artifact and reject any `PASS` without a real S3 report proving `backend=s3`, `evidence_tier=real-s3-iam`, no local S3 override, upload confirmation, and redaction proof.
- Added deterministic Go tests for valid PASS, honest missing-proof FAIL, weak PASS, local override, zero upload count, missing evidence, and prose-only evidence.
- Updated the release matrix and evidence artifact to keep issue `#429` and final real S3/IAM proof as `FAIL` until a real non-local `make production-rehearsal` report is linked.

### File List

- `_bmad-output/implementation-artifacts/6-6-real-s3-iam-production-rehearsal-closure.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
- `scripts/check-e2e-gates.sh`
- `scripts/check-real-s3-iam-gate.sh`
- `scripts/real_s3_iam_gate_test.go`
