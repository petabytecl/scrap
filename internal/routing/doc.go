// Package routing owns the Shard routing boundary for fixed hash slots.
//
// It maps Transaction identifiers to slots, validates slot-to-Shard placement,
// returns route metadata, and emits bounded lookup records. It does not own
// Raft apply, gRPC status mapping, Backend I/O, public request validation, or
// Shard lifecycle.
package routing
