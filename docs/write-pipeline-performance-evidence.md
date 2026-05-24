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
- component signal availability for block append sync latency, block append
  queue depth, block sync batch size, metadata command sync latency, metadata
  command batch size, and raft queue depth.

#178 adds blockstore split metrics through Prometheus. #179 adds metadata
command-log sync and batch-size metrics. This local in-process harness records
which component signals exist but does not scrape Prometheus.
