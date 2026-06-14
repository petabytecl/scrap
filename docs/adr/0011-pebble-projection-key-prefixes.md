# Pebble projection key prefixes

Status: Accepted

Date: 2026-05-27

## Context

The Pebble projection stores two kinds of state: transaction entries (document →
block lookup) and upload outbox entries (pending upload tracking). Upload outbox
keys use a `\x00upload\x00` binary prefix (ADR-0010). Transaction keys are stored
unprefixed — the raw transaction ID string is used directly as the Pebble key.

Transaction IDs are user-provided UTF-8 strings. A crafted transaction ID starting
with `\x00upload\x00` would collide with the upload outbox keyspace, corrupting
upload scans. Since transaction IDs are not constrained to any specific format
(they are not ULIDs or any other generated format), this collision is a real attack
vector, not a theoretical concern.

Additionally, the upload outbox iterator (`PendingUploads`) creates an unscoped
Pebble iterator and scans every key in the database, filtering by prefix in
application code. This is O(all keys) instead of O(upload keys), which degrades
as the projection grows.

## Decision

All Pebble key families use explicit binary prefixes:

| Key family      | Prefix           | Description                        |
| --------------- | ---------------- | ---------------------------------- |
| Transaction     | `\x00tx\x00`     | Document → block lookup entries    |
| Upload outbox   | `\x00upload\x00` | Pending upload tracking (existing) |

The upload outbox iterator uses `pebble.IterOptions` with `LowerBound` and
`UpperBound` set to the upload prefix range, so the iterator only visits SST
blocks that overlap the upload keyspace.

SCRAP is pre-release. Existing Pebble databases (dev/test only) require a clean
rebuild after this change — no migration path is provided.

## Consequences

- Transaction IDs can never collide with internal key families regardless of
  content.
- Upload outbox scans are O(pending uploads) instead of O(all keys).
- The `refreshUploadPressureLocked` call during Raft apply (which scans pending
  uploads) becomes sub-millisecond for typical backlogs.
- Future key families (e.g., compaction state, shard metadata) follow the same
  `\x00<family>\x00` convention.
- Any existing dev/test Pebble directories must be deleted and rebuilt.
