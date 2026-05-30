# Projection resolution boundary

Status: Accepted

Date: 2026-05-30

## Context

The Pebble **Projection** stores Transaction entries that point at Block IDs, but
Document metadata lives in each Block's `.idx` file. `HeadDocument`,
`ReadDocument`, and `FindDocuments` therefore need a read-side resolution step:
load the visible Transaction entry, walk its Block IDs, and decode matching
Document metadata from the Block index files.

Before this decision, that logic lived inline in `internal/shard`. The behavior
was duplicated across read paths, write duplicate checks, and Openlog recovery.
That made corruption handling inconsistent: client reads must fail closed when a
visible Projection entry cannot be resolved, while recovery and Raft replay must
tolerate the legitimate crash window where Pebble is durable but the current
Block `.idx` tail was not fsynced yet.

Moving the resolver into `internal/index` creates a deliberate package dependency
from `internal/index` to `internal/block`, because resolving a Projection entry
requires decoding Block index files.

## Decision

`internal/index` owns Projection Resolution through `Resolver`.

`Resolver` reads a Pebble Transaction entry from `Index`, opens each referenced
Block `.idx` file through `internal/block`, and returns Document metadata in
write order. Strict read methods (`ResolveDocument`, `ListDocuments`,
`ContainsDocument`) fail closed on visible metadata corruption, unreadable Block
indexes, missing transaction entries in a referenced Block, and `DocCount` drift.

Recovery and replay use an explicit lenient existence check
(`ContainsDocumentLenient`). It treats unreadable or missing-tail Block `.idx`
state as "not present" so Openlog recovery and idempotent Raft apply can proceed
through the projection-ahead-of-`.idx` crash window. It still returns errors for
Projection-level corruption, such as a nil Projection or an invalid transaction
entry.

`internal/shard` remains responsible for locking, leader/read gates, Block file
paths, and mapping resolver sentinels to Store errors.

## Consequences

- Projection Resolution semantics are centralized in one package instead of
  being reimplemented by Shard callers.
- `internal/index` now has an intentional dependency on `internal/block`; the
  package-boundary check accepts this direction.
- Client reads and write duplicate checks keep fail-closed behavior for visible
  corruption.
- Openlog recovery and Raft apply have a separate lenient path for recoverable
  projection-ahead-of-`.idx` states.
- No storage format, Raft wire format, or API change is introduced.
