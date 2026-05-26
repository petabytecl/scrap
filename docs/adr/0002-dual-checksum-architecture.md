# Dual checksum architecture: CRC-32C per frame, SHA-256 per document

Two checksum algorithms at two granularities:

- **Frame-level: CRC-32C** (embedded in each frame header in the block file).
  Purpose: fast disk corruption detection during reads. Hardware-accelerated via SSE4.2
  (Intel) and CRC instructions (ARM). Detects bit rot, bad sectors, silent corruption.

- **Document-level: SHA-256** (32 raw bytes, stored in the block index file and in the
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

## Phase 1 Contract Update

The Phase 1 Frame header is 32 bytes and carries both `payload_crc32c` and
`header_crc32c`. The header CRC uses CRC-32C Castagnoli and covers bytes 0-27 of the
Frame header. Payload CRC covers the Frame payload only.

The Block header also carries `header_crc32c`, covering bytes 0-35 of the 40-byte
Block header.

The API renders SHA-256 as lowercase 64-character hex. Internal Store, Block, Index,
and future Raft metadata code stores SHA-256 as raw 32-byte digests.

`ReadDocument` is all-or-error. The server verifies Frame header CRCs, payload CRCs,
Frame ordering, expected Frame count, and final Document SHA-256 before sending any
metadata or chunk response. Corruption maps to `DATA_LOSS` with zero streamed
messages.
