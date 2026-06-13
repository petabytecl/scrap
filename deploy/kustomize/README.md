# Kustomize layout

Canonical render targets live under `environments/`.

- `environments/local`: local Kind Cell with LocalStack and NodePorts.
- `environments/local-scrub`: local Kind Cell with fast scrub and test hooks.
- `environments/prodlike`: Cilium-backed prod-like Cell without test hooks.
- `environments/prodlike-openbao`: prod-like Cell wired to an external
  OpenBao Transit dependency without test hooks.
- `environments/prodlike-e2e`: prod-like Cell with Tier 2 E2E test hooks.
- `environments/evidence`: evidence workload Cell for stress/evidence runs.

Reusable pieces live under `components/`.

- `components/localstack`: LocalStack Service/Deployment and S3 upload config.
- `components/external-openbao-transit`: OpenBao Transit env and Secret
  references for operator-provided Transit.
- `components/nodeports`: local NodePort exposure for client and admin ports.
- `components/scrub-fast`: fast scrub/test-hook settings for local tests.
- `components/stress-tuning`: stress/evidence workload settings.
- `components/evidence-stack`: standalone OTel evidence stack.
- `components/monitoring`: legacy Prometheus/Grafana monitoring stack.

The old `overlays/*` paths are compatibility aliases. New Make targets and
scripts should use `environments/*` or `components/*` directly.
