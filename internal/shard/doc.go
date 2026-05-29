// Package shard implements the per-shard storage orchestrator: the write path
// with block and index writers, the Raft apply handler, projection rebuild,
// light and deep scrub coordination, backend upload, and the associated
// telemetry.
package shard
