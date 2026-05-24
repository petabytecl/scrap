# Use Go as the primary implementation language

Status: accepted

S.C.R.A.P. is implemented as a single primary Go service. Go matches the team's main language, has mature gRPC/protobuf support, gives enough control over streaming IO and concurrency, and keeps the operational surface simpler than splitting the storage core across Go, Rust, Java, and .NET.

The default substrate is `go.etcd.io/raft/v3`, Pebble, `grpc-go`, native backend SDK adapters, the OpenBao Go API client, and Go standard crypto. Rust remains a possible later process-boundary helper only if a representative spike proves Go cannot meet a measured hot-path budget after one focused tuning pass.

## Observability substrate addendum

Initial service metrics use `github.com/prometheus/client_golang`, a service-local custom registry, and `promhttp` on `/metrics`. This satisfies the Kubernetes scrape path without introducing an OpenTelemetry collector/exporter dependency before the tracing and span-correlation requirements are stable. Metric labels must stay bounded; document IDs, backend object keys, tenants, and other high-cardinality values belong in logs or traces, not metric labels.
