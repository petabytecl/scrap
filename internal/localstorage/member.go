package localstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const localMemberStateFile = "local-member-state.json"

type localMemberState struct {
	Cordoned bool `json:"cordoned"`
	Draining bool `json:"draining"`
}

func (a *Application) CordonMember(ctx context.Context, req api.MemberMutationRequest) (*adminv1.StorageMember, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.StorageMember != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	if err := a.updateLocalMemberState(func(state *localMemberState) {
		state.Cordoned = true
	}); err != nil {
		return nil, err
	}
	return a.GetAdminMember(ctx, "local")
}

func (a *Application) UncordonMember(ctx context.Context, req api.MemberMutationRequest) (*adminv1.StorageMember, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.StorageMember != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	if err := a.updateLocalMemberState(func(state *localMemberState) {
		state.Cordoned = false
	}); err != nil {
		return nil, err
	}
	return a.GetAdminMember(ctx, "local")
}

func (a *Application) GetEvictionSafety(ctx context.Context, memberID string) (*adminv1.EvictionSafety, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if memberID != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	return &adminv1.EvictionSafety{
		StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
		SafeToEvict:   false,
		Warnings: []*adminv1.OperationWarning{
			{
				Code:    "SCRAP_SINGLE_MEMBER_LOCAL_MODE",
				Message: "local non-production mode has no alternate member to preserve byte availability during eviction",
				Target:  &adminv1.Target{Target: &adminv1.Target_StorageMember{StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"}}},
			},
		},
	}, nil
}

func (a *Application) requireWriteAdmission() error {
	state := a.currentLocalMemberState()
	if state.Draining {
		return status.Error(codes.FailedPrecondition, "local storage member is draining and cannot accept new writes")
	}
	if state.Cordoned {
		return status.Error(codes.FailedPrecondition, "local storage member is cordoned and cannot accept new writes")
	}
	return nil
}

func (a *Application) updateLocalMemberState(mutator func(*localMemberState)) error {
	a.memberMu.Lock()
	defer a.memberMu.Unlock()
	next := a.memberState
	mutator(&next)
	if err := writeLocalMemberState(a.dir, next); err != nil {
		return err
	}
	a.memberState = next
	return nil
}

func (a *Application) currentLocalMemberState() localMemberState {
	a.memberMu.Lock()
	defer a.memberMu.Unlock()
	return a.memberState
}

func readLocalMemberState(dir string) (localMemberState, error) {
	data, err := os.ReadFile(filepath.Join(dir, localMemberStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return localMemberState{}, nil
	}
	if err != nil {
		return localMemberState{}, err
	}
	var state localMemberState
	if err := json.Unmarshal(data, &state); err != nil {
		return localMemberState{}, err
	}
	return state, nil
}

func writeLocalMemberState(dir string, state localMemberState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "local-member-state-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, filepath.Join(dir, localMemberStateFile)); err != nil {
		return err
	}
	if err := syncLocalDir(dir); err != nil {
		return err
	}
	committed = true
	return nil
}

func syncLocalDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
