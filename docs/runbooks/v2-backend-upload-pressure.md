# V2 Backend Upload Pressure Runbook

## Purpose

Use this runbook when sealed Blocks cannot upload fast enough, write admission
is rejecting new work, or pending upload bytes approach the configured budget.

## Owning Feature Epic or Release Gate

Epic 3 owns Backend upload, Upload Outbox, upload pressure, and Confirmed
Upload Catalog behavior. FR-6 and FR-16 cover evidence requirements.

## Symptoms

- Writes fail with upload-pressure or capacity-related responses.
- `scrapctl upload-pressure` reports pressure above the configured threshold.
- Backend dependency errors appear in status or evidence.

## Normal Path

```sh
scrapctl upload-pressure --admin-url <admin-url> --output=json
scrapctl status --admin-url <admin-url> --output=json
scrapctl evidence bundle pressure --admin-url <admin-url> \
  --bundle-dir evidence/runbooks/upload-pressure
```

If running a controlled rehearsal, use the implemented fault surface:

```sh
scrapctl fault backend break --context <kube-context> \
  --environment prodlike --confirm <cell-id>
scrapctl fault backend restore --context <kube-context> \
  --environment prodlike --confirm <cell-id>
```

## Failure Path

1. Confirm pressure status and Shard health before changing anything.
2. Check whether Backend connectivity is degraded or upload workers are stalled.
3. Stop new fault injection or load generation.
4. Restore Backend availability through the owning infrastructure path.
5. Watch pressure return below the admission threshold.

## Rollback or Escalation

Rollback recent deployment/config changes that affected Backend connectivity or
upload budget. Escalate to the Backend owner if the dependency cannot accept
uploads or confirms.

## Expected Outputs

- `scrapctl upload-pressure` reports bounded Shard pressure fields.
- `scrapctl status` reports Shard and Backend health without exposing raw
  dependency output.
- Evidence bundle creation exits successfully or records a clear gate failure.

## Evidence Collection

Record pressure command, status command, any controlled fault command, evidence
bundle path, commit/ref, environment, expected and actual outcomes, and redaction
proof.

## Redaction Requirements

Do not paste credential values, Backend object names, Document payloads,
unredacted log output, trace IDs, request IDs, auth claims, or local runtime
paths.

## Authority Boundary

Upload pressure is admission evidence. Do not use Backend object listings,
Local Block Lifecycle, or telemetry as the source of truth for committed upload
authority.

## References

- `CONTEXT.md`
- `docs/adr/0010-upload-outbox-via-raft.md`
- `_bmad-output/implementation-artifacts/epic-3-backend-durability-cold-read-closure-evidence.md`
- `internal/scrapctl/status.go`
- `internal/scrapctl/fault.go`
