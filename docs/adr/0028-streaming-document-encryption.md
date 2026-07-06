# Streaming Document encryption and decryption

Status: Accepted

Date: 2026-07-05

## Context

`EncryptDocument` accumulated every sealed ciphertext Frame into a `[][]byte`
and `DecryptDocument` returned the whole plaintext as one `[]byte`. The shard
write path additionally joined all Frames into a second full ciphertext copy
for peer replication, and the encrypted read path buffered all stored Frames
plus the full plaintext before serving the first byte. With Documents up to
128 MiB, an authenticated client could hold multiples of the Document size in
memory per in-flight request, violating the bounded-memory expectation that
the plaintext read path already honors with its two-pass
`verifyPass`/`streamPass` design (GitHub #444, DW-2).

The envelope format was never the problem: payloads are independently sealed
64 KiB AES-256-GCM Frames with per-Frame nonces and identity+sequence AAD.
Only the Go API forced whole-Document materialization.

## Decision

Add streaming primitives to `internal/encryption` and rewire the shard paths
onto them; the storage format and envelope schema are unchanged.

- `DocumentEncryptor` seals one Frame at a time with one-Frame read-ahead so
  each produced Frame knows whether it is the last; `Finalize` yields the
  envelope and digests after the last Frame. `EncryptDocument` remains as a
  buffering wrapper for small payloads and tests.
- `DocumentDecryptor` holds one unwrapped data key and offers `Verify`
  (streaming decrypt-to-discard) and `Reader` (streaming plaintext reader)
  over a `FrameSource`. Every Frame is authenticated before any of its bytes
  are served; ciphertext length, plaintext length, and the whole-Document
  SHA-256 are re-checked before EOF. `DecryptDocument` remains as a
  buffering wrapper.
- `internal/block` gains `Writer.AppendDocumentFrameSource` (streams prepared
  Frames into the Block; the caller truncates to the starting offset on
  error) and `OpenDocumentFrameSource` (streams stored Frame payloads).
- The encrypted read path mirrors the plaintext two-pass contract: a
  streaming `Verify` pass over the stored Frames, then a second streaming
  decrypt pass serves the response. Both passes share one Transit unwrap.
  Integrity failures therefore still surface before the first byte.
- The shard write path streams ciphertext Frames directly into the Block
  writer while teeing them into a single buffer for peer replication — the
  one remaining whole-Document copy on that path, owned by the replication
  transport design (deferred separately as peer transport hardening).
- The peer replication receive path verifies replicated ciphertext with the
  streaming `Verify`, never materializing plaintext.
