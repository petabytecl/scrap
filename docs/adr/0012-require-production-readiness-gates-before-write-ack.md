# Require production readiness gates before production write ACKs

Status: accepted

## Context

S.C.R.A.P. acknowledges writes before backend upload. That makes the immediate
ACK boundary a durability promise: acknowledged bytes must remain readable or
repairable through local and peer durability, authoritative metadata, and later
backend workflows.

The write-path spike produced useful evidence, and some pre-production API and
server scaffolding exists. Neither is enough to expose production write
semantics. A service can pass happy-path tests while still losing data through
crashes, stale leadership, unsafe placement, metadata incompatibility, backend
lag, key loss, or operator error.

## Decision

Production write ACK mode is disabled until release gates prove the durability,
consistency, compatibility, capacity, security, and operator contracts for the
target deployment profile.

The minimum gates are:

- quorum-applied authoritative metadata with durable Raft restart, snapshots,
  stale-leader fencing, and ReadIndex behavior tested;
- local and required peer byte durability before metadata visibility;
- crash/recovery tests across block bytes, prepare logs, metadata logs, local
  projections, backend jobs, and restore;
- versioned metadata, block, index, envelope, and published metadata
  compatibility checks;
- checksum-verified full and ranged reads that fail closed on corruption;
- explicit backend capacity profiles, disk runway guard bands, and admission
  control;
- OpenBao key retention, envelope restore, and crypto-unavailable behavior
  tested;
- generated-code, race, fuzz or property, security, and representative soak
  checks in CI or as named release evidence;
- operator runbooks for repair, scrub, drain, lost disk, lost member, backend
  outage, OpenBao outage, and DR rebuild.

Non-production single-member or development modes may exist, but they must be
named as non-production and must not silently claim the production ACK contract.

The executable production write ACK gate is `SCRAP_PRODUCTION_WRITE_ACK_READINESS`.
It must fail closed until the metadata compatibility boundary
`SCRAP_METADATA_COMPATIBILITY_BOUNDARY_V1` and later Raft, peer durability,
backend restore, OpenBao envelope, capacity admission, operator-readiness, and
implementation gates all have explicit release evidence. Passing the metadata
compatibility boundary alone is not enough to enable production write ACK mode.

## Consequences

- The project can keep building vertical slices without accidentally promoting
  scaffolding into a production storage guarantee.
- Release readiness becomes evidence-based and deployment-profile-specific.
- Some useful code may remain behind disabled production modes until the
  corresponding gates are satisfied.
- Operators and product owners must sign off on readiness evidence before
  production traffic is accepted.
