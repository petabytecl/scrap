# Lean Pebble projection with metadata tiering

Pebble stores one versioned entry per Transaction (not per Document), mapping
`transaction_id` to `{block_ids, doc_count, completed}`.
A Transaction may span multiple Blocks when a seal triggers between Documents,
so `block_ids` is a list. Per-Document metadata resolution uses the Block's `.idx`
file on disk. Pebble entries are evicted after a configurable hot window (default
6 months); cold reads resolve via monthly catalog archives in the Backend.

At 50M transactions/day with 7-year retention, a per-Document Pebble schema would
accumulate ~34 billion entries per shard (~10 TiB), with 15-30x LSM write
amplification rewriting data that is never updated. This is the metadata
amplification problem described in the Facebook Haystack paper: per-file metadata
in a database kills storage systems at scale.

The lean model (per-Transaction keys + hot window) caps Pebble at ~60 GiB per shard
regardless of retention period. This fits in Pebble's block cache, keeps the LSM
to 3-4 levels with ~15x write amplification, and reduces bloom filter memory from
~2.4 GiB to ~60 MiB.

The cost: HeadDocument adds ~50-100 µs for `.idx` file lookup (page-cached for
recent blocks). This is within the single-digit-millisecond p95 target and
invisible on the ReadDocument path where block I/O (~1-15 ms) dominates.

## Considered Options

- **Per-Document keys**: one Pebble entry per Document. Simplest read path, but
  10 TiB+ Pebble at scale, 6-7 LSM levels, 30x write amplification, multi-GiB
  bloom filters. Rejected — does not scale to the workload.
- **Per-Transaction keys with full metadata**: all Document metadata in the
  Transaction value. Reduces entries 4x but values are ~500 bytes, so Pebble is
  still ~270 GiB. Partial improvement. Rejected — still too large.
- **Per-Transaction keys with lean values + .idx resolution** (chosen): smallest
  possible Pebble footprint. `.idx` files already exist in the block format and
  are self-describing. Hot window keeps Pebble bounded. Best at scale.

## Consequences

- HeadDocument requires reading the `.idx` file (not just Pebble). This is fast
  for recent blocks (page cache) but slower for cold blocks.
- The `.idx` file format becomes load-bearing for reads, not just for block
  management. It must be versioned and stable.
- In Phase 1, Pebble is the local visibility authority. If Block or `.idx` bytes
  exist without a committed Pebble entry, the Document is invisible.
- In Phase 2+, Raft metadata is the authority and Pebble returns to being a
  rebuildable projection.
- HeadDocument and FindDocuments fail closed on visible `.idx` header, entry, CRC,
  or metadata corruption.
- FindDocuments returns write order: ascending Block ID, then append order within
  each `.idx`.
- Metadata tiering adds an eviction process: after the hot window, a background
  job deletes Pebble entries for Transactions whose Blocks are confirmed uploaded
  to the Backend.
- Pebble's `Comparer.Split` must be configured for the `transaction_id` key
  to enable bloom filters.
