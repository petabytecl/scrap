# V2 Block Quarantine Repair Runbook

## Purpose

Use this runbook when Deep Scrub isolates a corrupt Block and the affected
Documents are unreadable until repair completes.

## Owning Feature Epic or Release Gate

Block Quarantine and repair are part of the core durability and Deep Scrub
failure domain from Epics 1 and 2. FR-3 and FR-16 apply.

## Symptoms

- Deep Scrub reports Frame CRC or Document SHA-256 verification failure.
- A Block is isolated by quarantine marker state.
- Reads for Documents in the affected Block fail until repair completes.

## Normal Path

```sh
scrapctl status --admin-url <admin-url> --output=json
scrapctl evidence bundle pressure --admin-url <admin-url> \
  --bundle-dir evidence/runbooks/block-quarantine
```

For controlled non-production exercises only:

```sh
scrapctl fault block corrupt --context <kube-context> \
  --environment prodlike --confirm <cell-id>
```

## Failure Path

1. Confirm Shard and Member health through status.
2. Preserve scrub/quarantine evidence.
3. Allow the repair path to replace the corrupt Block from peer transfer.
4. If repair does not converge, stop related destructive exercises and escalate.
5. Do not rename, edit, or delete Block or index files by hand.

## Rollback or Escalation

There is no safe manual rollback by editing local files. Escalate to the storage
owner if peer repair cannot produce verified Block bytes or if quorum health is
compromised.

## Expected Outputs

- Affected Documents fail closed while Block Quarantine is active.
- Repair replaces corrupt local Block material with verified peer material.
- Evidence shows bounded error/status fields and no raw file paths.

## Evidence Collection

Record status, controlled fault command if used, evidence bundle path,
commit/ref, environment, expected and actual outcomes, and redaction result.

## Redaction Requirements

Do not paste local filesystem paths, Document names, Block object names,
credential values, unredacted log output, trace IDs, request IDs, or auth
claims.

## Authority Boundary

Block Quarantine is filesystem-level isolation for corrupt Block files. It is
not Content Quarantine, and it is not a manual Document lifecycle state.

## References

- `CONTEXT.md`
- `docs/adr/0002-dual-checksum-architecture.md`
- `docs/adr/0003-mirror-block-layout.md`
- `_bmad-output/implementation-artifacts/v2-release-evidence-matrix.md`
