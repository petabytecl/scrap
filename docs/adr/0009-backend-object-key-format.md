# Backend object key format

Status: Accepted

Date: 2026-05-27

## Context

Sealed Blocks must be uploaded to a cloud object store (S3, GCS, Azure Blob) for
cold durability. The object key format is a 7-year contract — renaming keys after
billions of objects exist is not practical. The key must be globally unique within
a cell's bucket, self-describing (shard + block identity recoverable from the key
alone), lexicographically ordered for efficient prefix listing, and stable across
leader changes (same block = same key regardless of who uploads).

Each sealed Block produces two Backend objects: the `.blk` (frame data, ~64 MiB)
and the `.idx` (document index, ~tens of KiB). They are uploaded separately because
cold reads need the `.idx` first (to resolve document offsets) before issuing a
ranged GET on the `.blk`. Bundling them would force downloading ~64 MiB to read a
200-byte index entry. The metadata tiering described in ADR 0004 also needs `.idx`
data independently — catalog archives are built from `.idx` entries.

## Decision

Flat prefix with cell, shard, and block identity:

```
{cell_id}/shards/{shard_id}/{block_id}.blk
{cell_id}/shards/{shard_id}/{block_id}.idx
```

`block_id` uses the existing fixed-width lowercase hex format (`000000000000002a`),
so lexicographic order = creation order within a shard. `cell_id` in the prefix
supports future multi-cell deployments sharing a bucket. `shard_id` is the numeric
shard identifier, also fixed-width hex.

## Considered Options

- **Date-partitioned** (`{cell}/shards/{shard}/2026/05/27/{block_id}.ext`):
  enables date-range listing but block sealing does not align with calendar
  boundaries. Complicates listing for recovery, catalog building, and shard-scoped
  operations. Rejected — the natural access pattern is shard-scoped, not
  date-scoped.
- **Content-addressed** (SHA-256-derived key): built-in deduplication but loses
  ordering, makes prefix listing and recovery harder, and blocks are not duplicated
  across shards anyway. Rejected.
- **Flat prefix** (chosen): simplest, preserves ordering, shard-scoped listing is
  a single prefix scan, recovery can enumerate a shard's backend state with one
  `ListObjects` call.
