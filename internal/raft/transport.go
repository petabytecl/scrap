package raft

import (
	"go.etcd.io/raft/v3/raftpb"
)

type Transport interface {
	Send(msgs []raftpb.Message)
}

// StatusReporter receives message-delivery outcomes from a Transport. The
// raft state machine needs them: after sending a MsgSnap the leader parks the
// follower's Progress in StateSnapshot and waits for a report — a transport
// that drops the message without reporting strands the follower there and
// the cell silently runs one durable replica short (#462).
type StatusReporter interface {
	// ReportUnreachable tells raft the peer could not be reached; the leader
	// backs off to probing instead of streaming into a dead link.
	ReportUnreachable(id uint64)
	// ReportSnapshotFailure tells raft a MsgSnap to the peer was dropped or
	// failed to send, so the leader retries instead of waiting forever.
	ReportSnapshotFailure(id uint64)
	// ReportSnapshotSuccess tells raft a MsgSnap was handed to the peer; the
	// leader resumes probing from the snapshot index.
	ReportSnapshotSuccess(id uint64)
}

// ReporterSink is implemented by Transports that can report send outcomes.
// Open hands the node's StatusReporter to such transports.
type ReporterSink interface {
	SetStatusReporter(r StatusReporter)
}
