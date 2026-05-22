# Storage Gateway Package Architecture

Status: planning gate for GitHub issue `#16`
Last updated: 2026-05-22

This guide defines the intended production package ownership and dependency
rules for S.C.R.A.P. storage-gateway code. It turns the package map from the
design notes into rules that can be used in code review and later enforced by
import checks.

The goal is to keep durable storage policy independent from transport,
frameworks, backend vendors, CLIs, process wiring, and generated API messages.
Production implementation should add or move packages toward these boundaries
instead of treating the current pre-production scaffolding as the final shape.

## Layer Model

Dependencies point downward in this table. Lower layers must not import higher
layers.

| Layer | Packages | Responsibility |
| --- | --- | --- |
| Commands and composition | `cmd/scrapd`, future `cmd/scrapctl`, `internal/node` | Process startup, config loading, dependency wiring, listeners, shutdown, and CLI entrypoints. |
| Transport adapters | `internal/api`, future `internal/admin` | gRPC service handlers, request validation, authorization calls, protobuf mapping, streaming mechanics, and gRPC status mapping. |
| Application workflow | future `internal/shard`, `internal/operations`, `internal/placement`, `internal/replication`, `internal/backendupload` | Storage use cases: write lifecycle, visibility, placement, peer prepare, upload intent, restore, repair, scrub, and admin operation orchestration. |
| Storage policy and durable contracts | `internal/metastore`, `internal/raftmeta`, `internal/storageformat`, `internal/published`, `internal/identity` | Authoritative metadata records, command encoding, schema compatibility, published metadata contracts, identities, invariants, and deterministic storage formats. |
| Mechanism adapters | `internal/blockstore`, `internal/backend`, `internal/backend/*`, future `internal/cryptoenv`, future `internal/authz`, future `internal/observe`, `internal/config` | Filesystem block IO, backend provider adapters, OpenBao envelope boundary, capability policy loading, observability helpers, and typed config. |
| Generated contracts | `internal/gen/scrap/...`, `proto/scrap/...` | Generated Go protobuf/gRPC code and source API/schema contracts. Generated code is a boundary format, not the storage-core domain model. |
| Spike code | `internal/spike/...`, `cmd/scrap-spike` | Disposable evidence and experiments. Spike package shape does not define production architecture. |

`internal/node` is the composition root for the daemon. It may import concrete
adapters and workflow packages to assemble the process. Lower layers must not
import `internal/node`.

Future `cmd/scrapctl` is a planned client command. When added, it must import
generated admin clients and ordinary CLI support only. It must not import server
internals such as
`internal/node`, shard implementation packages, block stores, metadata stores,
or backend adapters.

## Package Ownership

| Package or future package | Owner boundary |
| --- | --- |
| `cmd/scrapd` | Minimal daemon entrypoint. Parse flags/env enough to call `internal/node`; no storage decisions. |
| future `cmd/scrapctl` | Operator CLI over the admin gRPC API. No separate control path and no server-internal imports. |
| `internal/node` | Process lifecycle, dependency wiring, server listeners, shard registry, node-level worker pools, shutdown, and health/readiness aggregation. |
| `internal/shard` | One authoritative Raft metadata group: write lifecycle, command proposal, visibility, idempotency, hot index ownership, restore/repair state coordination. This package does not exist yet; current local-only orchestration is transitional. |
| `internal/localstorage` | Transitional non-production local application used by current server tests and scaffolding. New production work should move toward the `node`/`shard`/store boundaries described here instead of expanding this package as the final architecture. |
| `internal/blockstore` | Local block, index, frame, and openlog file IO; checksum verification; crash-recovery boundaries for local bytes. It does not own document visibility. |
| `internal/metastore` | Authoritative metadata storage model, durable record validation, codec boundaries, and projection/rebuild support. |
| `internal/raftmeta` | Raft-backed metadata authority, log/snapshot plumbing, committed command application, and replay into metadata state. |
| future `internal/raftstore` | Persistent Raft log/storage glue if it needs to split from `internal/raftmeta`; no public API or gRPC dependency. |
| `internal/storageformat` | Long-lived block/index/envelope storage-format codecs and compatibility fixtures. |
| `internal/published` | Published metadata snapshot/tail/checkpoint contracts for read-only cell imports. |
| `internal/backend` | Provider-neutral backend interfaces, normalized backend error classes, and shared capacity concepts. |
| `internal/backend/fs` | Filesystem backend adapter for non-production, test, and explicit filesystem deployments. |
| future `internal/backend/s3`, `internal/backend/gcs`, `internal/backend/azure` | Provider-specific object backend adapters hidden behind `internal/backend` interfaces. |
| `internal/backendupload` | Durable upload job execution and verification of backend block/index/envelope object sets. |
| future `internal/cryptoenv` | Envelope encryption policy, AAD, DEK cache, OpenBao Transit adapter boundary, key-version behavior, rewrap support, and crypto-unavailable classification. |
| `internal/api` | Public and admin gRPC handlers in current code, request validation, stream handling, protobuf translation, and gRPC status/error detail mapping. If it grows further, split admin workflow handlers into `internal/admin` while keeping transport mapping at the edge. |
| future `internal/admin` | Admin operation use cases and durable operation orchestration. It should expose plain internal request/result structs to transport handlers rather than generated request/response protobufs. |
| future `internal/authz` | Workload-identity capability policy, allowed/denied decisions, policy reload validation, and audit-relevant denial reasons. |
| `internal/config` | Typed config loading, validation, hot reload snapshots, and explicit non-production risk modes. |
| `internal/identity` | Tenant, transaction, document, shard, member, operation, block, and UUID identity types plus fingerprint/key encoding. |
| `internal/operations` | Durable operation store and operation-state persistence. Long-term admin use cases may wrap this rather than exposing generated admin protobufs through core code. |
| `internal/placement` | Replica placement checks, failure-domain validation, and placement-unhealthy reasons. |
| `internal/replication` | Peer byte prepare protocol records and transfer validation. |
| future `internal/observe` | Metrics, trace, log, audit helper types, and cardinality rules. It must not become a dependency shortcut between unrelated packages. |
| `internal/gen/scrap/...` | Generated protobuf/gRPC code. Used by transport, serialization boundaries, and clients; avoided in storage-core use-case APIs by default. |

## Boundary Data Rules

Storage core uses explicit internal Go structs for identities, physical
references, lifecycle state, operation state, and invariants.

Public and admin request/response protobufs are boundary formats. They should
be converted at API, admin, CLI, metadata serialization, published metadata, or
snapshot boundaries. New storage workflow APIs must not accept or return public
`*Request` or `*Response` protobuf messages by default.

Generated protobuf messages are allowed inside packages whose primary purpose
is serialization compatibility, such as metadata codecs, published metadata,
and transport adapters. Even there, package-level APIs should prefer narrow
domain structs when callers are not explicitly asking for serialized wire
contracts.

gRPC status codes and status details are transport output. Deep storage,
metadata, repair, backend, and shard packages should return typed internal
errors that preserve retryability, durability risk, corruption state,
authorization failure, capacity failure, and operator actionability. Transport
handlers map those errors to gRPC status codes and client-visible protobuf
details.

## Dependency Rules

These rules are intentionally reviewable now and automatable later.

1. `cmd/scrapd` may import `internal/node` and package-level version/config
   helpers. It must not contain storage workflow logic.
2. Future `cmd/scrapctl` may import generated admin clients and CLI formatting
   helpers. It must not import server-internal packages when added.
3. `internal/node` may import concrete implementations and wire them together.
   No package may import `internal/node`.
4. `internal/api` may import generated protobuf/gRPC packages, authz,
   workflow interfaces, and error mappers. Core storage packages must not
   import `internal/api`.
5. Workflow packages may import lower-level policy and mechanism packages, but
   lower-level packages must not import workflow packages.
6. `internal/blockstore`, `internal/storageformat`, `internal/metastore`,
   `internal/raftmeta`, and `internal/published` must not import gRPC status,
   public API handlers, CLI packages, backend provider-specific packages, or
   process wiring.
7. Provider-specific backend packages depend inward on `internal/backend`; the
   provider-neutral backend package must not import provider SDK packages.
8. `internal/cryptoenv` owns the OpenBao client boundary. Storage core depends
   on crypto envelope interfaces or value types, not on OpenBao client types.
9. `internal/observe` may define low-cardinality metric/log/trace helpers, but
   domain packages must not call global loggers, global meters, or process
   singletons directly.
10. Shared utility packages are allowed only when they have a narrow owner and
    stable meaning. Do not create generic `internal/common`, `internal/util`,
    or `internal/core` dumping grounds.

## Framework And Utility Library Policy

Utility libraries are allowed when they reduce local complexity without moving
architecture decisions into a framework. Their types should not leak into
storage-core public APIs unless issue `#18` records an explicit substrate
decision.

DI and lifecycle frameworks, including `go.uber.org/fx`, are composition-root
tools only. If adopted, they are allowed in `cmd/scrapd` and `internal/node`;
they are not allowed in `internal/shard`, `internal/blockstore`,
`internal/metastore`, `internal/raftmeta`, `internal/backend`, `internal/api`,
or storage-format packages. Until issue `#18` accepts such a framework, use
explicit constructors and small consumer-owned interfaces.

Error helpers such as `go.uber.org/multierr` may be used for local cleanup or
shutdown aggregation when the caller still receives a typed primary failure
with retryability and failure class preserved. They must not replace domain
error classification or become the transport mapping mechanism.

Test-only libraries such as `testify/require` or `testify/suite` are test
dependencies. They must not affect production APIs. `require`-style assertions
are acceptable for readable tests. Suites should be reserved for integration,
transport, or harness tests where shared setup is more valuable than simple
table-driven tests; they should not hide per-case invariants.

Logging, metrics, tracing, backend SDKs, OpenBao clients, and config libraries
are adapter or composition-root details. Storage policy packages receive the
minimum interfaces or value objects they need.

## Allowed Import Examples

```text
cmd/scrapd -> internal/node
future cmd/scrapctl -> internal/gen/scrap/admin/v1
internal/node -> internal/api
internal/node -> internal/backend/fs
internal/api -> internal/gen/scrap/v1
internal/api -> internal/identity
internal/shard -> internal/blockstore
internal/shard -> internal/metastore
internal/shard -> internal/backend
internal/backend/fs -> internal/backend
internal/backendupload -> internal/backend
internal/backendupload -> internal/storageformat
internal/published -> internal/gen/scrap/published/v1
```

## Disallowed Import Examples

```text
internal/blockstore -> internal/api
internal/blockstore -> google.golang.org/grpc/status
internal/metastore -> internal/node
internal/raftmeta -> internal/backend/s3
internal/backend -> internal/backend/fs
internal/shard -> github.com/openbao/openbao/api
internal/api -> cmd/scrapd
future cmd/scrapctl -> internal/node
future cmd/scrapctl -> internal/blockstore
internal/identity -> internal/gen/scrap/v1 request/response types
internal/storageformat -> go.uber.org/fx
```

## Review Checklist

- Does the package own one clear storage-gateway responsibility?
- Does its API expose internal value types instead of transport request or
  response protobufs unless it is a serialization boundary?
- Are gRPC status codes created only at API or CLI transport boundaries?
- Do source dependencies point toward storage policy rather than toward
  process wiring, handlers, providers, or frameworks?
- Are concrete backend, OpenBao, metrics, logging, and DI dependencies kept at
  adapters or composition roots?
- Could the dependency rule be checked later by an import test without
  subjective interpretation?
