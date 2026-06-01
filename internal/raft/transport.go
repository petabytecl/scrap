package raft

import (
	"go.etcd.io/raft/v3/raftpb"
)

type Transport interface {
	Send(msgs []raftpb.Message)
}
