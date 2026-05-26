# Dual checksum architecture: CRC-32C per frame, SHA-256 per document

Two checksum algorithms at two granularities:

- **Frame-level: CRC-32C** (4 bytes, embedded in each frame header in the block file).
  Purpose: fast disk corruption detection during reads. Hardware-accelerated via SSE4.2
  (Intel) and CRC instructions (ARM). Detects bit rot, bad sectors, silent corruption.

- **Document-level: SHA-256** (32 bytes, stored in the block index file and in the
  Raft metadata command). Purpose: cross-replica integrity verification and audit trail.
  NIST-standardized, universally recognized by compliance frameworks (SOC 2, ISO 27001,
  PCI DSS). With SHA-NI hardware acceleration, throughput is ~2-4 GiB/sec — not a
  bottleneck even for 128 MiB documents.

CRC-32C is not cryptographic and insufficient for tamper evidence, but frame-level
verification doesn't need tamper resistance — it catches disk-level corruption on the
hot read path. SHA-256 provides the tamper-evident guarantee at the document level,
where billing audit requirements demand it.

BLAKE3 was considered (3-4x faster than SHA-256, parallelizable) but rejected because
it is not yet NIST-standardized and may not be recognized by auditors. Documents are
retained for 7 years; the checksum algorithm must be stable and audit-accepted for
that entire period. SHA-256 with hardware acceleration is fast enough — the checksum
is never the throughput bottleneck.
