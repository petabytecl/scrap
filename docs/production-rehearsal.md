# Production Rehearsal

The production rehearsal target proves that `scrapd` can start in production
security mode, use real OpenBao Transit, write and read an encrypted Document,
confirm at least one sealed Block upload through the configured Backend, and
reject the test-only shortcuts that are valid in local E2E fixtures.

This rehearsal is not a Kubernetes deployment manifest. It is a local operator
gate that generates short-lived credentials under `artifacts/`, starts OpenBao
in Docker, starts one `scrapd` Member, and records a machine-readable report.

## Targets

```sh
make production-rehearsal-security
```

Runs the local production security rehearsal with:

- `SCRAP_SECURITY_MODE=production`
- TLS 1.3 mTLS on public, peer, admin, and `scrapctl` paths
- generated role, peer identity, audit, and rate-limit policies
- real OpenBao Transit over TLS
- filesystem Backend
- `SCRAP_TEST_HOOKS=false`
- `SCRAP_PPROF_ENABLED=false`

Use this target to prove the production security and Transit path without
requiring cloud credentials.

```sh
make production-rehearsal
```

Runs the same rehearsal with the S3 Backend. This target requires a real S3
configuration from the normal environment and fails fast when required values
are missing.

Required values:

- `SCRAP_S3_BUCKET`
- `SCRAP_S3_REGION`
- AWS credentials from the default provider chain, such as environment
  variables, a configured profile, or workload identity

`SCRAP_S3_ENDPOINT` must not point at localhost, LocalStack, or another test
endpoint unless `SCRAP_PROD_REHEARSAL_ALLOW_LOCAL_S3=true` is set explicitly.
That override is for development diagnosis only and does not satisfy production
readiness evidence.

The default rehearsal Cell ID includes a per-run suffix so repeated S3 runs do
not reuse Backend object keys. If `SCRAP_PROD_REHEARSAL_CELL_ID` is supplied
explicitly, choose an isolated value for that run.

```sh
make production-rehearsal-down
```

Stops leftover rehearsal processes and the OpenBao container.

## Report

Successful runs write:

```text
artifacts/production-rehearsal/report.json
```

The report records:

- production security mode and readiness status
- selected Backend
- pinned OpenBao image
- real OpenBao Transit usage
- test hooks disabled
- pprof disabled
- encrypted write/read success
- plaintext leak scan success
- committed Backend upload confirmation success and count
- log directory for local inspection

Generated TLS material, OpenBao initialization data, tokens, logs, and runtime
state stay under `artifacts/production-rehearsal/`, which is ignored by Git.
Do not copy token values, private keys, Document payloads, raw Backend object
keys, validation tokens, or raw logs into public issues or pull requests.

## Certificate Rotation

The initial production bridge uses restart-based certificate rotation. To rotate
SCRAP listener, client, or CA material:

1. Write the replacement certificate, key, and CA bundle to the mounted secret
   or host path for the affected surface.
2. Restart or roll the affected `scrapd` Members and `scrapctl` clients.
3. Verify startup succeeds in `SCRAP_SECURITY_MODE=production`; startup gates
   fail closed if files are missing, expired, untrusted, or not valid for the
   configured server name or client identity.
4. Run `make production-rehearsal-security` and keep the generated report with
   the relevant production-readiness issue or pull request.

Hot certificate reload is not part of the Phase 4.5 production contract. Adding
it requires a separate design for connection draining, rollback, and evidence.

## What This Proves

`production-rehearsal-security` proves that a local production-mode `scrapd`
can:

- fail open test shortcuts closed
- load production TLS, role policy, peer identity policy, audit policy, and
  rate-limit policy
- connect to a real TLS OpenBao Transit endpoint
- write, head, and read a Document through the public gRPC API with mTLS
- keep the plaintext payload out of the local data directory
- force a Block seal, upload the sealed Block through the configured Backend,
  and observe committed `ConfirmUpload` authority before declaring success
- report production readiness through the admin status surface

`production-rehearsal` adds S3 Backend proof when run with real S3 credentials
and a real bucket. A successful S3 run proves that the same committed upload
confirmation path completed after S3 PUT and HEAD verification.

## What This Does Not Prove

The rehearsal does not deploy OpenBao to Kubernetes and does not define the
production ownership model for OpenBao. Production and prod-like deployments
still need a separate manifest or platform contract that provides:

- `SCRAP_TRANSIT_ADDR`
- `SCRAP_TRANSIT_MOUNT`
- `SCRAP_TRANSIT_KEY`
- `SCRAP_TRANSIT_TOKEN_ENV`
- the referenced token Secret
- certificate trust for the OpenBao endpoint
- network policy and RBAC around the OpenBao dependency

For real production, OpenBao should normally be treated as managed security
infrastructure consumed by SCRAP, not as a `scrapd` sidecar. A self-contained
UAT or prod-like cluster may still deploy OpenBao in-cluster for rehearsal
purposes, but that is a separate deployment decision.

## Closure Use

Local output is useful development evidence. Closing production-readiness work
requires current, attributable evidence linked from the relevant GitHub issue or
pull request. When the claim includes S3/IAM behavior, attach a successful
`make production-rehearsal` report from a real S3 configuration; the filesystem
security rehearsal alone is not enough for that claim.
