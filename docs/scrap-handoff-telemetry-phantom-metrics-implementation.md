# Handoff — Telemetry phantom metrics fix implemented

**Status:** Code is implemented and verified locally + against the live Kind
stress cluster. It is **not committed**.

**Implementation worktree:** `/tmp/scrap-phantom-metrics`

**Branch:** `fix/telemetry-phantom-metrics`

**Base:** `main` at `4e0d3e31e34796f123cde09dcb0e3cf4e3f845ab`

**Original checkout:** `/home/coto/dev/petabyte/scrap` still has unrelated
dirty shard/index changes. Leave them alone unless the user explicitly redirects.

## What changed

Do not duplicate the diff; inspect the files directly:

- `internal/telemetry/resource.go`
  - Added `ResourceConfig.InstanceID`.
  - Emits OpenTelemetry `service.instance.id`.
  - Fallback order: explicit instance ID, `MemberSlotID`, `MemberID`, `local`.
- `cmd/scrapd/telemetry.go`
  - `scrapdTelemetryResourceConfig` now derives `InstanceID` from the stable
    Member slot first, then durable Member ID, then `local`.
  - Meter provider now has an SDK metric view for `scrap.rpc.server.duration`.
  - Explicit seconds-scaled boundaries:
    `0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10`.
  - The instrument still uses unit `s`, so the Prometheus-compatible export name
    remains `scrap_rpc_server_duration_seconds_bucket`.
- `internal/telemetry/resource_test.go`
  - Covers `service.instance.id` and all fallback cases.
- `cmd/scrapd/telemetry_test.go`
  - Covers `scrapd` instance identity derivation.
  - Collects from a manual OTel reader and asserts the duration bucket bounds.
- `docs/adr/0012-otel-evidence-plane.md`
  - Amended with metric identity and bucket-boundary decisions.
  - The earlier handoff suggested ADR 0013, but current repo docs show ADR 0012
    is the OTel evidence-plane decision. ADR 0013 is Raft trace-context specific.

## Verification already run

Local:

- `go test ./internal/telemetry ./cmd/scrapd ./internal/server` passed.
- `make test` passed.
- `make fmt-check` passed.
- `go tool -modfile=tools.go.mod golangci-lint run --timeout=5m ./cmd/scrapd ./internal/telemetry ./internal/server` passed.
- `make static` got through fmt, package-boundaries, and proto checks, then
  failed on pre-existing `internal/spike` lint debt unrelated to this patch:
  `errcheck`, `errorlint`, `gocognit`, `gosec`, and `govet`.

Live Kind stress cluster:

- Context was `kind-scrap-stress`.
- Built image from the worktree with:
  `make image IMAGE_NAME=localhost/scrapd:local GOFLAGS=-buildvcs=false`
  - `GOFLAGS=-buildvcs=false` was needed because Go VCS stamping failed inside
    the temporary git worktree; the Makefile still injects `buildSHA` via ldflags.
- Loaded image with:
  `kind load docker-image localhost/scrapd:local --name scrap-stress`
- Restarted and waited for `scrapd`:
  `kubectl -n scrap rollout restart statefulset/scrapd`
  `kubectl -n scrap rollout status statefulset/scrapd --timeout=180s`
- Opened Mimir port-forward during verification and closed it afterward:
  `kubectl -n monitoring port-forward svc/mimir 19009:9009`

Mimir results after waiting out old pre-restart samples:

- `target_info{job="scrapd",instance!=""}` showed `scrapd-0`, `scrapd-1`, and
  `scrapd-2` with patched build SHA `4e0d3e31e34796f123cde09dcb0e3cf4e3f845ab`.
- `count by (instance,rpc_grpc_status_code)(scrap_rpc_server_requests_total{rpc_method="WriteDocument"})`
  returned only instance-labeled fresh request series:
  - `instance="scrapd-0", status=13`
  - `instance="scrapd-0", status=8`
  - `instance="scrapd-2", status=14`
- `sum(rate(scrap_rpc_server_requests_total[5m]))` returned `0` at idle.
- `count by (le)(scrap_rpc_server_duration_seconds_bucket{rpc_method="WriteDocument",instance!=""})`
  showed the new bucket set: `0.001` through `10` plus `+Inf`.
- Idle p99 query returned `NaN` because there was no traffic, not the old fake
  `4.95`.

One short stress run was used only to force fresh post-fix request series:

`make stress STRESS_DURATION=15s STRESS_WORKERS=4 STRESS_DOC_SIZE=16384`

That run was **not** a healthy throughput proof. The existing cluster was under
upload pressure, and the run ended with all operations failed
(`upload_pressure`, plus a few `Unknown`, `deadline_exceeded`, and `unavailable`).

## Important gotchas

- No commit has been made.
- The implementation is in `/tmp/scrap-phantom-metrics`, not the original
  dirty checkout.
- `.worktrees/` is not gitignored in the repo, so the implementation was isolated
  under `/tmp` instead of an in-repo worktree.
- A full healthy loaded p99 verification still needs a clean/non-pressured stress
  environment. The metric bucket contract is verified live, but the cluster state
  prevented a meaningful throughput p99 run.
- Do not fix the unrelated `internal/spike` lint failures unless the user asks.

## Suggested skills

- `scrap-git-commit` — commit the current patch intentionally with a conventional
  message, likely `fix(telemetry): disambiguate scrapd metric series`.
- `scrap-yeet` — push the branch and open a draft PR against `main`.
- `scrap-gh-fix-ci` — use if GitHub checks fail after the PR is opened.
- `scrap-tdd` — use only if extending the fix or adding a fresh healthy loaded
  p99 regression harness.
