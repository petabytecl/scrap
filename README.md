# S.C.R.A.P.

S.C.R.A.P. is a transaction-scoped document storage gateway for billing ETL
workflows. Services write immutable Documents through a gRPC API and read them
back by `(transaction_id, document_name)` without needing to know whether bytes
are served from hot local storage, peer replicas, or a Backend object store.

SCRAP is the stable project line. Design decisions are captured in `CONTEXT.md`
and `docs/adr/`, and release readiness is proven through local and CI
verification evidence.

Planning and execution workflow is BMAD-first:

- PRDs and planning artifacts: `_bmad-output/planning-artifacts/`
- Stories, deferred work, and evidence artifacts:
  `_bmad-output/implementation-artifacts/`
- GitHub issues are the external publication mirror for BMAD-tracked work.

## Core Model

- **Document**: immutable file stored under a Transaction.
- **Transaction**: group of related Documents that land in one Shard.
- **Block**: append-only file of framed Document bytes; the unit of Backend
  upload, eviction, restore, and repair.
- **Shard**: independent Raft group that owns Transaction visibility.
- **Backend**: object store used for durable cold Block copies.
- **Pebble Projection**: rebuildable read-side metadata projection.

Read `CONTEXT.md` before changing code. It is the canonical glossary and process
contract for this repository.

## Repository Layout

- `cmd/scrapd`: service entrypoint.
- `cmd/scrapctl`: operator CLI entrypoint.
- `internal/`: production packages.
- `gen/go/`: generated protobuf code.
- `deploy/`: Kubernetes, Helm, Cilium, and Kustomize deployment assets.
- `docs/adr/`: accepted architecture decisions.
- `test/`: integration, E2E, and stress suites.
- `scripts/`: repository verification and operational helpers.

## Requirements

- Go 1.26.4.
- Docker for Testcontainers-based integration tests, container builds, and local
  `act` workflows.
- `kubectl` for Kubernetes-oriented targets.
- GitHub CLI (`gh`) for local `act` targets that need GitHub tokens.

Go-managed tools are pinned through `tools.go.mod` and invoked through
`go tool -modfile=tools.go.mod` by the Makefile.

## Common Commands

```sh
make help
make test
make test-race
make integration
make check
make production-rehearsal-security
```

`make production-rehearsal-security` runs the local production-mode security
rehearsal with real OpenBao Transit and a filesystem Backend. The full S3
rehearsal and evidence rules are documented in `docs/production-rehearsal.md`.

Local GitHub Actions validation uses `act` and the repo-local `.actrc`:

```sh
make act-ci-validate
make act-ci-job ACT_JOB=test
make act-ci-job ACT_JOB=race
make act-ci
make act-ci-clean
```

`make act-ci` runs the workflow dispatch path and cleans up local act containers
and the prod-like Kind Cell on exit. If a run is interrupted hard, run
`make act-ci-clean` before retrying.

## Security

Security policy and private reporting instructions are in `SECURITY.md`.

## License

S.C.R.A.P. is distributed under the MIT License. See `LICENSE.md`.
