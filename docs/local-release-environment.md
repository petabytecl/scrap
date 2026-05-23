# Local Release Evidence Environment

Status: release-artifact rehearsal contract for GitHub issue `#99`
Last updated: 2026-05-23

This repository owns release artifacts and local evidence for `scrap-prod-v1`.
It does not apply manifests to a live production cluster, choose the final
object-store account, approve live OpenBao HA, or sign off production capacity
for an environment it does not operate.

## What This Environment Proves

The local environment proves that the repo can build a `scrapd` image, render
GitOps YAMLs, run a local Kubernetes rehearsal, and produce evidence with the
same identifiers used by production-readiness gates.

Local evidence is intentionally limited:

- Docker + kind stands in for Kubernetes scheduling and service wiring.
- `localstack/localstack:4.8.1` stands in for the S3-compatible backend class.
- `openbao/openbao:2.3.2` in dev/test mode stands in for real Transit API and
  audit-device smoke.
- Local disk and backend observations are rehearsal data, not production
  capacity approval.

## Command Surface

Use the Makefile as the operator surface:

```sh
make image
make manifests-render
make manifests-check
make local-kind-create
make local-kind-load
make local-kind-deploy
make local-kind-smoke
make local-kind-evidence
make capacity-sample
make local-kind-delete
```

The default image is `localhost/scrapd:local`. Override `IMAGE_NAME` to test a
different immutable tag or registry path:

```sh
make image IMAGE_NAME=ghcr.io/petabytecl/scrap/scrapd:$(git rev-parse --short HEAD)
```

`make image` builds for `IMAGE_GOOS` and `IMAGE_GOARCH`, defaulting to Linux and
the local Go architecture, then passes the matching `IMAGE_PLATFORM` to Docker.
Override all three together when building for a different kind node platform.

## Evidence Record

`make local-kind-evidence` writes `local-kind-evidence.json` by default. The
report records:

- release SHA and dirty-tree status;
- release profile ID;
- local environment and kind cluster identity;
- image name;
- rendered manifest checksum;
- LocalStack image, fixed local S3 bucket, and provider class;
- OpenBao image, namespace, Transit key path, and audit-device status;
- explicit limits stating the evidence is local release rehearsal only.

Do not store secrets, OpenBao tokens, backend credentials, document bytes,
plaintext DEKs, wrapped DEKs, or customer payloads in local evidence reports.

## Advisory Capacity Sampling

`make capacity-sample` runs `scrapctl capacity sample` against the local
rehearsal targets and writes `capacity-sample-advisory.json` by default. The
command samples the admin runway RPC, the configured non-production backend URL,
the configured OpenBao Transit key, and the local disk path. It records bounded
workload shape, request latency samples, error classes, redacted request IDs,
and proposed capacity-profile values for human review.

Backend requests are signed with local S3-compatible SigV4 credentials sourced
from the environment. The default LocalStack credentials are `test` / `test` in
`us-east-1`; override `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, or
`CAPACITY_SAMPLE_BACKEND_REGION` when rehearsing a different local target.

The report is advisory only:

- it does not write production configuration;
- it does not update signoff documents;
- it does not satisfy production write ACK readiness gates;
- it does not approve live production capacity, OpenBao HA, key custody, audit
  retention, or downstream deployment rollout.

The default local targets assume port-forwarded or locally reachable services:

```sh
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export BAO_TOKEN=local-root

make capacity-sample \
  CAPACITY_SAMPLE_BACKEND_URL=http://127.0.0.1:4566/scrap-local \
  CAPACITY_SAMPLE_OPENBAO_ADDR=http://127.0.0.1:8200
```

## GitOps Boundary

The Kustomize overlay under `deploy/kustomize/overlays/local-kind` is the local
release rehearsal overlay. Downstream deployment owners can consume or adapt the
rendered shape, but their production GitOps repository remains responsible for
environment-specific values:

- object-store provider, account, bucket, region, storage class, and restore
  class;
- production OpenBao HA topology, unseal or recovery procedure, snapshots,
  audit retention, key custody, and namespace policy;
- node class, disk class, filesystem, topology, and live capacity profile;
- legal hold authority, retention periods, and compliance approval;
- applying manifests and recording production rollout evidence.
