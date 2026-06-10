# Durable rewrap Raft command

Status: Accepted

Date: 2026-06-09

## Context

ADR 0020 defines rewrap as a durable metadata lifecycle operation: the Shard asks
Transit to update a Document envelope and records the new envelope through Raft so
all Members converge without rewriting Block payload bytes.

That introduces a new Raft command and two concurrency hazards:

- overlapping operator rewrap requests for the same Document must not overwrite
  each other's proposal waiters or report success for the wrong requested key
  version;
- a rewrap of a sealed Block changes the retained `.idx`, so an older in-flight
  Backend upload confirmation must not clear the replacement Upload Outbox
  obligation.

Followers and replay must also apply the command deterministically. Pebble remains
a derived Projection, and Backend upload authority remains committed Raft state
plus the committed ConfirmUpload marker.

## Decision

`RewrapDocumentEnvelope` is an additive Raft command that carries the Document
identity, Block ID, replacement encryption envelope, old and new key versions,
rewrap timestamp, and a proposal ID.

The proposal ID exists only to correlate the leader's local waiter with the
committed command. It is not Document identity and does not affect replay. Old
commands without a proposal ID fall back to Transaction/Document waiter
correlation.

Apply validates the command before replacing the Block index entry:

- the current stored envelope must parse successfully;
- the current key version must match `old_key_version`;
- stale commands are no-ops for Raft apply and must not downgrade metadata;
- local apply errors that are not stale are returned to the Raft apply loop so a
  voter cannot silently miss a rewrap update.

For sealed historical Blocks, the leader must verify that its local `.blk` exists
before proposing the rewrap command when uploads are enabled. Followers apply the
committed command deterministically and may retain the replacement upload
obligation even if their local `.blk` has already been evicted. The upload worker
does not attempt Backend PUTs for pending uploads until the local `.blk` exists.
Phase 4.5 does not introduce index-only Backend updates for evicted Blocks.

`ConfirmUpload` also carries an additive `upload_generation` field. Initial sealed
uploads use generation `0` for replay compatibility. A rewrap requeue uses the
committed Raft log entry index as a non-zero generation in the Upload Outbox, and
upload confirmation must match the pending or already-confirmed generation before
it can clear the outbox or update the Confirmed Upload Catalog. Non-zero
generations are also included in Backend object keys so stale in-flight writers
cannot overwrite the replacement `.idx` object. Stale confirmations are ignored
and leave the newer upload obligation intact.

## Consequences

Positive:

- overlapping rewrap proposals are correlated by proposal ID instead of by
  Document identity alone;
- stale rewrap commands cannot downgrade a newer envelope;
- stale pre-rewrap upload confirmations cannot clear the replacement upload
  obligation;
- stale pre-rewrap upload writers cannot overwrite generation-scoped replacement
  Backend objects;
- replay remains compatible with old seal/confirm entries that have generation
  `0`.

Negative:

- rewrap proposal is rejected when the leader lacks the historical Block payload;
- `ConfirmUpload` and pending-upload Projection records carry another versioning
  field that must be preserved by rebuild and evidence tooling.
