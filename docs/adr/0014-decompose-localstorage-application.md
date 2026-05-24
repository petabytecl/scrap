# Decompose localstorage Application by use-case roles

Status: accepted

## Context

`internal/localstorage.Application` began as a single in-process composition
root and accumulated document, transaction, verification, operation, member,
repair, inspection, and disaster-recovery behavior. That made the public API
slots in `cmd/scrapd` easy to wire, but it also made `Application` the default
receiver for new local storage work.

The issue tracker requested "ADR-005" for this decision. The repository already
uses `0005` for envelope encryption, so this decision is recorded as ADR-0014.

## Decision

Keep `Application` as the local storage composition root and move behavior to
focused internal role types:

- `documentApplication`: document metadata reads, byte reads, cold-read restore
  enqueueing, and document search.
- `transactionCoordinator`: write admission, idempotent write replay,
  prepare-log persistence, peer prepare, metadata commit, and transaction state.
- `verificationEngine`: byte-serving readiness checks and backend checksum /
  envelope verification.
- `OperationExecutor`: queued operation recovery and execution, including
  restore, repair, scrub, drain, rewrap, tombstone, copy-verify, and DR drill
  workflows.

`cmd/scrapd` wires the public document and transaction gRPC slots to
`localApp.Documents()` and `localApp.Transactions()` instead of handing both
slots the whole `Application`. The background operation runner receives
`localApp.OperationExecutor()` explicitly. `Application` keeps thin delegate
methods for existing tests and local tooling while callers migrate to role
interfaces.

Do not add a dependency-injection framework for this split. Manual composition
keeps the dependency graph visible, avoids reflection or generated wiring, and
is enough for the current package shape.

## Mutex Partitioning

Do not partition locks in this ADR. The current mutable lock domains are already
smaller than the issue text suggested:

- `memberMu` guards local member admission state.
- `byteServingMu` guards byte-serving readiness.
- durable metadata and block state are protected by their stores.

Further lock partitioning must be driven by contention evidence or a concrete
shared-state invariant. It should not be bundled with behavior extraction.

## Test Migration

Existing `app_test.go` coverage remains unchanged during the first extraction.
Compatibility delegates on `Application` keep the current tests focused on
behavior instead of forcing a broad test rewrite in the same patch. New tests for
future localstorage work should target the role that owns the behavior.

## Acceptance Criteria

- `internal/localstorage/app.go` stays below 800 lines and contains the
  composition root rather than document or operation workflows.
- `cmd/scrapd` wires document, transaction, and operation execution through
  role-specific values.
- The six server application slots continue to compile against their existing
  interfaces.
- Full repository checks pass without modifying test assertions.
- New localstorage behavior should be added to a role type first; only thin
  compatibility delegates belong on `Application`.
