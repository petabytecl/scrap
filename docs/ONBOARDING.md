# S.C.R.A.P. Onboarding

Status: bus-factor mitigation artifact for issue `#145`
Owner: `@cotocisternas`
Last verified: not yet validated by a new contributor

Use this guide to orient a new contributor, reviewer, or incident responder
before they modify storage-gateway code.

## Start Here

Read these files first:

| File | Why |
| --- | --- |
| [`CONTEXT.md`](../CONTEXT.md) | Domain language and storage-gateway mental model. |
| [`docs/storage-gateway-design-notes.md`](storage-gateway-design-notes.md) | Full design notes and deferred decisions. |
| [`docs/storage-gateway-package-architecture.md`](storage-gateway-package-architecture.md) | Package ownership and dependency rules. |
| [`docs/storage-gateway-durability-coding-guidelines.md`](storage-gateway-durability-coding-guidelines.md) | Durability and fail-closed coding rules. |
| [`docs/storage-gateway-operator-runbooks.md`](storage-gateway-operator-runbooks.md) | Shared operator workflows. |
| [`docs/adr/`](adr/) | Accepted architecture decisions. |

## Architecture Overview

S.C.R.A.P. is a storage gateway for billing ETL document workflows. The service
fleet talks to S.C.R.A.P. through custom gRPC APIs. S.C.R.A.P. hides whether
bytes are served from local hot storage, peer replicas, or a backend object
store.

The main runtime layers are:

| Layer | Packages | Responsibility |
| --- | --- | --- |
| Transport | `internal/api`, `internal/node`, `internal/gen` | gRPC validation, authz, streaming, status mapping, server wiring. |
| Application workflow | `internal/localstorage`, `internal/storageapp` | Write/read orchestration, restore, repair, DR, and admin workflows. |
| Metadata authority | `internal/raftmeta`, `internal/metastore` | Authoritative document visibility, transactions, and durable metadata. |
| Byte storage | `internal/blockstore`, `internal/backendupload`, `internal/backend` | Local blocks, backend object uploads, restore sources, and verification. |
| Security and operations | `internal/authz`, `internal/observe`, `internal/operations` | Workload authorization, bounded metrics, durable operation records, and audit. |

Spike code under `cmd/scrap-spike` and `internal/spike` is evidence, not the
production package architecture.

## Five Design Invariants

1. A document is visible only after bytes and authoritative metadata satisfy the
   configured write-ACK contract.
2. Reads fail closed: S.C.R.A.P. verifies every required byte range before
   streaming document bytes.
3. Metadata authority lives in Raft/metastore state. Local indexes and caches
   are derived projections.
4. Security identity is server-derived from mTLS/workload identity, never from
   caller-supplied document metadata.
5. Durable artifact replacement writes a verified replacement first, then
   atomically renames. Do not delete the old artifact before replacement
   verification succeeds.

## Local Setup

Required tools:

- Go toolchain from `go.mod`
- `buf`
- Docker and kubectl for local release rehearsal targets. The Makefile runs
  kind and kustomize through Go by default.

Start with:

```sh
make help
make check
make vuln
```

`make check` is the default pre-PR gate. It runs manifest validation,
formatting checks, protobuf validation, compatibility tests, linting, package
tests, crash/fault catalog validation, race tests, and binary builds.

## Test Suite Guide

Use narrower tests while iterating, then run the full gate before committing.

| Need | Command |
| --- | --- |
| Package correctness | `go test ./internal/<package>` |
| Full package tests | `make test` |
| Race detector | `make test-race` |
| Durable format compatibility | `make test-compat` |
| Crash/fault catalog pattern validation | `make test-crashfault-catalog` |
| Lint baseline | `make lint` |
| Vulnerability scan | `make vuln` |
| Full local gate | `make check` |

For proto changes, run `make proto-check` and stage generated files under
`internal/gen`.

## Local Write/Read Cycle

The quickest local write/read evidence loop is the disposable spike:

```sh
make spike-write-path
make spike-write-path-raft
make spike-write-path-raft-durable
make spike-write-path-raft-cluster
```

The spike writes synthetic transactions, finalizes documents, checks
`HeadDocument`, reads bytes back, and reports invariant failures. It is useful
for orientation and design evidence, but production changes must be implemented
in the production packages and verified by the normal tests.

For production-shaped server behavior, use package tests around:

- `internal/api` for public/admin gRPC validation and streaming;
- `internal/node` for server limits, mTLS, authz, and health behavior;
- `internal/localstorage` for write/read, restore, repair, and DR workflows;
- `internal/blockstore` for block and frame IO;
- `internal/raftmeta` for metadata authority behavior.

## Change Workflow

1. Read the issue and any parent issue checklist.
2. Check relevant ADRs and package rules before choosing a design.
3. Write focused tests for behavior or update docs in the same change when the
   user-facing or operator contract changes.
4. Run targeted tests while iterating.
5. Before committing, run `git diff --check`, a changed-file secret scan,
   `make check`, and `make vuln`.
6. Open a draft PR, wait for CI, mark ready, wait for review, address comments,
   resolve conversations, wait for final CI, merge, and update issue checklists.

## Ownership Limit

`.github/CODEOWNERS` records the current ownership boundary and lets branch
protection require owner review on critical paths. The repository currently has
a solo maintainer, so CODEOWNERS does not prove second-human review until
another maintainer or owner team is added to those paths.

## Incident And Runbook Orientation

Focused runbooks live in [`docs/runbooks/`](runbooks/):

- [Raft Leadership Loss](runbooks/raft-leadership-loss.md)
- [Block Store Corruption](runbooks/block-store-corruption.md)
- [Backend Restore Failure](runbooks/backend-restore-failure.md)

The broader operator workflow reference remains
[Storage Gateway Operator Runbooks](storage-gateway-operator-runbooks.md).

## First Contribution Checklist

- [ ] Run `make check`.
- [ ] Run `make spike-write-path`.
- [ ] Read at least five ADRs relevant to your change.
- [ ] Trace one public write or read from `internal/api` through
      `internal/localstorage`.
- [ ] Identify which package owns the behavior you plan to change.
- [ ] Confirm whether the change affects durability, security, metrics,
      operator workflows, or public API contracts.
- [ ] Update docs or runbooks if the operator or contributor workflow changes.
