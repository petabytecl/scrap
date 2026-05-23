package localstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/api"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safepath"
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

func (a *Application) requireWriteAdmission(ctx context.Context, expectedLength *uint64) error {
	state := a.currentLocalMemberState()
	if state.Draining {
		return status.Error(codes.FailedPrecondition, "local storage member is draining and cannot accept new writes")
	}
	if state.Cordoned {
		return status.Error(codes.FailedPrecondition, "local storage member is cordoned and cannot accept new writes")
	}
	return a.requireCapacityAdmission(ctx, expectedLength)
}

func (a *Application) requireCapacityAdmission(ctx context.Context, expectedLength *uint64) error {
	required := a.minUsableBytesAfterWrite
	if expectedLength != nil {
		required = saturatingAddUint64(required, *expectedLength)
	}
	if required == 0 {
		return nil
	}
	stats, err := a.localDiskStats(ctx)
	if err != nil {
		return err
	}
	if stats.usableBytesRemaining >= required {
		return nil
	}
	return unsafeCapacityError(required, stats.usableBytesRemaining, []string{
		"SCRAP_LOCAL_CAPACITY_GUARD",
		"SCRAP_NON_PRODUCTION_CAPACITY_PROFILE",
	})
}

func unsafeCapacityError(requiredBytes uint64, availableBytes uint64, warnings []string) error {
	st := status.New(codes.ResourceExhausted, "local capacity profile cannot admit write")
	withDetails, err := st.WithDetails(&scrapv1.UnsafeCapacityDetail{
		CapacityProfileId: localCapacityProfileID,
		RequiredBytes:     requiredBytes,
		AvailableBytes:    availableBytes,
		Warnings:          append([]string(nil), warnings...),
	})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func saturatingAddUint64(left uint64, right uint64) uint64 {
	if right > ^uint64(0)-left {
		return ^uint64(0)
	}
	return left + right
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
	statePath, err := safepath.UnderDir(dir, filepath.Join(dir, localMemberStateFile))
	if err != nil {
		return localMemberState{}, err
	}
	data, err := readLocalPath(statePath)
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
	tempPath, err := safepath.UnderDir(dir, temp.Name())
	if err != nil {
		return errors.Join(err, temp.Close(), removeLocalPath(dir, temp.Name()))
	}
	statePath, err := safepath.UnderDir(dir, filepath.Join(dir, localMemberStateFile))
	if err != nil {
		return errors.Join(err, temp.Close(), removeLocalPath(dir, tempPath))
	}
	committed := false
	defer func() {
		if !committed {
			_ = removeLocalPath(dir, tempPath)
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
	// #nosec G703 -- source and destination are validated under the local member directory.
	if err := os.Rename(tempPath, statePath); err != nil {
		return err
	}
	if err := syncLocalDir(dir); err != nil {
		return err
	}
	committed = true
	return nil
}

func syncLocalDir(path string) error {
	// #nosec G304 G703 -- callers pass configured local storage directories.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func removeLocalPath(root string, path string) error {
	path, err := safepath.UnderDir(root, path)
	if err != nil {
		return err
	}
	// #nosec G703 -- path is validated under the local storage directory before removal.
	return os.Remove(path)
}

func readLocalPath(path string) ([]byte, error) {
	// #nosec G304 G703 -- callers validate paths under the local storage directory.
	return os.ReadFile(path)
}

func openLocalFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	// #nosec G304 G703 -- callers validate paths under the local storage directory.
	return os.OpenFile(path, flag, perm)
}
