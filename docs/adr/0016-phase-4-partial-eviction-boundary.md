# Phase 4 partial local eviction boundary

Status: Accepted

Date: 2026-05-31

## Context

Phase 3 added Backend upload, upload pressure, and evidence bundles. ADR 0012
requires throughput, mixed read/write/head, and upload-pressure evidence before
Phase 4 begins because Phase 4 removes some local Block copies and therefore
turns Backend restore into part of the read-availability story.

The V2 lifecycle in `CONTEXT.md` names Phase 4 as "Partial eviction" where
followers evict uploaded Blocks. Phase 5 is the separate future state where all
local copies may be evicted and reads become Backend-only.

External systems research on 2026-05-31 reinforced a narrow conclusion:
S.C.R.A.P. should not copy an S3-compatible gateway shape, but it should carry
forward fail-closed security boundaries, explicit backend retry policy,
deployment invariants, and operator visibility before eviction is implemented.
See `docs/research/2026-05-31-external-storage-systems.md`.

## Decision

Phase 4 is limited to partial local eviction of already-uploaded sealed Blocks.
It does not introduce cold-only reads, API deletion, S3 compatibility, tenant
quota authority, encryption, or multi-cell federation.

A Block is eligible for local eviction on a Member only when all of the following
are true:

- the Block is sealed;
- the `.blk` and `.idx` Backend objects were uploaded and verified;
- the matching `ConfirmUpload` command is committed in Raft;
- the Block is not quarantined and no repair is in progress;
- no local writer, scrubber, repairer, or reader holds the Block open;
- the Member can report the eviction decision through health/telemetry;
- a restore path from Backend to local Block files has already passed tests for
  the same object key format.

The first Phase 4 implementation evicts follower-local copies only. The current
leader keeps local copies for the normal hot read path. If leadership changes to
a Member that has evicted a requested Block, the new leader restores the Block
from the Backend before serving the read, or returns a typed transient
unavailability error. It must never return partial or least-bad bytes.

Eviction state is local member state, not metadata authority. Raft remains the
authority for Document visibility and physical refs. Pebble remains a derived
Projection. Removing a local `.blk` or `.idx` file does not alter Document
existence.

Eviction is not deletion. Retention, legal hold, and future cold-only lifecycle
rules are outside this ADR.

## Required Work Before Local Unlink

Implementation must land restore before unlink. The system must be able to fetch
the uploaded `.idx` and `.blk`, verify size and integrity, stage them safely, fsync
the files and directory entries, and atomically publish them before any read path
depends on an evicted copy.

The restore path must verify:

- Backend object keys match ADR 0009;
- `.idx` and `.blk` sizes match committed metadata;
- all touched Frame CRC-32C values verify;
- Document SHA-256 verifies before bytes are streamed;
- failed restore leaves either the old local files or no visible staged files;
- repeated restore attempts are idempotent.

The unlink path must be crash-safe:

- do not remove open Blocks;
- do not remove quarantined Blocks through the ordinary eviction path;
- remove `.idx` and `.blk` as one local lifecycle operation with observable
  failure state;
- leave Raft, Projection, and Upload Outbox state unchanged;
- on restart, distinguish "evicted locally" from "corrupt/missing unexpectedly".

## Observability and Operator Controls

Phase 4 must expose, at minimum:

- count and bytes of locally evicted Blocks by Shard and Member;
- restore attempts, failures, durations, and Backend error class;
- read failures caused by Backend unavailability for evicted Blocks;
- eviction skips by reason;
- health detail that separates upload pressure, eviction pressure, restore
  failure, and quarantine.

Dangerous operator commands for forced eviction, restore, and fault injection
belong on the Admin Service and must be unavailable in production unless the
target Cell explicitly enables the relevant non-production or evidence hooks.

## Consequences

Positive:

- Phase 4 can reduce follower disk usage without weakening the write ACK
  contract.
- Backend restore becomes testable before Phase 5 depends on Backend-only reads.
- The leader hot-read path remains simple while eviction behavior is introduced.

Negative:

- A newly elected leader may need Backend restore before it can serve older
  Documents whose local copy was evicted.
- Backend retry and admission behavior becomes more important because reads can
  depend on restored Blocks after local eviction.
- Operator and telemetry surfaces must grow before the storage code can safely
  remove local files.

## Alternatives Considered

### Evict any local copy after upload confirmation

Rejected for Phase 4. This collapses Phase 4 into Phase 5 by making every read
potentially Backend-only. It also removes the simple leader-local hot-read path
before restore behavior is proven.

### Keep eviction state in Raft

Rejected for the first Phase 4 boundary. Eviction is a per-Member cache/lifecycle
fact. Raft should not be polluted with local file-presence state unless a later
design needs cross-member placement accounting.

### Add an S3-compatible cold-read or redirect API

Rejected. S.C.R.A.P. is a gRPC Document gateway with all-or-error reads. Redirect
or S3-shaped reads would bypass checksum and authority semantics unless designed
as a separate future API.

### Implement encryption together with eviction

Rejected. Encryption is a long-lived envelope and key-management contract. It
should be decided in its own ADR and not coupled to the first local-eviction
mechanism.

## Success Criteria

Phase 4 is ready to implement when:

- ADR 0012 evidence remains current and passing;
- restore-from-Backend tests exist before eviction tests;
- read behavior for an evicted Block is all-or-error;
- operator health explains why a Block is present, evicted, restoring,
  quarantined, or unavailable;
- no API-visible Document identity or metadata semantics change.
