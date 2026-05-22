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

## Consequences

- Metadata evolution has a clear compatibility discipline across old readers,
  old writers, old data, and rolling upgrades.
- Published metadata can evolve without exposing internal consensus or repair
  implementation details.
- Operators can rebuild local projections from authoritative metadata instead
  of treating local indexes as sources of truth.
- The project accepts the overhead of maintaining private protobuf schemas and
  generated-code checks in addition to the public API schema.
