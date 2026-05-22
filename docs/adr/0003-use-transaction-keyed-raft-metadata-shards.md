# Use transaction-keyed Raft metadata shards

Status: accepted

S.C.R.A.P. shards by `tenant_id + transaction_id`, with one shard group represented by one Raft consensus group. This keeps all documents for a transaction local to one authority for visibility, `FindDocuments`, lifecycle, repair, upload outboxes, and transaction accounting.

The Raft log stores deterministic metadata commands only. Document bytes, full block indexes, and large payloads stay outside consensus and are replicated through a separate byte path, because embedding bytes in consensus would make write throughput, recovery, and snapshotting too expensive.
