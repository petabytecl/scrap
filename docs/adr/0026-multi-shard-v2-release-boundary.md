# Multi-Shard V2 release boundary

Status: Accepted

Date: 2026-06-10

## Context

`CONTEXT.md` defines a Shard as an independent Raft group managing a subset of
Transactions. Transactions are assigned to Shards through fixed hash slots.
One Cell contains multiple Members forming one or more Shard groups.

Current V2 implementation work has carried Shard identifiers through Block,
Backend, peer, Raft, telemetry, and operator contracts. ADR 0024 also added
explicit peer authorization by Shard scope and states that future multi-Shard
startup must derive the authorized Shard set from placement membership, not from
caller address.

The current `scrapd` composition still wires a single application Shard ID `0`.
That was acceptable for earlier implementation phases, but the clarified V2
release rule says V2 is not release-ready until all required V2 features are
complete. A release that still hardcodes one Shard would conflict with the
glossary, fixed-slot routing model, peer Shard-scope policy, and product
expectation that a Transaction is routed to an owning Shard.

## Decision

V2 release-ready status requires multi-Shard startup and routing. Single-Shard
composition remains acceptable for focused tests and development profiles, but
it is not the V2 production release contract.

V2 must add a Shard routing boundary that owns:

- the fixed hash-slot count;
- the Transaction-to-slot hash function;
- slot-to-Shard mapping;
- startup validation for full slot coverage;
- rejection of duplicate or overlapping slot ownership;
- lookup from `transaction_id` to owning Shard;
- low-cardinality routing telemetry; and
- route metadata suitable for admin and `scrapctl` diagnostics.

The routing boundary should live in a dedicated package such as
`internal/routing`. Public gRPC handlers must not hardcode Shard IDs and must
not embed route logic. They should call a Store-compatible router or narrow
interface that delegates to the owning Shard.

`internal/cmd` owns process composition. It must build the configured Shard set,
wire per-Shard telemetry and transports, and fail production startup when the
Shard map is invalid or incomplete.

`internal/peer` must receive the authorized Shard set from validated placement
membership. Shard-carrying peer RPCs must be denied before side effects when
the requested Shard is outside that set.

`internal/admin` and `scrapctl` must expose Shard-aware status without
conflating Cell, Member, Shard, or peer identity.

Backend object identity continues to include `shard_id` according to ADR 0009.
Upload Outbox, Confirmed Upload Catalog, eviction, restore, scrub, repair,
Content Scanner, and Content Quarantine remain Shard-local authority flows.

## Consequences

Positive:

- V2 release scope matches the glossary and fixed hash-slot model.
- Peer Shard-scope authorization from ADR 0024 becomes production-useful.
- Public API routing is explicit instead of hidden in one hardcoded Shard.
- Admin, telemetry, evidence, and `scrapctl` can report per-Shard health.

Negative:

- Startup configuration becomes stricter.
- Tests that assume Shard ID `0` must be isolated as single-Shard fixtures or
  updated to use the routing boundary.
- Evidence must prove cross-Shard isolation and wrong-Shard denial, not only
  single-Shard behavior.

Implementation guidance:

- Preserve `(transaction_id, document_name)` as Document identity.
- Do not add `tenant_id` to storage identity while implementing routing.
- Keep `FindDocuments` Transaction-scoped and routed to exactly one Shard.
- Do not infer Shard ownership from local files, Backend keys, hostnames, peer
  addresses, or certificate presence.
- Fail production startup closed when slot coverage, Shard membership, local
  Member assignment, or authorized peer Shard scope is invalid.
- Evidence must include at least two Shards, deterministic routing, wrong-Shard
  peer denial, per-Shard admin status, and Backend upload/restore behavior under
  non-zero Shard IDs.
