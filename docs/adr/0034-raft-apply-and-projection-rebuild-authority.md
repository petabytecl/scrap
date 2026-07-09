# Raft apply and Projection rebuild authority

Status: Accepted

Date: 2026-07-09

## Context

Findings `H-03`, `H-05`, and `M-11` show that apply can advance past failed or
unknown commands, and Projection rebuild can trust non-authoritative local
`.idx` enumeration and crash-unsafe directory swaps.

Raft is metadata authority. The Pebble Projection is derived and rebuildable.
Those invariants fail if apply treats infrastructure errors as success or if
rebuild certifies visibility from local files alone.

## Decision

1. **Apply fail-closed.** Infrastructure and capacity failures from
   `applyEntryCommand` propagate to the Raft Ready loop and must not advance the
   durable applied watermark. Unknown or unsupported command variants are
   rejected as apply failures, not silent success.

2. **Transaction cardinality preflight.** Transaction Document cardinality is
   checked before mutating `.idx` or Projection state so capacity limits cannot
   leave half-applied physical state.

3. **Rebuild from Raft/quorum authority.** Projection rebuild derives visibility
   from Raft log/snapshot replay plus verified Block bytes. Local `.idx` files
   validate physical references; they are not the source of Document visibility.
   Missing required Content Quarantine or scanner state fails certification
   rather than copying from a divergent Projection.

4. **Crash-durable swaps.** Parent-directory renames and removals during
   Projection swap are fsynced. Projection Resolution validates the complete
   Transaction before returning one Document.
