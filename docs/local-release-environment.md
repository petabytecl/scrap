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
- `openbao/openbao:2.5.3` in dev/test mode stands in for real Transit API and
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
make openbao-smoke-evidence
make capacity-sample
make local-soak-evidence
make local-dr-drill-evidence
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

`make openbao-smoke-evidence` writes
`openbao-transit-smoke-evidence.json` by default. It uses the local kind
OpenBao deployment and a short-lived Kubernetes-authenticated smoke client to
prove Transit data-key, unwrap, rewrap, audit, and crypto-unavailable evidence
shape without granting broad key-admin permissions. The evidence boundary and
fields are documented in [OpenBao Transit Smoke Coverage](openbao-transit-smoke-coverage.md).

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

## Local Soak And Capacity Rehearsal

`make local-soak-evidence` runs the #87 local release soak against the local
kind public and admin gRPC services and writes `local-soak-evidence.json` by
default. It consumes the #100 `capacity-sample-advisory.json` report, writes and
reads bounded documents through the public API, captures admin disk runway,
repair queue lag, restore/backlog visibility, backend upload-lag samples, and
OpenBao latency/error/saturation behavior.

The report records release SHA, image identity, runner/profile, dirty-tree
status, workload shape, document-size distribution, duration, capacity profile,
and explicit links to #48, #87, #99, and #100. It treats #100 values as proposed
advisory thresholds only. If a local rehearsal failure or advisory threshold
violation appears, the evidence status is failed and the release must link a
blocking issue or an approved owner+expiry exception before signoff.

The default command expects these local services to be reachable:

```sh
make local-soak-evidence \
  SCRAP_PUBLIC_ADDR=127.0.0.1:18080 \
  SCRAP_ADMIN_ADDR=127.0.0.1:18081 \
  CAPACITY_SAMPLE_REPORT=capacity-sample-advisory.json
```

## Local DR Drill Rehearsal

`make local-dr-drill-evidence` runs the #88 release-artifact DR drill rehearsal
and writes `local-dr-drill-evidence.json` by default. It first refreshes the
LocalStack capacity sample and OpenBao Transit smoke reports, then writes a
bounded fixture document through the local public API and drives the DR Rebuild
Drill operator flow through the admin API.

The local-kind `scrapd` manifest lowers the non-production block seal threshold
so small rehearsal writes can seal, upload, and publish a restorable metadata
checkpoint without writing a production-scale block. The drill records each
runbook command, operator approval, config/profile, image identity, release SHA,
dirty-tree status, operation IDs, terminal status, measured local recovery time,
latest restorable checkpoint time, LocalStack backend probe evidence, published
checkpoint artifact verification, and OpenBao crypto-unavailable outcomes.

Local adaptation: the single local-kind source service cannot also be a separate
fresh target cluster. The `dr-drill` operation owns the fresh scratch metadata
store used to prove metadata import and backend byte restore. The
`metadata-restore` step is run from the dry-run recovery plan so the source cell
is not mutated while still verifying the published checkpoint.

This evidence remains release-artifact rehearsal only:

- it does not approve live production RTO or RPO;
- it does not approve downstream deployment rollout;
- it does not approve provider account, object-store bucket, or OpenBao HA
  operations;
- any failed or skipped step requires a linked blocking issue or an approved
  owner+expiry release exception before signoff.

The default command expects public/admin gRPC, LocalStack, and OpenBao to be
reachable:

```sh
make local-dr-drill-evidence \
  SCRAP_PUBLIC_ADDR=127.0.0.1:18080 \
  SCRAP_ADMIN_ADDR=127.0.0.1:18081 \
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
