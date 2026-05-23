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
