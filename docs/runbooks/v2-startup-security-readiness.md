# V2 Startup and Security Readiness Runbook

## Purpose

Use this runbook when a Cell fails to start, reports unhealthy readiness, or
appears to be running with the wrong production security posture.

## Owning Feature Epic or Release Gate

Epic 4 owns production security mode, mTLS, authorization, audit, and rate
limits. Epic 6 owns final evidence aggregation through FR-16.

## Symptoms

- `scrapd` does not become ready.
- Admin status reports production readiness failure.
- `SCRAP_TEST_HOOKS` or unauthenticated pprof appears in a prod-like or
  production render.
- Public, peer, or admin calls fail mTLS verification.

## Normal Path

```sh
scrapctl doctor --context <kube-context> --namespace scrap --output=json
scrapctl status --admin-url <admin-url> --output=json
make production-rehearsal-security
```

For deployed prod-like Cells, also verify the rollout:

```sh
kubectl --context <kube-context> -n scrap rollout status statefulset/scrapd
```

## Failure Path

1. Confirm the target environment and Cell identity with `scrapctl doctor`.
2. Confirm admin readiness and security status with `scrapctl status`.
3. Inspect deployment renders for test hook or pprof exposure only through
   checked-in manifest paths and validation scripts, not by editing live pods.
4. If startup fails because required production files or policies are missing,
   correct the secret/config delivery path and roll the affected Members.

## Rollback or Escalation

Rollback is a controlled deployment rollback or Member rollout to a known-good
revision. Escalate to the platform owner if certificate, role policy, audit, or
rate-limit material is unavailable or cannot be trusted.

## Expected Outputs

- `scrapctl doctor` reports the target Cell and dependencies as reachable.
- `scrapctl status` reports production readiness fields without enabling
  test-only shortcuts.
- `make production-rehearsal-security` writes a report under
  `artifacts/production-rehearsal/` and exits successfully.

## Evidence Collection

Record command, commit/ref, environment, expected result, actual result,
artifact path, timestamp, and redaction proof. Keep generated runtime material
under ignored `artifacts/` paths and attach only sanitized summaries.

## Redaction Requirements

Do not paste credential values, private key material, generated certificate
material, Document payloads, Backend object names, unredacted log output,
request IDs, trace IDs, or auth claims into tracker comments.

## Authority Boundary

Admin readiness and production rehearsal are operational evidence. They do not
replace committed Shard state or prove Backend durability by themselves.

## References

- `docs/production-rehearsal.md`
- `_bmad-output/implementation-artifacts/epic-4-production-security-rehearsal-closure-evidence.md`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
