# Mirror block layout across replicas

Status: Accepted

Date: 2026-05-26

Phase 1 has only a single local writer, but it still uses the long-lived Block,
Frame, and `.idx` format that Phase 2 replicas must mirror.

All replicas of a shard maintain identical block files: same block IDs, same document
offsets within each block. The leader assigns block IDs and offsets; peers write to the
exact same positions.

This means the physical refs in Raft metadata (block_id, offset, frame_count) are
universal across all replicas — any member can serve a read using the same physical
refs without per-member translation. Recovery is a byte-copy: a missing block on one
member can be fetched from any peer. Backend upload can be performed by any member
(blocks are identical). The Raft metadata command stays compact (~330 bytes) because
it records one set of physical refs, not one per replica.

## Considered Options

- **Independent layout** (each member packs documents into its own blocks independently):
  more flexible, but Raft metadata would need per-replica physical refs, reads would
  need per-member translation, and recovery could not use simple byte-copy. Rejected —
  the flexibility is not needed since the leader controls write ordering anyway.
- **Mirror layout** (chosen): leader controls block assignment, peers replicate the
  exact layout. Simpler metadata, simpler reads, simpler recovery.

## Consequences

The leader is the sole block assigner. Peers cannot independently repack or reorganize
their blocks. If a peer misses a document (lagging), it has a gap at that offset in
its block file, filled during repair by fetching the missing bytes from another peer.

## Phase 1 Format Contract

Block IDs are `uint64` rendered as fixed-width lowercase hex:
`000000000000002a.blk` and `000000000000002a.idx`.

Startup scans valid Block filenames, allocates `max(existing_block_id) + 1`, and never
fills gaps. Malformed `.blk` or `.idx` filenames fail startup. Restart always opens a
new Block; existing Blocks are treated as closed.

The Block header is 40 bytes, little-endian:

`magic(4) + version(2) + header_len(2) + shard_id(8) + block_id(8) +
created_at_unix_micro(8) + reserved(4) + header_crc32c(4)`

`header_crc32c` covers bytes 0-35. Readers validate the header CRC and the header
Block ID against the filename.

The Frame header is 32 bytes, little-endian:

`magic(2) + version(1) + flags(1) + header_len(2) + reserved(2) + doc_seq(4) +
frame_seq(4) + payload_len(4) + payload_crc32c(4) + reserved(4) +
header_crc32c(4)`

`header_crc32c` covers bytes 0-27. Readers validate header CRC, payload CRC, flags,
document sequence, frame sequence, expected frame count, and final SHA-256.

The `.idx` file has a CRC-protected `SIDX` header. Each entry is framed as
`entry_len + payload + entry_crc32c`. Entry payload includes version, reserved,
transaction ID, document name, content type, created_at, first frame offset,
frame count, total bytes, and raw SHA-256 digest.
