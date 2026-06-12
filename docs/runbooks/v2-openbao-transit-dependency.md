# V2 OpenBao Transit Dependency Runbook

## Purpose

Use this runbook when SCRAP cannot reach OpenBao Transit, envelope encryption
fails closed, or local/prod-like Transit bootstrap needs evidence.

## Owning Feature Epic or Release Gate

Epic 4 owns OpenBao envelope encryption, rewrap, and local/prod-like bootstrap.
FR-14 covers `scrapctl openbao bootstrap`; FR-16 covers release evidence.

## Symptoms

- Production startup fails because Transit is missing or unreachable.
- Encrypted writes or reads fail closed with a bounded dependency error.
- Production rehearsal security cannot complete Transit setup or verification.

## Normal Path

For local or prod-like bootstrap:

```sh
scrapctl openbao bootstrap --address <openbao-url> \
  --token-env <credential-env-name> \
  --mount-path transit --key-name scrap-documents \
  --environment prodlike \
  --evidence-path artifacts/openbao-bootstrap/evidence.json
make production-rehearsal-security
```

For production, confirm the platform-managed OpenBao contract is present:

- HTTPS Transit endpoint configured.
- Transit mount and key name configured.
- SCRAP receives the credential through the configured environment variable.
- NetworkPolicy and RBAC allow only the intended access path.

## Failure Path

1. Confirm `SCRAP_TRANSIT_ADDR`, mount, key, and credential-env variable names.
2. Confirm the OpenBao endpoint is HTTPS and reachable from SCRAP.
3. Confirm fake Transit is absent for readiness evidence.
4. If Transit is unavailable, leave SCRAP failed closed and escalate to the
   platform OpenBao owner.

## Rollback or Escalation

Rollback only local/prod-like bootstrap changes you own. Escalate production
OpenBao lifecycle, unseal, key policy, credential issuance, network reachability,
and disaster recovery to the platform owner.

## Expected Outputs

- `scrapctl openbao bootstrap` reports a redacted evidence artifact and no
  leaked credential material.
- `make production-rehearsal-security` reports real Transit usage and encrypted
  write/read success with filesystem Backend.

## Evidence Collection

Capture the redacted bootstrap evidence path, production rehearsal report path,
commit/ref, environment, expected and actual outcomes, and redaction status.

## Redaction Requirements

Do not paste OpenBao credentials, unseal material, private key material,
generated certificates, wrapped-key ciphertext, raw dependency output, Document
payloads, Backend object names, trace IDs, request IDs, or auth claims.

## Authority Boundary

OpenBao proves the encryption dependency path. It does not own production SCRAP
storage authority, Backend durability, Shard leadership, or Document visibility.

## References

- `docs/openbao-deployment-contract.md`
- `docs/production-rehearsal.md`
- `internal/scrapctl/openbao.go`
- `_bmad-output/implementation-artifacts/4-5-scrapctl-openbao-bootstrap-fresh-setup.md`
- `_bmad-output/implementation-artifacts/4-6-scrapctl-openbao-bootstrap-idempotency-and-incompatible-state.md`
