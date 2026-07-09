package shard

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"

	"go.etcd.io/raft/v3/raftpb"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestApplyEntryCommandRejectsUnknownCommand(t *testing.T) {
	s := &Shard{logger: slog.New(slog.DiscardHandler)}
	// A RaftCommand with no oneof set must fail closed (H-03 / ADR 0034).
	err := s.applyEntryCommand(&scrapv1.RaftCommand{}, 1)
	if err == nil {
		t.Fatal("applyEntryCommand() = nil, want unsupported command error")
	}
}

func TestPreflightProjectionDocCountRejectsMaxUint16(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(dir)
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	if err := idx.Put("tx-max", 1, math.MaxUint16, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	err = preflightProjectionDocCount(idx, "tx-max")
	if err == nil {
		t.Fatal("preflightProjectionDocCount() = nil, want max doc count error")
	}
}

func TestCommitProposalWaiterKeyPrefersProposalID(t *testing.T) {
	doc := &scrapv1.CommitDocument{
		TransactionId: "tx",
		DocumentName:  "a.xml",
		ProposalId:    "prop-1",
	}
	if got, want := commitProposalWaiterKey(doc), "commit-proposal\x00prop-1"; got != want {
		t.Fatalf("commitProposalWaiterKey() = %q, want %q", got, want)
	}
	doc.ProposalId = ""
	if got, want := commitProposalWaiterKey(doc), "tx\x00a.xml"; got != want {
		t.Fatalf("commitProposalWaiterKey() = %q, want %q", got, want)
	}
}

func TestValidateReplicationFenceRejectsStaleTerm(t *testing.T) {
	s := &Shard{
		raft: &fenceRaftStub{term: 5, leaderID: 2},
	}
	err := s.validateReplicationFenceLocked(&scrapv1.ReplicateDocumentInit{
		LeaderId: 1,
		Term:     4,
	})
	if !errors.Is(err, storeapi.ErrFailedPrecondition) {
		t.Fatalf("validateReplicationFenceLocked() = %v, want ErrFailedPrecondition", err)
	}
}

type fenceRaftStub struct {
	term     uint64
	leaderID uint64
}

func (f *fenceRaftStub) Propose(context.Context, []byte) error      { return nil }
func (f *fenceRaftStub) ReadIndex(context.Context) (uint64, error)  { return 0, nil }
func (f *fenceRaftStub) Step(context.Context, raftpb.Message) error { return nil }
func (f *fenceRaftStub) IsLeader() bool                             { return f.leaderID == 1 }
func (f *fenceRaftStub) LeaderID() uint64                           { return f.leaderID }
func (f *fenceRaftStub) Term() uint64                               { return f.term }
func (f *fenceRaftStub) AppliedIndex() uint64                       { return 0 }
func (f *fenceRaftStub) CommitIndex() uint64                        { return 0 }
func (f *fenceRaftStub) WithStableLeadership(fn func() error) error { return fn() }
func (f *fenceRaftStub) Stop()                                      {}
