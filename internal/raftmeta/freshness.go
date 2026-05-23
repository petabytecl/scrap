package raftmeta

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotLeader                = errors.New("raftmeta: local member is not leader")
	ErrQuorumUnavailable        = errors.New("raftmeta: quorum unavailable")
	ErrReadFreshnessUnavailable = errors.New("raftmeta: read freshness cannot be proven")
	ErrLeaseReadDisabled        = errors.New("raftmeta: lease reads are disabled")
)

type FreshnessCheck struct {
	ShardID       string
	LocalMemberID string
	AppliedIndex  uint64
	Members       []Member
}

type FreshnessChecker interface {
	RequireWriteQuorum(context.Context, FreshnessCheck) error
	RequireReadIndex(context.Context, FreshnessCheck) error
}

type singleVoterFreshnessChecker struct{}

func (singleVoterFreshnessChecker) RequireWriteQuorum(ctx context.Context, check FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	voter, ok := singleVoter(check.Members)
	if !ok {
		return fmt.Errorf("%w: shard %s requires an explicit quorum checker", ErrQuorumUnavailable, check.ShardID)
	}
	if check.LocalMemberID != voter.MemberID {
		return fmt.Errorf("%w: local member %q is not voter %q", ErrNotLeader, check.LocalMemberID, voter.MemberID)
	}
	return nil
}

func (singleVoterFreshnessChecker) RequireReadIndex(ctx context.Context, check FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	voter, ok := singleVoter(check.Members)
	if !ok {
		return fmt.Errorf("%w: shard %s requires an explicit ReadIndex checker", ErrReadFreshnessUnavailable, check.ShardID)
	}
	if check.LocalMemberID != voter.MemberID {
		return fmt.Errorf("%w: local member %q is not voter %q", ErrNotLeader, check.LocalMemberID, voter.MemberID)
	}
	return nil
}

func singleVoter(members []Member) (Member, bool) {
	var voter Member
	voters := 0
	for _, member := range members {
		if !member.IsVoter() {
			continue
		}
		voter = member
		voters++
	}
	return voter, voters == 1
}
