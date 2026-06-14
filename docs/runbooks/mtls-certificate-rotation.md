# SCRAP mTLS Certificate Rotation Runbook

## Purpose

Use this runbook to rotate SCRAP listener, client, or CA material for public,
peer, admin, or `scrapctl` paths.

## Owning Feature Epic or Release Gate

Epic 4 owns mTLS enforcement and production security mode. FR-16 requires
operator documentation and evidence for rotation workflows.

## Symptoms

- Clients receive TLS verification failures.
- Admin or peer requests fail after certificate replacement.
- A certificate is close to expiry, expired, or no longer trusted.

## Normal Path

1. Write the replacement certificate, private key file, and CA bundle to the
   mounted secret or host path for the affected surface.
2. Restart or roll the affected `scrapd` Members and `scrapctl` clients.
3. Wait for rollout:

```sh
kubectl --context <kube-context> -n scrap rollout status statefulset/scrapd
```

4. Verify startup and mTLS readiness:

```sh
scrapctl status --admin-url <admin-url> --output=json \
  --tls-cert <client-cert> --tls-key <client-key> \
  --tls-ca <ca-bundle> --tls-server-name <server-name>
make production-rehearsal-security
```

## Failure Path

If rollout or readiness fails, check certificate validity, trust chain, server
name, client identity, file permissions, and secret mount delivery. Do not
disable mTLS to restore service.

## Rollback or Escalation

Rollback by restoring the last known-good certificate set and rolling affected
Members. Escalate to the certificate owner when the CA bundle, peer identity,
or client identity cannot be validated.

## Expected Outputs

- `kubectl rollout status` completes for `statefulset/scrapd`.
- `scrapctl status` succeeds with the rotated client certificate.
- Production rehearsal succeeds without enabling test-only shortcuts.

## Evidence Collection

Record the rollout command, `scrapctl status` command, rehearsal command,
commit/ref, environment, expected and actual outcomes, and sanitized artifact
paths. Do not store generated certificate material in the evidence artifact.

## Redaction Requirements

Never paste certificate private key material, generated cert material,
credential values, unredacted log output, Document payloads, Backend object
names, trace IDs, request IDs, or auth claims.

## Authority Boundary

Certificate validity proves transport identity only. It does not prove Shard
membership, Document visibility, Backend upload, or restore authority.

## References

- `docs/production-rehearsal.md#Certificate Rotation`
- `docs/adr/0019-production-security-boundary.md`
- `docs/adr/0024-production-topology-and-peer-scope-policy.md`
