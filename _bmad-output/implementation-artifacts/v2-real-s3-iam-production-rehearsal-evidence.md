# V2 Real S3/IAM Production Rehearsal Evidence

Artifact status: complete; real S3/IAM release evidence PASS on 2026-06-13
Release gate status: PASS

Story: 6.6 - Real S3/IAM Production Rehearsal Closure

## Scope

This artifact records the hard release criteria for issue `#429` and the real
S3/IAM gate state. It does not replace the runtime report. A final release PASS
requires a sanitized `artifacts/production-rehearsal/report.json` from a real
non-local S3/IAM `env GOFLAGS=-buildvcs=false make production-rehearsal` run.

Tracker query:

```text
gh issue view 429 --repo petabytecl/scrap --json number,title,state,labels,milestone,url,updatedAt
```

Issue `#429` is closed by this release PR. The real non-local S3/IAM rehearsal
ran successfully and the sanitized report satisfies every hard criterion below,
so the gate is satisfied and `#429` is resolved.

## Run Provenance

- Command: `env GOFLAGS=-buildvcs=false make production-rehearsal`
- Tested commit/ref: `f615c7226173d6cc1804a1bba391209b6fee6b54` on branch
  `feat/real-aws-validation-iac`; `git_worktree_state=clean`.
- Backend: real non-local AWS S3 in `us-east-2` (`SCRAP_S3_REGION=us-east-2`).
- IAM provenance: credentials from the default provider chain via a dedicated
  least-privilege IAM role (`AssumeRole` from the AWS SSO session). The role
  grants only `s3:PutObject`/`s3:GetObject` under the Cell prefix,
  `s3:ListBucket` (prefix-scoped), and `s3:GetBucketLocation`; out-of-scope
  writes are denied.
- Endpoint: `SCRAP_S3_ENDPOINT` unset (real AWS endpoint);
  `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` not used.
- Report timestamp: `2026-06-13T04:13:41Z`.
- Infrastructure: provisioned by `deploy/aws/validation/scrap-real-aws-validation.yaml`.

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Real S3/IAM production rehearsal | PASS | real S3/IAM report captured 2026-06-13 | report `artifacts/production-rehearsal/report.json` proves every criterion under a least-privilege role; issue `#429` closed by this release PR; command `env GOFLAGS=-buildvcs=false make production-rehearsal`; owner Release owner. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Issue | Report fields | Redaction proof | Freshness | Status | Owner | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.6 Real S3/IAM production rehearsal | `env GOFLAGS=-buildvcs=false make production-rehearsal` | tested `f615c7226173d6cc1804a1bba391209b6fee6b54` on branch `feat/real-aws-validation-iac`; clean worktree | Real non-local S3/IAM in `us-east-2`; `SCRAP_S3_BUCKET` and `SCRAP_S3_REGION` set; credentials from default provider chain via a dedicated least-privilege role; `SCRAP_S3_ENDPOINT` unset; `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3` not used | S3 Backend report proves production mode, real OpenBao Transit, encrypted write/read, committed Backend upload confirmation, and redacted artifacts. | PASS: real non-local S3/IAM run succeeded under the dedicated least-privilege validation role and the sanitized report proves every criterion. | sanitized `artifacts/production-rehearsal/report.json`; committed criteria artifact `_bmad-output/implementation-artifacts/v2-real-s3-iam-production-rehearsal-evidence.md`. | issue `#429` closed by this release PR: https://github.com/petabytecl/scrap/issues/429 | Observed `status=passed`, `command=make production-rehearsal`, `evidence_tier=real-s3-iam`, `backend=s3`, `local_overrides.real_s3_iam=true`, `local_overrides.local_s3_endpoint_allowed=false`, `security_mode=production`, `production_readiness_status=ready`, `openbao_transit=real`, `test_hooks_enabled=false`, `pprof_enabled=false`, `encrypted_write_read_ok=true`, `plaintext_leak_scan_ok=true`, `backend_upload_confirmed=true`, `confirmed_upload_count >= 1` (observed 1), and `redaction_proof.status=passed`. | Redaction proof excludes secrets, tokens, raw Backend keys, validation tokens, raw logs, Document payloads, private material, generated certificate material, raw bucket object keys, trace IDs, and request IDs. | Real S3/IAM run captured 2026-06-13T04:13:41Z. | PASS | Release owner | None; the gate is satisfied and issue `#429` is closed by this release PR. |

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
