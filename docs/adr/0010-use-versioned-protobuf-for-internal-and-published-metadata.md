# Use versioned protobuf for internal and published metadata

Status: accepted

## Context

S.C.R.A.P. has several metadata surfaces with different owners and durability
expectations:

- authoritative shard metadata stored through Raft commands and replay;
- local projections such as Pebble indexes;
- block indexes and envelope references;
- published metadata snapshots and tails imported by read-only cells.

These records become long-lived compatibility contracts. They must survive
rolling upgrades, old stored data, future readers, and rebuild workflows.
Reusing public service API messages as storage records would couple client
traffic contracts to internal durability and recovery formats. Using ad hoc
JSON for canonical records would make type evolution, size, and compatibility
less disciplined.

## Decision

Use private, versioned Protocol Buffer messages for internal authoritative
metadata and for published metadata artifacts.

The internal metadata schema and the published metadata schema are separate
boundaries:

- internal metadata may include fields needed for Raft replay, physical refs,
  repair state, upload outboxes, restore state, encryption envelope refs, and
  shard-local workflow state;
- published metadata includes only the facts a read-only importing cell is
  allowed to consume, with source ownership and version information explicit;
- public gRPC request/response messages are not the canonical storage schema;
- local projections are rebuildable derived data, not independent metadata
  authorities.

Schema changes must be additive by default, versioned when behavior changes,
and covered by generated-code and compatibility checks before production use.
Writers use only the shard's active committed format until an explicit feature
gate changes it.

Compatibility guarantees are part of this decision:

- Fields may be added, but existing field numbers must not be reused,
  renumbered, or removed from durable protobuf messages.
- Field type or meaning changes are prohibited unless the owning schema or
  format version is incremented and old readers fail closed on the new required
  version.
- `schema_version` increments mean the reader must understand a new required
  invariant before it can safely accept the record. Purely additive fields do
  not increment `schema_version`.
- Durable storage changes require at least two release trains of deprecation
  notice before a field can be intentionally zeroed or ignored by writers.
- During rolling upgrades, readers accept every supported version from V1
  through current, while writers emit only the current committed version for
  the shard.
- Snapshot restore and DR import may re-encode accepted old metadata into the
  current version only after the old record has been fully validated.
- Any document written with schema version N must remain readable by software
  that supports schema version N+5 or later for the seven-year billing
  retention window. If a future reader cannot carry that compatibility directly,
  the release must include a tested migration/export path before writers emit
  the incompatible format.

Pebble key layouts are durable enough to need an explicit schema byte even
though local projections are rebuildable. Metastore Pebble keys use
`PebbleKeySchemaV1 = 0x01` followed by the logical key bytes. Future key-schema
versions must coexist with V1 during migration; deleting or rewriting V1 keys
is allowed only after authoritative metadata has been verified or rebuilt from
Raft/published sources.

## Consequences

- Metadata evolution has a clear compatibility discipline across old readers,
  old writers, old data, and rolling upgrades.
- Published metadata can evolve without exposing internal consensus or repair
  implementation details.
- Operators can rebuild local projections from authoritative metadata instead
  of treating local indexes as sources of truth.
- The project accepts the overhead of maintaining private protobuf schemas and
  generated-code checks in addition to the public API schema.
