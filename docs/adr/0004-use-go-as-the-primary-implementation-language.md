# Use Go as the primary implementation language

Status: accepted

S.C.R.A.P. is implemented as a single primary Go service. Go matches the team's main language, has mature gRPC/protobuf support, gives enough control over streaming IO and concurrency, and keeps the operational surface simpler than splitting the storage core across Go, Rust, Java, and .NET.

The default substrate is `go.etcd.io/raft/v3`, Pebble, `grpc-go`, native backend SDK adapters, the OpenBao Go API client, and Go standard crypto. Rust remains a possible later process-boundary helper only if a representative spike proves Go cannot meet a measured hot-path budget after one focused tuning pass.
