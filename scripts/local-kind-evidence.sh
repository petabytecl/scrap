#!/usr/bin/env sh
set -eu

profile_id="${PROFILE_ID:-scrap-prod-v1}"
environment_id="${LOCAL_KIND_ENVIRONMENT_ID:-local-kind}"
kind_cluster="${KIND_CLUSTER:-scrap-local}"
namespace="${LOCAL_KIND_NAMESPACE:-scrap-local}"
image_name="${IMAGE_NAME:-localhost/scrapd:local}"
localstack_bucket="${LOCALSTACK_BUCKET:-scrap-local}"
localstack_image="${LOCALSTACK_IMAGE:-localstack/localstack:4.8.1}"
openbao_namespace="${OPENBAO_NAMESPACE:-root}"
openbao_image="${OPENBAO_IMAGE:-openbao/openbao:2.3.2}"
openbao_transit_key_path="${OPENBAO_TRANSIT_KEY_PATH:-transit/keys/scrap-backend}"
openbao_audit_device="${OPENBAO_AUDIT_DEVICE:-file:/tmp/openbao-audit.log}"

release_sha="$(git rev-parse HEAD)"
dirty_tree="clean"
if ! git diff --quiet || ! git diff --cached --quiet; then
	dirty_tree="dirty"
fi

rendered_manifests="$(mktemp)"
trap 'rm -f "$rendered_manifests"' EXIT
${KUSTOMIZE:-go run sigs.k8s.io/kustomize/kustomize/v5@v5.7.1} build "${LOCAL_KIND_OVERLAY:-deploy/kustomize/overlays/local-kind}" > "$rendered_manifests"
manifest_sha256="$(sha256sum "$rendered_manifests" | awk '{print $1}')"

cat <<EOF
{
  "report_kind": "local-kind-release-evidence",
  "release_sha": "$release_sha",
  "dirty_tree": "$dirty_tree",
  "profile_id": "$profile_id",
  "environment_id": "$environment_id",
  "kind_cluster": "$kind_cluster",
  "namespace": "$namespace",
  "image": "$image_name",
  "manifest_sha256": "$manifest_sha256",
  "localstack": {
    "service": "localstack.$namespace.svc",
    "image": "$localstack_image",
    "bucket": "$localstack_bucket",
    "provider_class": "s3-compatible"
  },
  "openbao": {
    "service": "openbao.$namespace.svc",
    "image": "$openbao_image",
    "namespace": "$openbao_namespace",
    "transit_key_path": "$openbao_transit_key_path",
    "audit_device": "$openbao_audit_device",
    "audit_device_status": "configured-by-local-bootstrap-job"
  },
  "evidence_limits": [
    "local kind evidence is release-artifact rehearsal evidence only",
    "local evidence does not approve live production capacity",
    "local evidence does not approve live OpenBao HA, unseal, snapshot, key custody, or audit retention",
    "local evidence does not apply GitOps manifests to a downstream production cluster"
  ],
  "operation_ids": [],
  "redacted_request_ids": []
}
EOF
