# V2 Evidence Collection Runbook

## Purpose

Use this runbook to collect release-relevant operator evidence without leaking
sensitive material or over-claiming final V2 readiness.

## Owning Feature Epic or Release Gate

Epic 6 owns release evidence aggregation. FR-15 covers the OTel evidence plane;
FR-16 covers current linked evidence, runbooks, final gate status, and issue
`#429`.

## Symptoms

- A release claim lacks command, commit/ref, environment, artifact path,
  expected result, actual result, timestamp, or redaction proof.
- Evidence is local-only but is being treated as final release proof.
- A tracker comment needs a sanitized artifact summary.

## Normal Path

```sh
scrapctl evidence bundle throughput --admin-url <admin-url> \
  --bundle-dir evidence/runbooks/release
make tier2-e2e-up
make tier3-evidence-up STRESS_SCENARIO=throughput
make production-rehearsal-security
```

Use real S3/IAM only when the environment is intentionally configured for final
Backend proof:

```sh
make production-rehearsal
```

## Failure Path

1. If evidence bundle generation fails, preserve the bundle path and gate
   failure reason.
2. If Tier 2 or Tier 3 fails, do not close the release claim.
3. If production rehearsal security fails, keep production security/Transit
   readiness open.
4. If real S3/IAM proof is absent, keep issue `#429` open.

## Rollback or Escalation

Evidence collection should not mutate production storage state. Roll back only
the evidence environment or rehearsal deployment. Escalate missing required
release evidence to the release owner.

## Expected Outputs

- Evidence bundle path is printed and the gate passes or fails explicitly.
- Tier 2 and Tier 3 artifacts are linked to commit/ref and environment.
- Production rehearsal reports stay in ignored artifact paths.
- Final S3/IAM proof is separate from filesystem Backend security proof.

## Evidence Collection

Each release evidence row must include source requirement, command, artifact
path, environment, owner, timestamp, commit/ref, expected result, actual result,
redaction proof, freshness decision, release status, and mitigation or next
owner.

## Redaction Requirements

Do not paste credential values, private key material, generated certificates,
Document payloads, Backend object names, raw dependency output, trace IDs,
request IDs, auth claims, or runtime logs into public issues or pull requests.

## Authority Boundary

Evidence proves that a workflow was observed. It is not a storage authority and
must not override committed Shard state, Backend confirmation semantics, or
feature-specific failure behavior.

## References

- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
- `docs/prd-closure-policy.md`
- `docs/production-rehearsal.md`
- `internal/scrapctl/evidence.go`
- `Makefile`
