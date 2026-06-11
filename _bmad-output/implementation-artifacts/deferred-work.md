## Deferred from: code review of 1-4-transaction-scoped-document-discovery (2026-06-11)

- Rebuilding Store errors still map to `INTERNAL` [internal/server/server.go:570]: `storeapi.ErrRebuilding` from Shard read gates is not handled by central server Store error mapping, so public read/discovery paths can report `INTERNAL` instead of a transient unavailable-style status during projection rebuild. Deferred as pre-existing because Story 1.4 did not introduce `mapStoreError` or the Shard rebuilding sentinel.

## Deferred from: code review of 2-2-multi-shard-cell-startup-composition (2026-06-11)

- Peer replication sink buffers full replicated Documents before dispatch [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and buffers the full peer replication body before calling the sink. Deferred as pre-existing peer transport hardening outside the Story 2.2 composition boundary.
- Peer replication sink wraps status-bearing sink errors as `INTERNAL` [internal/peer/server.go]: `replicateToSink` existed before Story 2.2 and maps sink errors through a generic internal status. Deferred as pre-existing peer transport error-mapping hardening outside the Story 2.2 composition boundary.
