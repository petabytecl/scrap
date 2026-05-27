# S.C.R.A.P. — Project Rules

## Context

Read `CONTEXT.md` at the repo root before working on the codebase. It defines
the domain vocabulary (Document, Transaction, Block, Frame, Shard) and
architectural constraints. Use the glossary terms exactly — do not drift to
synonyms the glossary explicitly avoids.

### Package map

| Package            | Domain concept | Responsibility                                       |
| ------------------ | -------------- | ---------------------------------------------------- |
| `internal/shard/`  | Shard          | Shard lifecycle, Raft integration, write path        |
| `internal/block/`  | Block, Frame   | Block writer, frame encoding/decoding, reader        |
| `internal/index/`  | Projection     | Pebble projection (document → block lookup)          |
| `internal/scrub/`  | Scrub          | Light + deep scrub integrity verification            |
| `internal/raft/`   | Raft           | Raft node, peer resolution, log management           |
| `internal/peer/`   | Peer           | gRPC peer transport, replication, consistency checks |
| `internal/store/`  | Store          | Store interface contract and error sentinels         |
| `internal/server/` | Server         | gRPC client-facing API server                        |
| `internal/admin/`  | Admin          | HTTP admin server (metrics, health)                  |
| `internal/ulid/`   | ULID           | Custom ULID generator (ADR-0007)                     |

## Development

### Build and test

| Command            | What it does                                         |
| ------------------ | ---------------------------------------------------- |
| `make build`       | Compile all command binaries to `bin/`               |
| `make test`        | Run unit tests                                       |
| `make test-race`   | Run unit tests with the race detector                |
| `make test-cover`  | Run tests with coverage profile and JUnit XML        |
| `make integration` | Run integration tests (`test/integration/`)          |
| `make lint`        | Run golangci-lint                                    |
| `make static`      | Run all static analysis and format checks            |
| `make check`       | Full local verification gate (static + tests + race) |
| `make proto`       | Regenerate protobuf/gRPC code                        |
| `make proto-check` | Lint protos and verify generated code is up to date  |
| `make e2e-setup`   | Build image, load into Kind, deploy manifests        |

### Protobuf workflow

Proto source lives in `proto/scrap/`. Run `make proto` to regenerate. Generated
code lands in `gen/go/` — never edit by hand. After changing a `.proto` file,
always run `make proto` before committing.

### Code style

All Go code must follow `docs/go-style-guide.md`. The guide covers design
decisions, naming, error handling, concurrency, testing, performance, metrics,
and documentation conventions. Mechanical formatting is enforced by
`.golangci.yml`.

### ADR conventions

Architecture Decision Records live in `docs/adr/`. Use the format:

- **File name:** `NNNN-slug.md` (zero-padded, sequential)
- **Sections:** `# Title`, `Status: Accepted|Superseded`, `Date: YYYY-MM-DD`,
  `## Context`, `## Decision`

Create an ADR when a decision changes the storage format, wire protocol,
dependency choices, or cross-package boundaries. Do not create ADRs for
implementation details that are local to a single package.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues on `petabytecl/scrap` via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default label vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context repo — one `CONTEXT.md` at the root, ADRs in `docs/adr/`. See `docs/agents/domain.md`.
