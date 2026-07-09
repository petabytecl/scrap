# Backend durability: seal intent, verified upload, and leadership fence

Status: Accepted

Date: 2026-07-09

## Context

Findings `H-07` and `H-08` show sealed Blocks can permanently leave the Upload
Outbox, and Backend confirmation can certify corrupt or stale bytes when upload
checks only size/ETag, workers are not term-fenced, and generation-zero keys are
mutable.

ADR 0010 remains the Upload Outbox Raft command shape. This ADR tightens the
durability and confirmation contract around those commands.

## Decision

1. **Fsynced seal intent.** Before Block rotation closes the old Block, the
   leader persists a fsynced seal intent. Startup and new leadership reconcile
   every closed Block into the Upload Outbox. Shutdown seals the tail Block or
   records durable intent for the next leader.

2. **Pre-upload integrity verification.** Upload verifies exact source Block and
   index integrity (including Document-level checks required by the read path)
   before PUT. Size/ETag alone is insufficient for ConfirmUpload.

3. **Leadership-fenced workers.** Upload workers cancel or recheck against the
   current leadership epoch. A former leader must not ConfirmUpload after losing
   leadership.

4. **Immutable/conditional generations.** Backend object generations use
   conditional/immutable writes so a stale leader cannot overwrite a newer
   generation. Peer repair requeues Backend replacement when local bytes change.
