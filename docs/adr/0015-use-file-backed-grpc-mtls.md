# Use file-backed gRPC mTLS credentials for v1 bootstrap

Status: accepted

## Context

Issue #137 requires mutual TLS on both public and admin gRPC listeners and asks
for a certificate infrastructure decision before implementation. The issue
lists two viable sources:

- cert-manager plus a Kubernetes CA
- SPIFFE/SPIRE

The security chain is sequential. Listener encryption and client certificate
verification are #137. Binding the authenticated workload identity to the mTLS
certificate is #138. Tenant isolation follows in #139.

## Decision

Use file-backed PEM inputs for the server process:

- `TLSEnabled`
- `TLSCertFile`
- `TLSKeyFile`
- `TLSCACertFile`

For the v1 Kubernetes bootstrap, source those files from cert-manager-managed
certificates backed by the Kubernetes CA. This gives the node process standard
server certificate, key, and client-CA files without coupling the process to a
cluster-specific certificate API.

Do not introduce SPIFFE/SPIRE as part of #137. SPIFFE remains the stronger
long-term identity substrate, but adopting it before the workload-identity
binding work would mix transport encryption with the #138 identity invariant.

## Consequences

Both public and admin gRPC servers use `grpc.Creds(credentials.NewTLS(...))`
with `tls.RequireAndVerifyClientCert`. Production listener startup fails unless
TLS is enabled with certificate, key, and client-CA files. Local
non-production storage may keep TLS disabled for test and development loops.

The same TLS configuration currently applies to both listeners. Separate
listener certificates or live certificate reload can be added later if a
deployment requirement proves the need.

## Acceptance Criteria

- Public and admin gRPC listeners require a client certificate when TLS is
  enabled.
- Production startup fails closed when TLS is disabled.
- Missing TLS certificate, key, or client-CA fields fail with field-specific
  errors.
- Local non-production mode can bypass mTLS for existing local test workflows.
