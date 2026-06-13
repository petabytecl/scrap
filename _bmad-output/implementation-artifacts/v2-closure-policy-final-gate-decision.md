# V2 Closure Policy Final Gate Decision

Artifact status: updated 2026-06-13 after V2 landed on main; real S3/IAM gate resolved
Final gate status: FAIL

Story: 6.7 - V2 Closure Policy and Final Gate Decision

## Policy Review

V2 has no intermediate releases. Closed issues, merged PRs, and closed phase
milestones are progress evidence, not release PASS proof without current linked
evidence.

`docs/prd-closure-policy.md` records the V2 major-release closure rule, the
distinction between progress evidence and release evidence, and the non-waivable
blocker list. Non-waivable blockers include required P0 feature evidence,
production security evidence, Tier 2/Tier 3 release evidence, real S3/IAM
evidence, redaction proof, and ownered mitigation for every release blocker.

## Source Inputs

| Input | Command or path | Result |
| --- | --- | --- |
| Branch | `git branch --show-current` | `v2`, now merged into `main` (the V2 rewrite replaced main's content). |
| Reviewed/tested head | `git rev-parse HEAD` | `89cbc50` on `main`. |
| Closure policy | `docs/prd-closure-policy.md` | V2 no-intermediate-release and non-waivable blocker policy. |
| Release matrix | `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` | Feature scope through Epic 5 current; Epic 6 evidence gates tracked here. |
| Issue `#429` (real S3/IAM) | `gh issue view 429 --repo petabytecl/scrap` | `CLOSED` (completed) via PR #435; real S3/IAM rehearsal evidence accepted. |
| Real S3/IAM report | `artifacts/production-rehearsal/report.json` | `status=passed`, `evidence_tier=real-s3-iam`, `confirmed_upload_count=1`; gate `scripts/check-real-s3-iam-gate.sh` PASS. |
| Tier 2 prod-like E2E run | `gh run view 27457877436` | PASS once: https://github.com/petabytecl/scrap/actions/runs/27457877436 (suite is flaky under load — see issue #437). |
| Tier 3 evidence-gate runs | `gh run list --workflow evidence-gate.yml` | FAIL: blocked by flaky E2E suite (#437) and unvalidated evidence stack/stress phases (#438). |
| Latest pushed CI | `gh run list --branch main --workflow ci` | `ci` run green for `89cbc50`: https://github.com/petabytecl/scrap/actions/runs/27459061473. |
| Latest pushed CodeQL | `gh run list --branch main` | `CodeQL` run green for `89cbc50`: https://github.com/petabytecl/scrap/actions/runs/27459061343. |
| Follow-up issues | `gh issue view 437 / 438` | #437 flaky multi-member E2E suite; #438 Tier 3 evidence-stack/stress validation. |
| Non-goal source | `docs/v2-scope-reconciliation.md` | Confirms explicit non-goals and final gate ordering. |
| Domain source | `CONTEXT.md` | S.C.R.A.P. is not an S3-compatible API; `tenant_id` is not storage identity. |

## Gate Summary

| Gate | Status | Evidence | Owner / next action |
| --- | --- | --- | --- |
| Final V2 release gate | FAIL | Real S3/IAM resolved (issue `#429` closed, report linked); Tier 2 passed once (run 27459061473 ci green for `89cbc50`, CodeQL green); but the multi-member E2E suite is flaky under load (#437) and Tier 3 evidence-stack/stress phases are unvalidated (#438), so no reliable green Tier 3 runtime evidence exists. | Release owner: stabilize the flaky E2E suite (#437), complete Tier 3 evidence validation (#438), then re-decide. |

## Full Blocker Rows

| Requirement | Source | Evidence command | Commit/ref | Environment | Evidence artifact | Issue/Run | Expected result | Actual result | Redaction proof | Freshness | Status | Owner | Mitigation | Next action |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.7 Final V2 release gate | Story 6.7 / FR-16 / DG-5 | `scripts/check-v2-closure-gate.sh` | `89cbc50` | Release evidence/docs | `_bmad-output/implementation-artifacts/v2-closure-policy-final-gate-decision.md` | issues `#429` closed, `#437`/`#438` open; ci green https://github.com/petabytecl/scrap/actions/runs/27459061473; CodeQL green https://github.com/petabytecl/scrap/actions/runs/27459061343 | Final V2 release PASS only with current linked evidence for every required gate. | FAIL: real S3/IAM is resolved and Tier 2 passed once, but the multi-member E2E suite is flaky under CI load (#437) and the Tier 3 evidence stack/stress phases are not yet validated end-to-end (#438), so reliable green Tier 3 runtime evidence is missing. | Redaction proof PASS: artifact excludes secrets, raw Backend keys, raw logs, Document payloads, private material, trace IDs, request IDs, auth claims, data keys, wrapped-key ciphertext, and host-absolute paths. | Current live check. | FAIL | Release owner | Stabilize flaky multi-member E2E tests (#437) and validate the Tier 3 evidence stack + stress/bundle phases (#438). | Land #437 and #438, capture a green Tier 3 run, then re-run this gate. |

## Gap Table

| Gap | Status | Owner | Mitigation | Next action | Freshness | Release status |
| --- | --- | --- | --- | --- | --- | --- |
| Real S3/IAM production rehearsal | PASS | Release owner | Real non-local `make production-rehearsal` run under a least-privilege IAM role; sanitized `artifacts/production-rehearsal/report.json` committed; issue `#429` closed. | None; gate satisfied and tracked in `v2-real-s3-iam-production-rehearsal-evidence.md`. | Current: report timestamp 2026-06-13, gate checker PASS. | PASS |
| Tier 2 prod-like runtime evidence | CONCERNS | Release owner | Tier 2 `prodlike-e2e` passed once on Linux CI (run 27457877436), but the multi-member E2E suite is flaky under load. | Stabilize the flaky suite (#437) so Tier 2 is reliably green and link a durable artifact. | Current: one green run exists; reliability blocked by #437. | CONCERNS |
| Tier 3 telemetry/evidence bundle | FAIL | Release owner | Evidence-stack non-root/read-only-rootfs bugs fixed (mimir, alloy, pyroscope); E2E gate flakiness (#437) and unexercised stress/bundle phases (#438) still block a green run. | Land #437 and #438, then capture a durable Tier 3 bundle (`manifest.json`, `gates.json`, `privacy-scan.json`, logs/metrics/traces/profiles, retention, privacy PASS). | Current: stack fixes committed at `89cbc50`; runtime bundle missing. | FAIL |

## Epic Rollup

| Epic | Status | Artifact | Command/ref | Owner | Release status |
| --- | --- | --- | --- | --- | --- |
| Epic 1 through Epic 6 | CONCERNS | `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md` | current release matrix; `scripts/check-v2-closure-gate.sh` | Release owner | FAIL |

## Non-Goal Review

| Item | Source | Scope decision | Release impact |
| --- | --- | --- | --- |
| S3-compatible API | `docs/v2-scope-reconciliation.md` | Explicit non-goal unless re-chartered. | Out of scope; not a release blocker. |
| Public deletion API | `docs/v2-scope-reconciliation.md` | Explicit non-goal unless re-chartered. | Out of scope; not a release blocker. |
| `tenant_id` as storage identity | `CONTEXT.md`; `docs/v2-scope-reconciliation.md` | Explicit non-goal unless re-chartered by ADR/PRD. | Out of scope; not a release blocker. |
| Cell federation | `docs/v2-scope-reconciliation.md` | Explicit non-goal unless re-chartered. | Out of scope; not a release blocker. |
| Direct Backend ciphertext streaming | ADR 0027; `docs/v2-scope-reconciliation.md` | Rejected for V2 unless re-chartered. | Out of scope; not a release blocker. |

## Hard Criteria

Hard criteria reject local-only output, screenshots, stale artifacts, unlinked
terminal snippets, ownerless blockers, and non-waivable waiver bypasses. Final
release `PASS` also requires current green ci and CodeQL runs for the tested
release ref.

## Redaction Review

This committed artifact contains sanitized metadata only. It does not include
credential values, private keys, generated certificate material, Document
payloads, raw Backend keys, raw logs, trace IDs, request IDs, auth claims, data
keys, wrapped-key ciphertext, or host-absolute paths.
