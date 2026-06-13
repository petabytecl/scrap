# V2 Release Tier Gates Evidence

Artifact status: complete for Story 6.5 implementation
Release gate status: FAIL

Story: 6.5 - Tier 2 and Tier 3 Release Evidence Gates
Story baseline commit: `06f2b4f120a165f7034afe74e822d5e7f4ad294c`
Story creation commit: `d2a9bcc264e1a74273b6cf2f52c7b855e7fd8d20`
Branch: `v2`
Generated: 2026-06-12T20:08:34-04:00

## Scope

This artifact links the existing Tier 2 and Tier 3 release-gate surfaces into
V2 closure using hard PASS, CONCERNS, and FAIL criteria. It does not claim final
release readiness. Real S3/IAM proof remains Story 6.6 and issue `#429`; the
final V2 decision remains Story 6.7.

Each gate row records command, commit/ref, environment, expected result, actual
result, artifact path, timestamp, redaction proof, freshness, owner, mitigation,
and artifact retention.

## Source Inputs

| Input | Command or path | Result |
| --- | --- | --- |
| Tier 2 Make target | `make tier2-e2e-up` | Target exists and is wired through `scripts/check-e2e-gates.sh`. It runs `make tier2-e2e` after bringing up the prod-like Kind/Cilium Cell. |
| Tier 3 Make target | `make tier3-evidence-up STRESS_SCENARIO=throughput` | Target exists and delegates to `scrapctl evidence bundle` through `scripts/evidence-bundle.sh`. |
| Tier 2 dedicated workflow | `gh run list --repo petabytecl/scrap --workflow prodlike-e2e.yml --branch v2 --limit 10` | `HTTP 404`: workflow is not on the default branch, so it cannot be queried or dispatched by workflow name yet. |
| Tier 3 dedicated workflow | `gh run list --repo petabytecl/scrap --workflow evidence-gate.yml --branch v2 --limit 10` | `HTTP 404`: workflow is not on the default branch, so it cannot be queried or dispatched by workflow name yet. |
| Current `v2` push CI | `gh run view 27450173124 --repo petabytecl/scrap --json status,conclusion,url,headSha,jobs` | Success for commit `d2a9bcc264e1a74273b6cf2f52c7b855e7fd8d20`; `e2e` job skipped because push events do not request Tier 2 E2E. Run URL: `https://github.com/petabytecl/scrap/actions/runs/27450173124`. |
| Current CodeQL | `gh run view 27450173131 --repo petabytecl/scrap --json status,conclusion,url,headSha` | Success for commit `d2a9bcc264e1a74273b6cf2f52c7b855e7fd8d20`; not Tier 2 or Tier 3 runtime proof. |
| Static gate wiring | `scripts/check-e2e-gates.sh` | Updated to require this artifact through `scripts/check-release-tier-gates.sh`. |

## Gate Summary

| Gate | Status | Freshness | Evidence |
| --- | --- | --- | --- |
| Tier 2 prod-like Cilium | CONCERNS | Current target and workflow wiring; no green current runtime run. | Command `make tier2-e2e-up`; GitHub Actions workflow `prodlike-e2e.yml`; CI carrier can run `make tier2-e2e-up` through workflow dispatch; expected artifact `artifacts/tier2-e2e.log`; screenshot, unlinked output, stale output, and local-only output are rejected. |
| Tier 3 evidence bundle | FAIL | Current target and workflow wiring; no current bundle linked. | Command `make tier3-evidence-up STRESS_SCENARIO=throughput`; GitHub Actions workflow `evidence-gate.yml`; expected artifact `artifacts/tier3-bundle-path.txt`; bundle must include `manifest.json`, `gates.json`, and `privacy-scan.json`; screenshot, unlinked output, stale output, and local-only output are rejected. |

## Full Evidence Rows

| Requirement | Command | Commit/ref | Environment | Expected result | Actual result | Artifact path | Timestamp | Redaction proof | Freshness | Status | Owner | Mitigation | Retention |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| AC-6.5.1 Tier 2 prod-like Cilium | `make tier2-e2e-up` through `prodlike-e2e.yml` or `ci.yml` workflow dispatch | Required: current implementation commit; current observed push CI ref is `d2a9bcc264e1a74273b6cf2f52c7b855e7fd8d20` | Prod-like Kind Cell with Cilium, `SCRAP_E2E_CELL_ID="kind-prodlike"`, security evidence report under `artifacts/prodlike-security/security-evidence.json` | Deployed gateway behavior, security posture, Backend upload/cold-read-relevant E2E coverage, multi-Shard restart/routing evidence, light scrub, and prod-like security evidence pass. | CONCERNS: Makefile/workflow wiring exists, but dedicated workflow is unavailable until present on default branch and the latest push CI run skipped `e2e`. | Expected durable artifact: `ci-tier2-e2e-<run_id>` or `tier2-prodlike-e2e-<run_id>` containing `artifacts/tier2-e2e.log` and Kind diagnostics. No current runtime artifact is linked. | 2026-06-12T20:08:34-04:00 | Current committed rows include no raw workflow logs, credentials, generated key material, Document payloads, raw Document identifiers, Backend keys, trace IDs, request IDs, auth claims, or host-absolute local paths. | Missing current runtime run. | CONCERNS | Release owner / Story 6.5 | After this implementation commit is pushed, run `gh workflow run ci.yml --ref v2` or use the dedicated `prodlike-e2e.yml` once it is on the default branch; link the run URL, artifact name, artifact path, and tested commit/ref. | GitHub Actions artifact retention must be tracked from the run. If the artifact will expire before release review, copy the sanitized evidence to durable release storage or mark stale. Local-only output is not final proof. |
| AC-6.5.2 Tier 3 evidence bundle | `make tier3-evidence-up STRESS_SCENARIO=throughput` through `evidence-gate.yml` or a durable local run | Required: current implementation commit; no current Tier 3 run exists | Tier 3 evidence Cell with logs, metrics, traces, profiles, stress run, and Story 6.4 bundle privacy gate | Logs, metrics, traces, profiles, `manifest.json`, `gates.json`, and `privacy-scan.json` are present; final metadata was privacy-scanned after creation; leak-scan status is PASS. | FAIL: workflow and Make targets exist, but dedicated workflow is unavailable until present on default branch and no current Tier 3 bundle path is linked. | Expected durable artifact: `tier3-evidence-<scenario>-<run_id>` containing `artifacts/tier3-bundle-path.txt`, `artifacts/tier3-evidence.log`, and the `evidence/` bundle with `manifest.json`, `gates.json`, and `privacy-scan.json`. | 2026-06-12T20:08:34-04:00 | Story 6.4 privacy contract requires the final `manifest.json` and `gates.json` to be scanned by `privacy-scan.json`; this Story 6.5 artifact does not paste raw runtime logs or bundle contents. | Missing current runtime bundle. | FAIL | Release owner / Story 6.5 | Run `evidence-gate.yml` once available on the default branch, or run `make tier3-evidence-up STRESS_SCENARIO=throughput` and promote the sanitized bundle to durable reviewable storage; link the bundle path and tested commit/ref. | GitHub Actions artifact retention or durable storage retention must be explicit. Local ignored `evidence/` and `artifacts/` paths are development evidence only unless copied to durable reviewable storage. |

## Pass and Fail Criteria

Tier 2 passes only when all of the following are true:

- A current GitHub Actions run or durable release artifact links the exact command
  `make tier2-e2e-up`.
- The tested commit/ref matches the release claim.
- The artifact includes `artifacts/tier2-e2e.log` and Kind diagnostics.
- The run result is green and includes the prod-like Kind/Cilium environment.
- The artifact retention decision is recorded.

Tier 3 passes only when all of the following are true:

- A current GitHub Actions run or durable release artifact links the exact command
  `make tier3-evidence-up STRESS_SCENARIO=<scenario>`.
- The tested commit/ref matches the release claim.
- The artifact includes `artifacts/tier3-bundle-path.txt`, `artifacts/tier3-evidence.log`,
  and the referenced `evidence/` bundle.
- The bundle includes `manifest.json`, `gates.json`, and `privacy-scan.json`.
- `privacy-scan.json` covers final metadata and reports PASS.
- Logs, metrics, traces, profiles, and leak-scan results are present or a
  missing item is recorded as FAIL/CONCERNS with owner and mitigation.
- The artifact retention decision is recorded.

The gate fails or records CONCERNS when evidence is screenshot-only, stale,
local-only, unlinked, missing artifact retention, missing tested commit/ref, or
inconsistent with the current release claim.

## Retention

GitHub Actions artifact retention is bounded by the repository, organization, or
enterprise retention setting. Release evidence linked only to an expiring
artifact becomes stale when that artifact expires. Local-only artifacts under
ignored `artifacts/` or `evidence/` paths are not final release proof unless
their sanitized contents are copied to durable reviewable storage and linked
from the release evidence matrix.

## Current Decision

Story 6.5 establishes and validates the Tier 2/Tier 3 closure contract, but V2
release readiness remains FAIL:

- Tier 2 has current target/workflow wiring and a CI carrier, but no current
  green Tier 2 runtime run is linked.
- Tier 3 has current target/workflow wiring, but no current evidence bundle is
  linked.
- Real S3/IAM proof remains Story 6.6 and issue `#429`.
- The final V2 release decision remains Story 6.7.
