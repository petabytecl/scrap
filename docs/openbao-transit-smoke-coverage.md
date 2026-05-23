# OpenBao Transit Smoke Coverage

S.C.R.A.P. correctness tests use deterministic fake Transit behavior for
repeatable envelope, unwrap, rewrap, outage, and missing-key cases. A production
release still needs a small smoke run against a real OpenBao Transit deployment
before OpenBao-backed backend encryption is enabled.

Required smoke coverage:

- Kubernetes-authenticated client can request a data key for the configured
  backend Transit key without receiving broad key-admin permissions.
- Wrapped DEK can be unwrapped only with the expected key name, key version, and
  AAD context.
- Rewrap changes only the envelope object material and preserves the block
  object and index object bytes.
- Transit outage and missing key-version material are surfaced as
  crypto-unavailable outcomes without exposing plaintext DEKs, wrapped DEKs, or
  OpenBao tokens in logs, metrics, audit events, or operation metadata.
- OpenBao audit devices record data-key, unwrap, and rewrap requests with enough
  request identity, key, version, block, and operation context for security
  review.

Evidence artifacts should include the OpenBao namespace/key path, key version
before and after rewrap, operation ID, audit device status, and redacted request
IDs. They must not include plaintext DEKs, wrapped DEKs, backend object bytes, or
OpenBao tokens.

## Local Rehearsal Command

The repo-owned local evidence path is:

```sh
export BAO_TOKEN=local-root
make openbao-smoke-evidence
```

`make openbao-smoke-evidence` creates a short-lived Kubernetes service account
JWT for `openbao-transit-smoke` in the `scrap-local` namespace and runs
`scrap-openbao-smoke`. The token is passed through the process environment, not
through CLI flags. The generated report defaults to
`openbao-transit-smoke-evidence.json`.

The local-kind OpenBao deployment enables file audit from its server config. The
bootstrap job enables the `kubernetes` auth method, creates the
`scrap-transit-client` policy, and binds it to the `openbao-transit-smoke`
service account. That policy can request data keys, unwrap, rewrap, and inspect
its own capabilities; it does not grant broad Transit key-admin permissions such
as create, update, delete, or sudo on `transit/keys/*`.

The report records:

- release SHA, dirty-tree status, profile ID, namespace, and OpenBao deployment;
- Kubernetes auth role, returned policies, and self-reported capabilities;
- Transit key name and key versions before data-key, after rotate, and after
  rewrap;
- AAD context digest, unwrap/rewrap AAD match status, operation IDs, audit
  device status, and redacted request IDs;
- crypto-unavailable outcomes for a missing key version and a configured
  transport outage probe;
- explicit evidence limits stating that local smoke is release rehearsal only.

The command returns non-zero if the Kubernetes-authenticated token has broad key
admin capabilities, if the expected Transit operations fail, or if
crypto-unavailable cases unexpectedly succeed.
