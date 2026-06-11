# Epic 2 Multi-Shard Evidence Closure

Date: 2026-06-11

Scope: Epic 2 feature-scope evidence for Shard-aware Cell operation. This is not final V2 release readiness, PRD closure, real S3/IAM rehearsal, or Epic 6 closure.

Ref: this Story 2.6 change set, based on `1a8f9d04729c72ffe33339189eebf26ffd52075b`.

## Gate Decision

Result: `CONCERNS`

Epic 2 has current passing evidence for deterministic routing, invalid startup failure, wrong-Shard peer denial, Shard-aware diagnostics, restart behavior, non-zero Shard Backend upload, and redaction/authority-boundary checks. The remaining concerns are intentionally outside this story's implementation scope:

- True multi-Shard rebuild command evidence is still missing because peer `RequestIndexRebuild` has no Shard-scoped request identity. Restart determinism and light-scrub mismatch evidence pass.
- Restore-first cold reads remain Epic 3 scope. Current security evidence records this as an encrypted restore concern instead of a false PASS.

## Evidence Matrix

| AC ID | Source Story | Evidence Command | Artifact or Test Path | Commit/Ref | Result | Next Action |
| --- | --- | --- | --- | --- | --- | --- |
| AC-2.6.1 | 2.1, 2.3, 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./test/e2e -run 'TestE2ETransactionForShardUsesRoutingPlacement|TestBackendPairsAcceptNonZeroShardPrefixes' -count=1` | `test/e2e/upload_e2e_test.go` | this change set | `PASS` | None. |
| AC-2.6.1 | 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build make tier2-e2e-up` | `test/e2e/multishard_evidence_e2e_test.go` (`TestE2EMultiShardRestartDeterminism`) | this change set | `PASS` | None for restart determinism. |
| AC-2.6.1, AC-2.6.3 | 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build make tier2-e2e-up` | `test/e2e/scrub_e2e_test.go` (`TestE2ELightScrubDetectsProjectionDivergence`), `internal/cmd/app_test.go` (`TestNewAppPeerServerAuthorizesOnlyValidatedLocalShards`) | this change set | `CONCERNS` | Add Shard-scoped rebuild request/command before claiming true multi-Shard rebuild PASS. |
| AC-2.6.2 | 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build make tier2-e2e-up` | `test/e2e/multishard_evidence_e2e_test.go` (`TestE2EMultiShardBackendUploadUsesNonZeroShard`) | this change set | `PASS` | None. |
| AC-2.6.2 | 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build make tier2-e2e-up` | `test/e2e/security_evidence_e2e_test.go`, `artifacts/prodlike-security/security-evidence.json` | this change set | `CONCERNS` | Complete Epic 3 Stories 3.4 and 3.7 before restore-first cold-read PASS. |
| AC-2.6.3 | 2.1, 2.2 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/routing ./internal/cmd -count=1` | `internal/routing`, `internal/cmd/routing_config_test.go`, `internal/cmd/app_test.go` | this change set | `PASS` | None. |
| AC-2.6.3 | 2.4 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/peer ./internal/cmd -count=1` | `internal/peer/authorization_test.go`, `internal/peer/audit_ratelimit_test.go`, `internal/cmd/app_test.go` | this change set | `PASS` | None. |
| AC-2.6.3 | 2.5, 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build go test ./internal/admin ./internal/scrapctl ./internal/cmd -count=1` | `internal/cmd/shard_diagnostics.go`, `internal/admin`, `internal/scrapctl`, `test/e2e/multishard_evidence_e2e_test.go` | this change set | `PASS` | None. |
| AC-2.6.3 | 2.6 | `env GOCACHE=/tmp/scrap-v2-go-build make manifests-check && env GOCACHE=/tmp/scrap-v2-go-build make gates-check && env GOCACHE=/tmp/scrap-v2-go-build make kind-cilium-check` | `deploy/kustomize/environments/prodlike-e2e/`, `scripts/check-*.sh` | this change set | `PASS` | None. |
| AC-2.6.2, AC-2.6.3 | 2.3, 2.6 | `rg -n "BackendKey|ListObjects|HeadObject|GetObject|/shards/|S3|backend object" internal/cmd/public_store_router.go internal/server internal/cmd/app.go internal/cmd/public_store_router_test.go` | Public routing and server code authority-boundary scan | this change set | `PASS` | Backend keys remain Backend object identity only; public routing continues to use Transaction placement. |
| AC-2.6.3 | 2.1-2.6 | `rg -n "10\\.1\\.2\\.3|private-key|backend-key|secret|/tmp/secret|tx-secret|invoice-secret|203\\.0\\.113\\.42|BEGIN (RSA \|EC \|OPENSSH )?PRIVATE KEY|OPENBAO_TOKEN|SCRAP_TLS_SCRAPCTL_KEY" <changed files>` | Story, E2E, deploy, and closure changed-file redaction scan | this change set | `PASS` | Review expected non-secret matches only: env var names, Kubernetes Secret references, and bounded fixture strings. |

## Backend Authority Boundary

Backend object keys are observed only through S3/LocalStack evidence paths and use the ADR 0009 shape:

```text
{cell_id}/shards/{shard_id}/{block_id}.blk
{cell_id}/shards/{shard_id}/{block_id}.idx
```

Public API calls still route by Transaction through `internal/routing` and the composition-root router. No public handler uses Backend object keys, Backend listing, pod names, local files, peer addresses, certificates, metrics, admin output, or evidence artifacts as Shard authority.

## Closure Notes

- `TestE2EMultiShardRestartDeterminism` writes Documents for Shards `7` and `9`, restarts the `scrapd` StatefulSet, then verifies both Transactions still route to their owning Shards and remain readable.
- `TestE2EMultiShardBackendUploadUsesNonZeroShard` writes and uploads a sealed Block on Shard `7`, verifies the uploaded `.blk` and `.idx` pair, and reads through the public API without parsing Backend keys for routing.
- `TestE2EProdlikeSecurityEncryptionEvidence` now waits for the Backend index pair containing the encrypted Document under test and records restore as `CONCERNS` when restore-first cold read evidence is unavailable.
- Multi-Shard admin composition now exposes aggregate upload pressure and routed rewrap/test-hook operations. Eviction plan/apply routes remain single-Shard-only until Shard-scoped operator workflows exist.
