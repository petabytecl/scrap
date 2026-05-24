# Write Pipeline Performance Evidence

Issue #148 uses this local evidence harness to compare write-path behavior
before and after group-commit changes. The harness is intentionally local and
bounded: it opens an in-process `localstorage.Application`, writes synthetic
documents through `WriteDocument`, and records ACK latency and throughput.

This evidence is a regression and performance-smoke signal. It is not
production capacity approval, and it does not exercise kind, LocalStack,
OpenBao, backend upload, multi-member placement, or live deployment topology.

## Capture A Baseline

Run the harness on the baseline commit:

```sh
git switch main
git pull --ff-only
make write-pipeline-evidence WRITE_PIPELINE_REPORT=write-pipeline-baseline.json
```

Record the commit SHA, dirty-tree state, workload shape, throughput, and
ACK-latency p50/p95/p99 from the JSON report.

## Capture A Candidate

Run the same workload on the candidate branch:

```sh
git switch <candidate-branch>
make write-pipeline-evidence WRITE_PIPELINE_REPORT=write-pipeline-candidate.json
```

Use the same `WRITE_PIPELINE_SAMPLES`, `WRITE_PIPELINE_CONCURRENCY`,
`WRITE_PIPELINE_DOCUMENT_SIZE`, and `WRITE_PIPELINE_DURATION` values for both
runs. If the candidate introduces threshold requirements, pass them explicitly:

```sh
make write-pipeline-evidence \
  WRITE_PIPELINE_REPORT=write-pipeline-candidate.json \
  WRITE_PIPELINE_MIN_WRITES_PER_SECOND=500 \
  WRITE_PIPELINE_MAX_P99_ACK_LATENCY=25ms
```

The command exits non-zero when configured thresholds are missed, but it still
writes the JSON report so the failure can be attached to #148 or a blocking
follow-up issue.

## Report Contents

The report includes:

- release SHA and dirty-tree state;
- runner ID, sample count, concurrency, document size, and max duration;
- total successful writes and bytes;
- writes/sec and bytes/sec;
- ACK latency min, average, p50, p95, p99, and max;
- threshold settings and violations;
- observed component signals for block append sync latency, block append queue
  depth, block sync batch size, metadata command sync latency, metadata command
  batch size, and raft queue depth.

#178 adds blockstore split metrics through Prometheus. #179 adds metadata
command-log sync and batch-size metrics. The local in-process harness gathers
those Prometheus metrics at the end of the run and samples queue-depth gauges
while the workload is active, so the report shows actual counts, sums,
averages, current values, and max observed queue depth instead of only metric
availability.

## Interpreting Local Fsync Evidence

The local runner may have very low `fsync` cost because of filesystem,
virtualization, cache, or temporary-directory behavior. In that profile,
artificial batch linger can dominate ACK latency even when the implementation is
correct. Treat the component sync-latency averages as part of the result: if
durable sync latency is already below the configured linger, the evidence runner
is measuring the linger policy more than a real fsync bottleneck.

For #148, use a target-load run that is large enough to exercise concurrency,
for example:

```sh
make write-pipeline-evidence \
  WRITE_PIPELINE_REPORT=write-pipeline-target-load.json \
  WRITE_PIPELINE_SAMPLES=1024 \
  WRITE_PIPELINE_CONCURRENCY=128 \
  WRITE_PIPELINE_DOCUMENT_SIZE=4096 \
  WRITE_PIPELINE_DURATION=30s \
  WRITE_PIPELINE_MIN_WRITES_PER_SECOND=500 \
  WRITE_PIPELINE_MAX_P99_ACK_LATENCY=25ms
```

The default 128-sample run remains useful as a quick smoke test, but parent
performance claims should cite the larger target-load profile plus the observed
component signals.
