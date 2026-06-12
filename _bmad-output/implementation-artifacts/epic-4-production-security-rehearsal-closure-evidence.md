# Epic 4 Production Security Rehearsal Closure Evidence

Status: done
Story: 4.7 - Production Security Rehearsal Evidence Closure
Story frontmatter baseline commit: `940008d5e59c89b54c7dcacea39c37e6901c4751`
Implementation baseline commit after Story 4.7 creation checkpoint: `e7bb8c98c7fb9f8be3863f5cc9ea9a11c61c825e`
Started: 2026-06-12T11:27:21-04:00
Last updated: 2026-06-12T11:54:35-04:00

## Scope

This artifact records Story 4.7 evidence for FR-9, FR-10, and FR-14 closure:

- link Epic 4 startup, surface security, encryption, rewrap, bootstrap,
  idempotency, incompatible-state, and redaction evidence to owning stories;
- prove the production security rehearsal command emits release-grade metadata;
- prove Transit outage, auth-denied, and wrong-key or missing-key rehearsal
  drills fail closed without plaintext fallback or secret leakage; and
- make Epic 4 closure `FAIL`, `CONCERNS`, or `PASS` using V2 release gate
  language instead of deferring missing P0 evidence to Epic 6.

Out of scope:

- real non-local S3/IAM production rehearsal closure, owned by Story 6.6 /
  issue #429;
- production OpenBao deployment, HA topology, secret custody, and lifecycle;
- storage format, wire protocol, Document identity, Backend object identity,
  Raft command shape, envelope metadata format, and public/peer/admin contract
  changes.

## Current Closure Decision

| Area | Decision | Reason |
| --- | --- | --- |
| Epic 4 local production-security rehearsal closure | `PASS` | Story 4.1 through 4.6 evidence is linked with owning stories, and Story 4.7 now records a passing `make production-rehearsal-security` report with release-grade metadata, supported `scrapctl openbao bootstrap`, encrypted write/read, committed Backend upload confirmation, Shard placement, three fail-closed drills, and redaction proof. |
| Real S3/IAM closure | `FAIL` for real S3/IAM claims | Story 4.7 may close local production-security rehearsal evidence only. Real non-local S3/IAM proof remains Story 6.6 / issue #429 until `make production-rehearsal` is run with real S3/IAM and linked. |

## Source References

- `_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md`
- `_bmad-output/planning-artifacts/epics.md`
- `_bmad-output/planning-artifacts/prds/prd-scrap-v2-master-2026-06-10/prd.md`
- `_bmad-output/planning-artifacts/architecture-v2-master-2026-06-10.md`
- `_bmad-output/planning-artifacts/architecture.md`
- `docs/adr/0019-production-security-boundary.md`
- `docs/adr/0023-openbao-api-client.md`
- `docs/production-rehearsal.md`
- `docs/prd-closure-policy.md`
- `docs/openbao-deployment-contract.md`
- `scripts/production-rehearsal.sh`
- `scripts/check-e2e-gates.sh`
- OpenBao Transit docs: https://openbao.org/docs/secrets/transit/
- OpenBao health API docs: https://openbao.org/api-docs/system/health/
- OpenBao Transit API docs: https://openbao.org/api-docs/secret/transit/
- OpenBao policy docs: https://openbao.org/docs/concepts/policies/

## Prior Epic 4 Evidence Inventory

| Owning story | Evidence artifact | Required claim | Current scoped result | Closure impact |
| --- | --- | --- | --- | --- |
| Story 4.1 - Production Security Startup Gate | `_bmad-output/implementation-artifacts/epic-4-production-security-startup-gate-evidence.md` | Production startup gates fail closed and do not fall back to local/test defaults. | `PASS` for package/startup-gate scope. | Linked. Story 4.7 must add live production rehearsal proof. |
| Story 4.2 - Surface Authorization, Audit, and Rate Limits | `_bmad-output/implementation-artifacts/epic-4-surface-authorization-audit-rate-limit-evidence.md` | Public, peer, admin, and `scrapctl` paths deny before side effects; audit and rate-limit evidence is bounded. | `PASS` for local/package surface scope. | Linked. Story 4.7 must add production-rehearsal closure proof. |
| Story 4.3 - OpenBao-Backed Encrypted Write and Read | `_bmad-output/implementation-artifacts/epic-4-openbao-encrypted-write-read-evidence.md` | Encrypted writes persist ciphertext, reads decrypt through normal path, Transit/key failures fail closed, redaction holds. | `PASS` for local crypto-path and OpenBao adapter scope. | Linked. Story 4.7 owns production outage drill proof. |
| Story 4.4 - Durable Envelope Rewrap Workflow | `_bmad-output/implementation-artifacts/epic-4-durable-envelope-rewrap-evidence.md` | Rewrap is Raft-owned, idempotent, interruption-safe, and does not rewrite Block payload bytes. | `PASS` for local durable rewrap scope. | Linked. Story 4.7 must aggregate closure and avoid overclaiming production rehearsal. |
| Story 4.5 - `scrapctl openbao bootstrap` Fresh Setup | `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-fresh-setup-evidence.md` | Fresh local/prod-like bootstrap initializes/unseals, mounts Transit, creates/verifies key, and emits redacted evidence using the official OpenBao client. | `PASS` for fresh bootstrap scope. | Linked. Story 4.7 should prefer the supported bootstrap path where practical. |
| Story 4.6 - `scrapctl openbao bootstrap` Idempotency and Incompatible State | `_bmad-output/implementation-artifacts/epic-4-openbao-bootstrap-idempotency-evidence.md` | Compatible reruns preserve state; incompatible state fails closed without unsafe mutation; bootstrap slice closure is explicit. | `PASS` for idempotency/incompatible-state scope. | Linked. Story 4.7 can close the aggregate bootstrap evidence row only after rehearsal and leak scans are complete. |

## Story 4.7 Acceptance Matrix

| AC | Evidence required | Current proof command or artifact | Decision | Gap |
| --- | --- | --- | --- | --- |
| AC-4.7.1 | Linked startup fail-closed, authz, audit, rate-limit, encrypted write/read, Transit outage, rewrap, bootstrap, idempotency, incompatible state, and redaction evidence with owning stories. | This artifact's prior evidence inventory plus `artifacts/production-rehearsal/report.json`. | `PASS` | None for local production-security rehearsal scope. Real S3/IAM remains Story 6.6. |
| AC-4.7.1a | Transit unavailable, auth denied, and wrong-key or missing-key production rehearsal drills fail closed without plaintext fallback or secret leakage. | `artifacts/production-rehearsal/runtime/fail-closed-drills/transit_unavailable/result.json`, `auth_denied/result.json`, and `missing_key/result.json`. | `PASS` | None for local production-security rehearsal scope. |
| AC-4.7.2 | `make production-rehearsal-security` artifacts include command, commit/ref, environment, expected result, actual result, artifact path, and redaction proof; local overrides are marked. | `artifacts/production-rehearsal/report.json` from `env GOCACHE=/tmp/scrap-v2-go-build make production-rehearsal-security`. | `PASS` | None. Report marks filesystem Backend as local-only and `real_s3_iam=false`. |
| AC-4.7.3 | Missing P0 security or secret-redaction evidence causes closure `FAIL`, not Epic 6 deferral. | This artifact's current closure decision and real S3/IAM row. | `PASS` | Local production-security rehearsal can pass. Real S3/IAM remains `FAIL` for real S3/IAM claims instead of being silently deferred. |

## Production Rehearsal Report Evidence

`artifacts/production-rehearsal/report.json` from
`env GOCACHE=/tmp/scrap-v2-go-build make production-rehearsal-security` records:

- `status`: `passed`
- `command`: `make production-rehearsal-security`
- `commit_ref`: `e7bb8c98c7fb9f8be3863f5cc9ea9a11c61c825e`
- `git_worktree_state`: `dirty`
- `git_diff_sha256`: `e9aebc9bf3ec59aabf03c89ad6701298b7e5473305e0ab27d6e1a82114672292`
- `timestamp`: `2026-06-12T15:52:26Z`
- `environment`: `production-rehearsal`
- `evidence_tier`: `local-production-security`
- `expected_result`: production mode with real OpenBao Transit, encrypted
  write/read, committed Backend upload confirmation, fail-closed drills, and
  redacted artifacts passes
- `actual_result`: production security rehearsal passed
- `artifact_path` and `report_path`: `artifacts/production-rehearsal/report.json`
- `shard_placement`: 1024 slots, local Shards `[7, 9]`, route map
  `0-511:shard=7,512-1023:shard=9`
- `security_mode`: `production`
- `production_readiness_status`: `ready`
- `backend`: `fs`
- `local_overrides.filesystem_backend`: `true`
- `local_overrides.real_s3_iam`: `false`
- `openbao_image`: `openbao/openbao:2.5.4`
- `openbao_transit`: `real`
- `openbao_bootstrap.command`: `scrapctl openbao bootstrap`
- `encrypted_write_read_ok`: `true`
- `backend_upload_confirmed`: `true`
- `confirmed_upload_count`: `1`
- `fail_closed_drills`: three `pass` rows
- `redaction_proof.status`: `passed`
- `redaction_proof.scan_artifact_path`:
  `artifacts/production-rehearsal/runtime/redaction-scan.json`

The report is intentionally local production-security proof. It does not claim
real non-local S3/IAM readiness.

The worktree was dirty when the local rehearsal was run because the Story 4.7
implementation and evidence updates had not yet been committed. The generated
report records the dirty tree and a diff SHA so the local evidence is
attributable to the reviewed change set instead of pretending to be a clean
release commit.

## Fail-Closed Drill Evidence

| Drill | Artifact | Expected result | Actual result |
| --- | --- | --- | --- |
| Transit unavailable | `artifacts/production-rehearsal/runtime/fail-closed-drills/transit_unavailable/result.json` | Write fails closed when OpenBao Transit is unavailable. | `pass`; write failed closed without plaintext fallback; plaintext and secret leak scans marked true. |
| Auth denied | `artifacts/production-rehearsal/runtime/fail-closed-drills/auth_denied/result.json` | Write fails closed when OpenBao denies the configured token. | `pass`; write failed closed without plaintext fallback; plaintext and secret leak scans marked true. |
| Missing Transit key | `artifacts/production-rehearsal/runtime/fail-closed-drills/missing_key/result.json` | Write fails closed when the configured Transit key is missing. | `pass`; write failed closed without plaintext fallback; plaintext and secret leak scans marked true. |

## Command Evidence

- `scripts/check-e2e-gates.sh` - PASS before Story 4.7 implementation. This
  proves the existing structural gate still recognizes the rehearsal target and
  current invariants, but does not close the missing Story 4.7 report/drill
  gaps.
- `scripts/check-e2e-gates.sh` - RED after adding production-rehearsal Shard
  placement guards; failed with missing Shard placement file in
  `scripts/production-rehearsal.sh`.
- `bash -n scripts/production-rehearsal.sh` - PASS.
- `bash -n scripts/check-e2e-gates.sh` - PASS.
- `git diff --check` - PASS.
- `scripts/check-e2e-gates.sh` - PASS after adding placement, metadata, drill,
  and bootstrap structural guards.
- `env GOCACHE=/tmp/scrap-v2-go-build make production-rehearsal-security` -
  PASS before code review fixes. Generated `artifacts/production-rehearsal/report.json`.
- `jq . artifacts/production-rehearsal/report.json` - PASS. Report includes
  release-grade metadata and three fail-closed drill rows.
- `make production-rehearsal-down` - PASS after artifact inspection.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS. This includes static
  checks, `go test ./...`, race tests, integration tests, and final binary
  builds.
- `bmad-code-review` - completed with patch findings for fail-closed drill false
  positives, auth-denied token setup, measured redaction proof, drill cleanup,
  unavailable-port validation, structural report validation, and dirty-tree
  attribution.
- `bash -n scripts/production-rehearsal.sh` - PASS after review fixes.
- `bash -n scripts/check-e2e-gates.sh` - PASS after review fixes.
- `scripts/check-e2e-gates.sh` - PASS after review fixes.
- `git diff --check` - PASS after review fixes.
- `env GOCACHE=/tmp/scrap-v2-go-build make production-rehearsal-security` -
  PASS after review fixes. Generated `artifacts/production-rehearsal/report.json`
  with `git_worktree_state=dirty` and diff SHA
  `e9aebc9bf3ec59aabf03c89ad6701298b7e5473305e0ab27d6e1a82114672292`.
- `jq . artifacts/production-rehearsal/runtime/redaction-scan.json` - PASS.
  Scan artifact recorded no forbidden material across bootstrap evidence, main
  logs, generated report, and drill stdout/stderr/log/result files.
- `make production-rehearsal-down` - PASS after review-fix rehearsal inspection.
- Strict shaped-secret scan over final story/evidence/scripts/report/drill
  outputs - PASS. Sensitive-value and payload scans over generated
  tracker-ready report/drill outputs - PASS. Plaintext payload scans over main
  and drill data directories - PASS.
- `env GOCACHE=/tmp/scrap-v2-go-build make check` - PASS after review fixes.
  This includes static checks, `go test ./...`, race tests, integration tests,
  and final binary builds.

## Redaction Evidence

Story 4.7 redaction scans passed over:

- `_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md`
- `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`
- `scripts/production-rehearsal.sh`
- `scripts/check-e2e-gates.sh`
- `artifacts/production-rehearsal/report.json`
- generated drill `result.json`, `write-error.txt`, and `write-response.json`
  files under `artifacts/production-rehearsal/runtime/fail-closed-drills/`
- main and drill data directories for literal plaintext payload scans

Results:

- Strict shaped-secret scan: PASS with no matches.
- Sensitive-value scan for token/unseal/private-key surfaces in tracker-ready
  generated evidence: PASS with no matches.
- Raw rehearsal payload scan in report/bootstrap/drill outputs: PASS with no
  matches.
- Main data directory plaintext payload scan: PASS, payload not found.
- Drill data directory plaintext payload scans: PASS for `auth_denied`,
  `missing_key`, and `transit_unavailable`, payloads not found.
- Script-measured redaction scan artifact:
  `artifacts/production-rehearsal/runtime/redaction-scan.json`, PASS with
  `forbidden_material_found=false`.

## Code Review Resolution

| Finding | Resolution |
| --- | --- |
| Fail-closed drills accepted any write failure as PASS. | Added `assert_expected_drill_error`; each drill now validates the expected gRPC code and bounded marker before writing `status=pass`. |
| Auth-denied drill used an arbitrary invalid token. | Added `create_auth_denied_token`; the drill now uses a short-lived OpenBao token with no Transit capabilities. |
| Redaction proof was asserted instead of measured. | Added generated-artifact redaction scans and `redaction-scan.json`; report generation is followed by measured scans. |
| Drill `scrapd` cleanup only ran on the success path. | Added active drill PID tracking to global cleanup. |
| Transit-unavailable drill depended on unchecked port arithmetic. | Added port validation, port-65535 overflow handling, and an explicit unavailable endpoint probe. |
| Structural gates did not require generated report validation. | Added `assert_report_invariants` and tightened `scripts/check-e2e-gates.sh` to require validators and stricter report markers. |
| Commit/ref evidence did not identify dirty-tree attribution. | Added `git_worktree_state` and `git_diff_sha256` to the generated report and closure evidence. |

## Changed Boundaries

Expected:

- `_bmad-output/implementation-artifacts`
- `scripts/production-rehearsal.sh`
- `scripts/check-e2e-gates.sh` if structural report invariants are added
- `docs/production-rehearsal.md` only if the operator contract changes
- narrow tests/helpers near the script or evidence parsing boundary if needed

Actual:

- `_bmad-output/implementation-artifacts/4-7-production-security-rehearsal-evidence-closure.md`
- `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`
- `_bmad-output/implementation-artifacts/sprint-status.yaml`
- `scripts/production-rehearsal.sh`
- `scripts/check-e2e-gates.sh`

No ADR was required: the implementation stays inside the existing rehearsal
script, structural gate, and evidence artifact boundaries. It does not change
Document identity, Backend object identity, storage format, Block/Frame layout,
Raft commands, envelope metadata, public/peer/admin wire contracts, OpenBao
client package ownership, or production OpenBao lifecycle ownership.
