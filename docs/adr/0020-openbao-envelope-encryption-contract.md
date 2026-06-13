# OpenBao envelope encryption contract

Status: Accepted

Date: 2026-06-02

## Context

OpenBao Transit is a locked V2 substrate in `CONTEXT.md`, but encryption has
been deferred through Phase 4. Phase 4 now allows local Block data to disappear
from some Members after Backend upload, and Phase 5 will consider cold-only
reads. That makes encryption metadata a long-lived storage compatibility
contract: encrypted bytes may live in the Backend for years, while key versions,
rewrap policy, and outage behavior change over time.

OpenBao Transit keeps cryptographic keys in OpenBao and lets applications store
ciphertext elsewhere. Transit ciphertext records key-version information,
rotating a key makes future encryptions use the new key version, and `rewrap`
upgrades ciphertext to a newer key version without returning plaintext to the
caller.

S.C.R.A.P. must preserve existing storage authority rules while adding
encryption. Raft remains the metadata authority. Pebble remains a derived
projection. The Backend is not in the write ACK path. Block, Frame, and
Projection Resolution semantics must remain fail-closed on corruption.

## Tracking

GitHub tracking issue: #400.

Implementation slices are published in
`docs/phase-4.5-security-implementation-slices.md`.

## Decision

Phase 4.5 introduces envelope encryption for new Document payload bytes. It does
not encrypt transaction IDs, Document names, sizes, Raft metadata, Pebble
Projection keys, `.idx` entries, audit events, or telemetry labels. Metadata
encryption and tenant-specific key policy are future decisions.

Each Document gets its own data encryption key. The Shard obtains the data key
from OpenBao Transit before writing encrypted payload bytes. The plaintext data
key may exist only in process memory for the active operation. S.C.R.A.P. stores
only the wrapped data key and envelope metadata needed to decrypt, verify, and
rewrap the Document later.

The encryption envelope is per Document and versioned. It records at least:

- envelope version;
- Transit mount and key name;
- Transit key version or wrapped-key version marker returned by Transit;
- wrapped data key ciphertext;
- payload algorithm;
- nonce or nonce-derivation metadata;
- plaintext Document SHA-256 already required by the API contract; and
- encrypted payload length.

Block Frame payloads contain ciphertext for encrypted Documents. Frame CRC-32C
covers the stored ciphertext bytes because CRC is a storage-corruption check.
After decrypting, reads verify the plaintext Document SHA-256 before returning
bytes to the client. A CRC failure or plaintext digest mismatch remains
`DATA_LOSS`.

The first payload algorithm is an AEAD from Go's standard cryptographic stack,
with unique nonces per Document key and Frame. The implementation must not use
Transit convergent encryption for Document payloads. S.C.R.A.P. does not need
deduplicating ciphertext, and deterministic ciphertext would weaken privacy for
common billing payloads.

Write ACK requires successful encryption. If Transit cannot provide a data key,
or the encryption envelope cannot be persisted durably with the Document
metadata, the write is not ACK'd. S.C.R.A.P. must never write plaintext Document
payload bytes to a Block as a fallback in production mode.

Read availability is fail-closed when key material is unavailable. Transit
outage, sealed Transit, auth failure, missing key, or an envelope that cannot be
unwrapped returns a typed crypto-unavailable error to clients. Admin health and
audit evidence may distinguish outage, authorization failure, missing key, and
minimum-decryption-version rejection; public client errors should not leak
operator secrets or Transit policy details.

Rewrap is a durable metadata lifecycle operation, not a rewrite of Block bytes.
Rewrap asks Transit to upgrade the wrapped data key or envelope ciphertext
without exposing plaintext to the operator. A successful rewrap is recorded
through Raft metadata so all Members converge on the new envelope. Rewrap is
idempotent for a Document whose envelope already targets the requested key
version. Rewrap audit evidence records principal, target key, old and new
version markers, result, and reason without logging plaintext, data keys, or
wrapped-key ciphertext.

OpenBao policy is least-privilege by operation:

- the write path needs data-key generation for the configured Transit key;
- the read path needs unwrap/decrypt capability for stored envelopes;
- the rewrap path needs rewrap capability and does not need plaintext export;
  and
- health checks need only enough capability to report dependency readiness
  without revealing key material.

Tests use a deterministic fake Transit boundary that supports data-key, unwrap,
rewrap, outage, auth-denied, missing-key, and minimum-version failure behavior.
The fake exists to test S.C.R.A.P. contracts; it is not production cryptography.

Phase 4.5 may hard-cut local development data. Existing unencrypted Blocks do
not need a transparent compatibility path unless a later migration issue
explicitly requires it. Evidence Cells for encrypted behavior should start from
fresh data or run an explicit migration workflow.

## Consequences

Positive:

- Backend-resident Blocks can be encrypted before Phase 5 depends on cold-only
  reads.
- Rewrap can rotate key metadata without rewriting Block payloads.
- Frame CRC, plaintext SHA-256, Raft authority, and Projection Resolution remain
  separate checks.
- Tests can exercise outage and missing-key behavior without a live OpenBao
  deployment.

Negative:

- Writes become dependent on Transit availability in production mode.
- Reads for encrypted Documents become dependent on Transit availability unless
  a later key-cache ADR is accepted.
- The `.idx` and Raft metadata continue to expose Document identity, size, and
  digest metadata.

Rejected alternatives:

- **Per-Block data keys**: rejected for the first implementation because reading
  one Document would expose key material for unrelated Documents in the same
  Block.
- **Transit encrypts every Frame directly**: rejected because it would make the
  write path pay a remote cryptographic call per Frame and couple Block I/O to
  Transit latency.
- **Plaintext fallback when Transit is down**: rejected because it would silently
  violate the production encryption contract.
- **Direct ciphertext streaming without local restore**: deferred to Phase 5
  because it changes read-path shape independently from encryption.
