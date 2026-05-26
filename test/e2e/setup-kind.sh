#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${KIND_CLUSTER:-scrap-e2e}"
IMAGE_NAME="scrapd:latest"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "==> Checking prerequisites"
command -v kind >/dev/null || { echo "kind not found; install: https://kind.sigs.k8s.io/"; exit 1; }
command -v kubectl >/dev/null || { echo "kubectl not found"; exit 1; }
command -v docker >/dev/null || { echo "docker not found"; exit 1; }

echo "==> Creating Kind cluster (if not exists)"
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  kind create cluster --name "$CLUSTER_NAME" --wait 60s
fi
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

echo "==> Building scrapd image"
cd "$ROOT_DIR"
docker build -t "$IMAGE_NAME" .

echo "==> Loading image into Kind"
kind load docker-image "$IMAGE_NAME" --name "$CLUSTER_NAME"

echo "==> Applying K8s manifests"
kubectl apply -f deploy/k8s/scrap.yaml --context "kind-${CLUSTER_NAME}"

echo "==> Waiting for pods to be Ready"
kubectl -n scrap wait --for=condition=Ready pod -l app=scrap --timeout=120s --context "kind-${CLUSTER_NAME}"

echo "==> Cluster ready"
kubectl -n scrap get pods --context "kind-${CLUSTER_NAME}"
