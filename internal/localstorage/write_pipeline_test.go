package localstorage

import (
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/appstatus"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestValidateWriteReplicationResultAcceptsLocalOnlyWrite(t *testing.T) {
	result := replication.Result{
		DesiredReplicaCount:  1,
		RequiredReplicaCount: 1,
		AchievedReplicaCount: 1,
	}

	testutil.RequireNoErrorf(t, validateWriteReplicationResult(result), "validate local-only result")
}

func TestValidateWriteReplicationResultAcceptsQuorumWrite(t *testing.T) {
	result := replication.Result{
		DesiredReplicaCount:  5,
		RequiredReplicaCount: 3,
		AchievedReplicaCount: 3,
		Receipts: []replication.Receipt{
			{MemberID: "peer-a"},
			{MemberID: "peer-b"},
		},
	}

	testutil.RequireNoErrorf(t, validateWriteReplicationResult(result), "validate quorum result")
}

func TestValidateWriteReplicationResultRejectsInsufficientQuorum(t *testing.T) {
	result := replication.Result{
		DesiredReplicaCount:  3,
		RequiredReplicaCount: 2,
		AchievedReplicaCount: 1,
	}

	requireWritePipelineInvariant(t, validateWriteReplicationResult(result))
}

func TestValidateWriteReplicationResultRejectsImpossibleReplicaCounts(t *testing.T) {
	result := replication.Result{
		DesiredReplicaCount:  2,
		RequiredReplicaCount: 1,
		AchievedReplicaCount: 3,
		Receipts: []replication.Receipt{
			{MemberID: "peer-a"},
			{MemberID: "peer-b"},
		},
	}

	requireWritePipelineInvariant(t, validateWriteReplicationResult(result))
}

func TestValidateWriteReplicationResultRejectsReceiptCountMismatch(t *testing.T) {
	result := replication.Result{
		DesiredReplicaCount:  5,
		RequiredReplicaCount: 3,
		AchievedReplicaCount: 3,
		Receipts: []replication.Receipt{
			{MemberID: "peer-a"},
		},
	}

	requireWritePipelineInvariant(t, validateWriteReplicationResult(result))
}

func TestValidateWriteReplicationResultRejectsInvalidReplicaBounds(t *testing.T) {
	cases := map[string]replication.Result{
		"missing desired local replica": {
			DesiredReplicaCount:  0,
			RequiredReplicaCount: 1,
			AchievedReplicaCount: 1,
		},
		"missing required local replica": {
			DesiredReplicaCount:  1,
			RequiredReplicaCount: 0,
			AchievedReplicaCount: 1,
		},
		"required exceeds desired": {
			DesiredReplicaCount:  1,
			RequiredReplicaCount: 2,
			AchievedReplicaCount: 2,
			Receipts: []replication.Receipt{
				{MemberID: "peer-a"},
			},
		},
		"missing achieved local replica": {
			DesiredReplicaCount:  1,
			RequiredReplicaCount: 1,
			AchievedReplicaCount: 0,
		},
	}

	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			requireWritePipelineInvariant(t, validateWriteReplicationResult(result))
		})
	}
}

func TestMapErrorMapsWritePipelineInvariant(t *testing.T) {
	err := mapError(ErrWritePipelineInvariant)

	var statusErr *appstatus.Error
	testutil.RequireTruef(t, errors.As(err, &statusErr), "mapped error type")
	testutil.RequireEqualf(t, appstatus.CodeInternal, statusErr.Code, "mapped error code")
	testutil.RequireTruef(t, errors.Is(err, ErrWritePipelineInvariant), "mapped error cause")
}

func requireWritePipelineInvariant(t testing.TB, err error) {
	t.Helper()
	if !errors.Is(err, ErrWritePipelineInvariant) {
		t.Fatalf("error = %v, want %v", err, ErrWritePipelineInvariant)
	}
}
