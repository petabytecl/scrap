# V2 Restore Failures Runbook

## Purpose

Use this runbook when a read requires an evicted Block and restore-first cold
read behavior fails, times out, or returns a sanitized unavailable error.

## Owning Feature Epic or Release Gate

Epic 3 owns restore-first cold reads, restore failure semantics, and encryption
compatible restore evidence. FR-7, FR-8, ADR 0027, and FR-16 apply.

## Symptoms

- Reads that need evicted Block bytes fail closed.
- Backend transient failure, missing object, corrupt object, or checksum
  mismatch is reported through bounded public errors.
- Restore evidence marks local/package proof but not final release proof.

## Normal Path

```sh
scrapctl status --admin-url <admin-url> --output=json
scrapctl upload-pressure --admin-url <admin-url> --output=json
scrapctl evidence bundle pressure --admin-url <admin-url> \
  --bundle-dir evidence/runbooks/restore
make e2e-up
```

For controlled non-production exercises only:

```sh
scrapctl fault backend break --context <kube-context> \
  --environment prodlike --confirm <cell-id>
scrapctl fault backend restore --context <kube-context> \
  --environment prodlike --confirm <cell-id>
```

## Failure Path

1. Confirm the affected read path and Shard health through `scrapctl status`.
2. Confirm upload pressure and Backend dependency status.
3. If Backend is unavailable, restore the dependency and retry the read path.
4. If missing/corrupt Backend data is suspected, preserve evidence and escalate.
5. Do not publish partial restored bytes or bypass verification.

## Rollback or Escalation

Rollback recent deployment/config changes that affected Backend access or
encryption dependencies. Escalate to storage leadership for missing/corrupt
Backend data because file loss is catastrophic.

## Expected Outputs

- Restore succeeds only after full-Block verification and normal local read.
- Transient Backend failures fail closed and can recover after dependency
  restoration.
- Missing or corrupt Backend data remains failed closed.
- Public errors stay bounded and sanitized.

## Evidence Collection

Record status, pressure, evidence bundle, relevant e2e/integration command,
commit/ref, environment, expected and actual outcomes, and redaction proof.
Link Epic 3 closure artifacts when using existing package/local proof.

## Redaction Requirements

Do not paste Document payloads, Document names, Backend object names, credential
values, raw dependency output, trace IDs, request IDs, auth claims, or local
runtime paths.

## Authority Boundary

Restore follows committed Confirmed Upload Catalog metadata and full-Block
verification. Do not use Backend LIST/HEAD/object listings, local member files,
or telemetry as restore authority.

## References

- `docs/adr/0027-phase-5-restore-first-cold-reads.md`
- `_bmad-output/implementation-artifacts/epic-3-restore-first-cold-read-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-restore-failure-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-encryption-restore-evidence.md`
- `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`
