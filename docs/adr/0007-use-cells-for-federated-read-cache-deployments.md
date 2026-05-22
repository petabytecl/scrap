# Use cells for federated read-cache deployments

Status: accepted

A cell is a S.C.R.A.P. deployment that owns a disjoint `source_namespace` for writes. Other cells may import its published metadata snapshots and tails read-only to build bounded-staleness local catalogs and byte caches, but they do not join the source cell's shard consensus or mutate source metadata.

The term `shard` remains reserved for transaction-keyed Raft groups inside a cell. This avoids overloading shard to mean both consensus partition and Kubernetes deployment. Cross-cell conflicts fail closed, and authority movement is an explicit out-of-band migration workflow.
