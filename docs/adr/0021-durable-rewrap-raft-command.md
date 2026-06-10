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

For sealed historical Blocks, rewrap must be able to requeue Backend upload before
mutating the retained `.idx`. If uploads are enabled and the local `.blk` is
missing, the command is rejected before the index is rewritten. Phase 4.5 does not
introduce index-only Backend updates for evicted Blocks.

`ConfirmUpload` also carries an additive `upload_generation` field. Initial sealed
uploads use generation `0` for replay compatibility. A rewrap requeue uses a fresh
non-zero generation in the Upload Outbox, and upload confirmation must match the
pending or already-confirmed generation before it can clear the outbox or update
the Confirmed Upload Catalog. Stale confirmations are ignored and leave the newer
upload obligation intact.

## Consequences

Positive:

- overlapping rewrap proposals are correlated by proposal ID instead of by
  Document identity alone;
- stale rewrap commands cannot downgrade a newer envelope;
- stale pre-rewrap upload confirmations cannot clear the replacement upload
  obligation;
- replay remains compatible with old seal/confirm entries that have generation
  `0`.

Negative:

- rewrap of an already-evicted Block is rejected until a future index-only Backend
  metadata update or restore-first workflow is designed;
- `ConfirmUpload` and pending-upload Projection records carry another versioning
  field that must be preserved by rebuild and evidence tooling.
