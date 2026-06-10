# OpenBao API client boundary

Status: Accepted

Date: 2026-06-10

## Context

OpenBao Transit is the V2 cryptographic substrate, and ADR 0020 defines the
envelope encryption contract. The first implementation used direct HTTP request
construction inside the Transit adapter, while the new Testcontainers OpenBao
fixture also needed to configure mounts and keys.

Keeping raw request construction in production code would make S.C.R.A.P. own
OpenBao URL construction, auth headers, response parsing, retry defaults, and
provider error shape. That duplicates behavior already provided by OpenBao's
official Go API client and increases drift risk between integration fixtures and
the production Transit adapter.

The stable `github.com/openbao/openbao/api` semver tags currently declare the
legacy Vault module path in their `go.mod`; the usable OpenBao import path is
available from the OpenBao API module's main-line pseudo-version.

## Decision

Use `github.com/openbao/openbao/api` as the only application-level client for
OpenBao interactions in S.C.R.A.P. Production Transit calls, integration fixture
bootstrap, fixture validation, and test-only Transit key rotation use the
official client.

The OpenBao API client stays behind S.C.R.A.P.-owned boundaries:

- `internal/encryption` continues to expose only the `Transit` interface and
  `OpenBaoConfig`.
- OpenBao client types do not flow into Shard, Backend, server, admin, or public
  API contracts.
- S.C.R.A.P. retains its own config validation, redaction, fail-closed behavior,
  and `ErrUnavailable` / `ErrAuthDenied` / `ErrMissingKey` /
  `ErrMinimumVersion` / `ErrInvalidRequest` taxonomy.
- The adapter disables the official client's default retries for now so this
  dependency swap does not change Transit retry behavior.

Do not add raw OpenBao HTTP calls in application or test fixture code unless a
future ADR documents why the official client cannot model that operation.
