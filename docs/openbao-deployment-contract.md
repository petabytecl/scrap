# OpenBao Deployment Contract

OpenBao Transit is production security infrastructure consumed by S.C.R.A.P. It
is not part of the `scrapd` application container and must not be hidden behind
test-only Fake Transit when making production-readiness claims.

## Ownership

The production default is platform-managed OpenBao. The platform team owns the
OpenBao deployment, seal/unseal or auto-unseal policy, key lifecycle, Transit
policy, token issuance, network reachability, audit retention, and disaster
recovery.

S.C.R.A.P. owns only the client contract:

- the Transit endpoint address;
- the Transit mount and key name;
- the environment variable name that contains the token;
- TLS trust for the endpoint;
- fail-closed startup and runtime behavior when Transit is unavailable.

A self-contained UAT or prod-like Cell may deploy OpenBao in-cluster, but that
must be explicit in that environment. In-cluster UAT OpenBao is not the
production default and must not make `SCRAP_TRANSIT_FAKE=true` acceptable for
production-readiness evidence.

## Kustomize Component

`deploy/kustomize/components/external-openbao-transit` wires the `scrapd`
environment contract for an operator-provided OpenBao Transit endpoint.

`deploy/kustomize/environments/prodlike-openbao` renders prod-like S.C.R.A.P.
with that component included. It is a manifest contract target, not a complete
OpenBao installation.

The component provides a non-secret ConfigMap:

```text
scrap-openbao-transit-config
```

Required keys:

- `address`: absolute `https://` OpenBao address
- `mount`: Transit mount path, for example `transit`
- `key`: Transit key name, for example `scrap-documents`

The component expects a platform-owned Secret:

```text
scrap-openbao-transit-token
```

Required key:

- `token`: OpenBao token used by `scrapd`

The StatefulSet receives:

- `SCRAP_TRANSIT_ADDR` from `scrap-openbao-transit-config.address`
- `SCRAP_TRANSIT_MOUNT` from `scrap-openbao-transit-config.mount`
- `SCRAP_TRANSIT_KEY` from `scrap-openbao-transit-config.key`
- `SCRAP_TRANSIT_TOKEN_ENV=OPENBAO_TOKEN`
- `OPENBAO_TOKEN` from `scrap-openbao-transit-token.token`

Do not commit the Secret manifest with a real token. Create it through the
platform secret manager, External Secrets, Sealed Secrets, `kubectl create
secret`, or another private delivery path.

## TLS Trust

`SCRAP_TRANSIT_ADDR` must use HTTPS. If OpenBao uses a certificate chain already
trusted by the `scrapd` image, no extra mount is required. If it uses a private
CA, the deployment must provide the CA bundle through a platform-owned
ConfigMap, Secret, image trust store, or equivalent mechanism and point the Go
runtime at that trust bundle, for example with `SSL_CERT_FILE`.

Do not commit private CA keys, generated cert private keys, OpenBao recovery
keys, unseal keys, root tokens, or runtime logs.

## NetworkPolicy And RBAC

Network policy is defense in depth; it does not replace application mTLS,
authorization, audit, or the production startup gate.

Production and prod-like environments must provide:

- egress from `scrapd` pods to the OpenBao HTTPS listener;
- ingress to OpenBao only from approved S.C.R.A.P. namespaces or identities;
- no public exposure of the OpenBao listener;
- DNS reachability for the configured endpoint;
- audit/log egress according to the OpenBao owner policy.

Kubernetes RBAC for `scrapd` must not grant broad Secret read permissions. Token
delivery should happen through a Secret volume/env reference or platform secret
projection that lets the kubelet inject only the named token.

## Evidence Rules

Production-readiness evidence must show real Transit:

- `SCRAP_TRANSIT_FAKE` absent or false;
- `SCRAP_TRANSIT_ADDR` set to an HTTPS endpoint;
- `SCRAP_TRANSIT_TOKEN_ENV` set to the token environment variable;
- the referenced token injected from a private Secret path;
- no token, private key, raw Document payload, Backend key, or raw log material
  pasted into public issues or pull requests.

`make production-rehearsal-security` proves local production-mode behavior with
real OpenBao Transit and filesystem Backend. `make production-rehearsal` adds
real S3/IAM Backend proof when configured with a real bucket and credentials.
