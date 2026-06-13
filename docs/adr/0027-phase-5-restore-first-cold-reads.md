# Phase 5 restore-first cold reads

Status: Accepted

Date: 2026-06-10

## Context

ADR 0016 limited Phase 4 to partial local eviction. Followers may evict
uploaded Block data files, while the serving leader keeps local `.blk` copies
for the normal hot read path. When a Member needs an intentionally evicted
Block in Phase 4, it restores the full `.blk` from the Backend before using the
normal local Block reader.

ADR 0016 and ADR 0020 both deferred direct Backend ciphertext streaming to
Phase 5. The V2 master PRD identified Phase 5 cold-read shape as a blocking
decision: restore-first cold reads, direct Backend ciphertext streaming, or
both.

S.C.R.A.P. must preserve these invariants:

- `ReadDocument` is all-or-error;
- Backend inventory is not a consistency oracle;
- Backend upload is not in the write ACK path;
- Raft remains metadata authority;
- Pebble Projection remains derived;
- Frame CRC verifies stored bytes;
- Document SHA-256 verifies plaintext before return; and
- encrypted Backend-resident Blocks remain opaque ciphertext.

Direct streaming from Backend to client would create a second read
implementation with separate cancellation, buffering, checksum, encryption,
telemetry, and failure semantics. It would also increase the risk that Backend
access becomes an implicit read oracle.

## Decision

V2 Phase 5 cold reads use restore-first full-Block restore. Direct Backend
ciphertext streaming, range streaming from Backend, and per-Frame remote reads
are not part of V2 release scope.

Phase 5 extends eviction so every local `.blk` copy of an uploaded sealed Block
may be intentionally evicted when policy allows. A later `ReadDocument` restores
the full Block data file to local storage before serving bytes through the
normal local Block reader.

Restore-first cold reads must follow the same authority model as Phase 4:

- restore is allowed only from committed Confirmed Upload Catalog metadata;
- Backend list or inventory output is not authority;
- the restored `.blk` is staged safely before publication;
- staged bytes are verified against committed Backend metadata and retained
  local `.idx` metadata;
- the Block header, Frame CRCs, and Document SHA-256 are verified before bytes
  are returned;
- failed restore does not publish partial bytes;
- successful restore atomically publishes a local `.blk`; and
- local lifecycle metadata records restore evidence without changing Document
  visibility.

Restore remains a per-Block singleflight operation owned by the Shard. The first
read that needs a cold Block starts restore; concurrent reads for the same Block
wait on that restore instead of issuing duplicate Backend downloads. Client
cancellation stops that client from waiting, but does not automatically cancel a
restore that may satisfy later reads.

Metadata-only reads do not restore Block bytes. `HeadDocument` and
`FindDocuments` continue to use retained local `.idx` data and Projection
Resolution.

Encryption behavior remains unchanged:

- Backend stores ciphertext Blocks;
- restore downloads ciphertext Blocks;
- Frame CRC verifies ciphertext storage integrity;
- normal read decrypts through the envelope path; and
- plaintext SHA-256 verifies before return.

## Consequences

Positive:

- V2 keeps one read implementation after local Block bytes are present.
- All-or-error read behavior and existing Block/Frame verification are
  preserved.
- Backend access stays behind committed metadata and explicit restore workflow.
- Direct Backend streaming remains available for future research without
  blocking V2.

Negative:

- First read of a cold Block pays full-Block restore latency.
- Local disk needs enough runway for restored Blocks and restore staging.
- Large Blocks may restore more bytes than a single requested Document needs.
- Future direct streaming, if needed, will require a new ADR and evidence gates.

Implementation guidance:

- Phase 5 may evict the current leader's local `.blk` copy only after restore
  behavior, disk runway, leadership behavior, and evidence gates are in place.
- Production startup and health must expose whether cold-read restore policy is
  configured safely.
- Restore concurrency, timeout, retry, staging, and disk budget settings must be
  bounded and observable.
- Backend transient dependency failure maps to `UNAVAILABLE`.
- Missing or corrupt confirmed Backend objects map to `DATA_LOSS`.
- Restore must not use Backend list or object existence as a consistency oracle.
- Evidence must prove all-local-copy eviction, restore-on-read, concurrent read
  singleflight, Backend transient failure, Backend missing/corrupt failure,
  encryption interaction, and no raw identifier or Backend key leaks.
