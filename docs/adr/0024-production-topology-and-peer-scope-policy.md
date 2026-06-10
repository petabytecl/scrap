# Production topology and peer scope policy

Status: Accepted

Date: 2026-06-10

## Context

The Phase 4.5 security review closed the immediate public API and deployment
control gaps, but left four production-readiness decisions for follow-up:

- whether `LeaderHint.leader_addr` should continue to expose a routable member
  address;
- whether peer authorization should include Shard scope before multi-Shard
  routing;
- whether mTLS should require TLS 1.3 instead of TLS 1.2; and
- whether certificate rotation requires hot reload for the initial production
  bridge.

Tracking issue: #433.

## Decision

`LeaderHint.leader_addr` remains the V2 client redirect contract. Non-leader
public RPCs may return the current leader address in a `LeaderHint` status
detail so the smart client can retry directly. This is topology disclosure, but
it is accepted only behind the production public surface: mTLS, authorization,
rate limits, and bounded errors. Replacing the address with an opaque route hint
or gateway token is a future wire-contract change that requires a separate ADR,
protobuf update, and redirect stress/e2e update.

Peer authorization now includes explicit Shard scope. A peer must still pass the
Phase 4.5 role, Cell, Member identity, and principal checks from ADR 0019. After
that, Shard-carrying peer RPCs must match the server's configured authorized
Shard set before reaching Raft routing, byte replication sinks, or Block
transfer handlers. `ReplicateDocumentInit` carries `shard_id` so byte
replication is covered by the same Shard policy as `ForwardRaft` and
`TransferBlock`. The current `scrapd` application wires the single V2 Shard ID
`0`; future multi-Shard startup must derive the authorized Shard set from
placement membership, not caller address.

Production mTLS requires TLS 1.3 on both server and client configurations built
by the shared SCRAP TLS builders. TLS 1.2 compatibility is not part of the
production security bridge. Any later compatibility exception needs an explicit
deployment-scoped ADR and evidence that the affected surface remains bounded.

Certificate rotation remains restart-based for this phase. Operators rotate
certificate and CA files by updating the mounted material, restarting or rolling
the affected `scrapd` Members, and relying on startup gates to fail closed if
the new material is missing, expired, not trusted, or not valid for the expected
surface identity. Hot certificate reload is deferred until there is a proven
operational need and a design for connection draining and rollback evidence.
