# Fail closed on read corruption

Status: accepted

## Context

S.C.R.A.P. stores immutable documents in block ranges and may serve bytes from
local hot storage, peer replicas, restored backend blocks, or read-only cell
caches. Bit rot, partial writes, bad disks, stale replicas, and corrupt backend
objects are expected failure modes.

The service fleet should not receive corrupt document bytes as if they were
valid. Returning a clean prefix and failing later would be simpler for large
streaming reads, but it would weaken the API into a partial-success contract
and push integrity ambiguity into every caller.

The expected workload makes verification-before-streaming acceptable for v1:
most documents are below roughly 200 KiB, while 128 MiB documents are edge
cases.

## Decision

`ReadDocument` fails closed on detected corruption.

Before streaming response bytes, the server verifies all frame checksums touched
by the requested range against authoritative metadata. If a source is corrupt
or unavailable, the server may retry another verified source. If no verified
source can satisfy the requested range, the server returns a typed failure and
does not stream document bytes for that response.

Corrupt byte sources are quarantined from serving, repair is scheduled from
verified sources when possible, and integrity incidents are reported when no
valid source remains.

## Consequences

- Callers get an all-or-error integrity contract instead of partial-byte
  ambiguity.
- First-byte latency for full-document reads includes verification of all
  touched frames.
- Ranged reads can still succeed when their touched frames are clean, even if a
  different frame in the same document is corrupt.
- Repair and observability must be first-class production workflows, not
  best-effort logging.
- Changing this behavior later would require a product/API decision because it
  would alter caller-visible semantics.
