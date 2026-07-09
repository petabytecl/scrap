# SCRAP Real S3/IAM Production Rehearsal Evidence

Artifact status: reconciled 2026-07-09 to FAIL; historical PASS on `f615c722` is stale (H-19)
Release gate status: FAIL

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

## Scope

This artifact records the hard release criteria for issue `#429` and the real
S3/IAM gate state. A final release PASS requires a sanitized
`artifacts/production-rehearsal/report.json` from a real non-local S3/IAM
`env GOFLAGS=-buildvcs=false make production-rehearsal` run whose `commit_ref`
matches the exact candidate release SHA (`RELEASE_SHA` or `git rev-parse HEAD`).

Historical issue `#429` closure and the 2026-06-13 report at `f615c722` remain
progress evidence only. They cannot certify the thermo-nuclear remediation
baseline or any later candidate SHA (finding `H-19` / Story 6.10).

## Run Provenance

- Command: `env GOFLAGS=-buildvcs=false make production-rehearsal`
- Historical tested commit/ref: `f615c7226173d6cc1804a1bba391209b6fee6b54`
  (stale relative to remediation baseline `03798da` and current HEAD).
- Required for PASS: `commit_ref` equals exact candidate SHA with freshness.
- Backend: real non-local AWS S3 (historical run in `us-east-2`).
- Report path: `artifacts/production-rehearsal/report.json` (stale commit_ref).

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | FAIL | stale `commit_ref` vs current HEAD / remediation baseline | Historical report at `f615c722` cannot certify current SHA; rerun exact-SHA rehearsal before PASS. Owner Release owner. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Issue | Report fields | Redaction proof | Freshness | Status | Owner | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.6 Real S3/IAM production rehearsal | `env GOFLAGS=-buildvcs=false make production-rehearsal` | required: exact candidate SHA; historical `f615c722` is stale | Real non-local S3/IAM; `SCRAP_S3_BUCKET` and `SCRAP_S3_REGION` set; credentials from default provider chain; `SCRAP_S3_ENDPOINT` unset; `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` not used | S3 Backend report proves production mode, real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, and redacted artifacts on the exact release SHA. | FAIL: report `commit_ref` does not match current HEAD / RELEASE_SHA (H-19). Historical `#429` closure is progress evidence only. | Expected sanitized `artifacts/production-rehearsal/report.json` with matching `commit_ref`. | issue `#429` historically closed; exact-SHA revalidation required | Requires `status=passed`, `command=make production-rehearsal`, `evidence_tier=real-s3-iam`, `backend=s3`, `local_overrides.real_s3_iam=true`, `local_overrides.local_s3_endpoint_allowed=false`, and `confirmed_upload_count >= 1` on the exact candidate SHA; historical report fields are not accepted while `commit_ref` mismatches HEAD. | Redaction proof: artifact excludes secrets, tokens, raw Backend keys, Document payloads, private material, and raw logs. | Stale relative to remediation baseline 2026-07-09. | FAIL | Release owner | Rerun real non-local rehearsal on the candidate SHA; keep release below PASS until exact-SHA evidence lands. |

## Hard Criteria

PASS is allowed only when the full evidence row links a sanitized
`artifacts/production-rehearsal/report.json` produced by real non-local S3/IAM
whose `commit_ref` matches `RELEASE_SHA` or `git rev-parse HEAD`, and the report
fields satisfy every criterion in the Story 6.6 contract.

Hard pass/fail criteria reject vague, screenshot-only, localhost-only,
LocalStack-only, local-only, stale, unlinked, or missing IAM provenance.

## Redaction Review

The committed artifact contains only command names, field names, status labels,
issue metadata, and release criteria. It does not contain credential values,
private keys, generated certificate material, Document payloads, raw Backend
keys, raw bucket object keys, validation tokens, raw logs, data keys, or
wrapped-key ciphertext.
