# Bytes separate from Raft

Status: Accepted

Date: 2026-05-26

This ADR governs Phase 2+ replicated storage. Phase 1 is a single-node spike-store:
it uses the same API and binary storage contracts, but has no Raft, peer fan-out, or
quorum ACK path.

Document bytes flow peer-to-peer (leader fan-out to all followers via a separate gRPC
peer service), not through Raft log entries. Raft carries only metadata commands
(~330 bytes per document: identity, physical refs, whole-document SHA-256, content
metadata, idempotency key).

At p50 document size (64 KiB) and 1,000 writes/sec, putting bytes in Raft would cost
~64 MiB/sec of Raft log bandwidth per voter (320 MiB/sec total with 5 voters).
Metadata-only Raft costs ~330 KB/sec — a 190x reduction. Beyond bandwidth, bytes-in-Raft
forces whole-document heap buffering on every voter during Raft processing, violating the
spike-validated design principle that the write path works without heap-buffering documents.

Recovery is also affected: a new or lagging member replaying a byte-heavy Raft log would
need to re-ingest gigabytes of document data through the Raft state machine. With the
separated model, Raft replay is fast (metadata only) and block files transfer in parallel
via the peer service.

## Considered Options

- **Bytes in Raft entries**: single replication path (simpler), but 190x bandwidth cost,
  heap buffering on all voters, slow recovery. Rejected.
- **Chain replication for bytes** (leader → peer1 → peer2): halves leader outbound
  bandwidth, but adds latency (sequential), failure-mode complexity, and only matters
  at 7+ voters. Rejected for S.C.R.A.P.'s 3-5 voter target.
- **Leader fan-out for bytes** (chosen): leader sends to all N-1 peers in parallel.
  Simple, lowest latency, bandwidth is not a bottleneck at 3-5 voters with the workload's
  document sizes.
