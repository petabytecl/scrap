# V2 Real S3/IAM Production Rehearsal Evidence

Artifact status: complete for Story 6.6 validation
Release gate status: FAIL

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

## Scope

This artifact records the hard release criteria for issue `#429` and the current
real S3/IAM gate state. It does not replace the runtime report. A final release
PASS requires a sanitized `artifacts/production-rehearsal/report.json` from a
real non-local S3/IAM `env GOFLAGS=-buildvcs=false make production-rehearsal`
run.

Current tracker query:

```text
gh issue view 429 --repo petabytecl/scrap --json number,title,state,labels,milestone,url,updatedAt
```

Query result summary at `2026-06-12T20:42:34-04:00`: issue `#429` is `OPEN`,
labels are `ready-for-human`, `production-readiness`, `v2`, and `e2e`,
milestone is `NONE`, and `updatedAt` is `2026-06-10T02:56:17Z`.

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | FAIL | current gate contract; missing real non-local S3/IAM report | issue `#429` open; command `env GOFLAGS=-buildvcs=false make production-rehearsal`; expected report path `artifacts/production-rehearsal/report.json`; owner Release owner. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Issue | Report fields | Redaction proof | Freshness | Status | Owner | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.6 Real S3/IAM production rehearsal | `env GOFLAGS=-buildvcs=false make production-rehearsal` | baseline `794c0f16e951c2186aeea573a448c39123736ed8`; Story 6.6 implementation commit pending | Real non-local S3/IAM; `SCRAP_S3_BUCKET` and `SCRAP_S3_REGION` required; credentials from default provider chain/profile/workload identity; `SCRAP_S3_ENDPOINT` unset or real non-local; `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` not used | S3 Backend report proves production mode, real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, and redacted artifacts. | FAIL: real non-local S3/IAM run is not available yet and issue `#429` remains open. | Expected sanitized `artifacts/production-rehearsal/report.json`; this committed artifact is `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`. | issue `#429` open: https://github.com/petabytecl/scrap/issues/429 | Requires `status=passed`, `command=make production-rehearsal`, `evidence_tier=real-s3-iam`, `backend=s3`, `local_overrides.real_s3_iam=true`, `local_overrides.local_s3_endpoint_allowed=false`, `security_mode=production`, `production_readiness_status=ready`, `openbao_transit=real`, `test_hooks_enabled=false`, `pprof_enabled=false`, `encrypted_write_read_ok=true`, `plaintext_leak_scan_ok=true`, `backend_upload_confirmed=true`, `confirmed_upload_count >= 1`, and `redaction_proof.status=passed`. | Redaction proof must exclude secrets, tokens, raw Backend keys, validation tokens, raw logs, Document payloads, private material, generated certificate material, raw bucket object keys, trace IDs, and request IDs. | Current gate contract; missing current real S3/IAM run. | FAIL | Release owner | Run real non-local S3/IAM rehearsal, attach/link sanitized report and tested commit/ref, then close or explicitly waive issue `#429` before final release PASS. |

## Hard Criteria

PASS is allowed only when the full evidence row links a sanitized
`artifacts/production-rehearsal/report.json` produced by real non-local S3/IAM
and the report fields satisfy every criterion in the row above.

Hard pass/fail criteria reject vague, screenshot-only, localhost-only,
LocalStack-only, local-only, stale, unlinked, or missing IAM provenance. Any use
of `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true` is development-only evidence and
cannot close issue `#429`.

## Redaction Review

The committed artifact contains only command names, field names, status labels,
issue metadata, and release criteria. It does not contain credential values,
private keys, generated certificate material, Document payloads, raw Backend
keys, raw bucket object keys, validation tokens, raw logs, data keys, or
wrapped-key ciphertext.
