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
