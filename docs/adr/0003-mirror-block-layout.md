# Mirror block layout across replicas

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
