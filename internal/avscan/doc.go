// Package avscan owns Content Scanner scheduling and engine boundaries.
//
// Content Scanner runs after Document ACK on sealed Blocks and reports bounded
// scan status. It is separate from Deep Scrub, which verifies Block integrity,
// and from Content Quarantine, whose authority belongs to Shard/Raft.
package avscan
