package localstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/appstatus"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safepath"
	"github.com/petabytecl/scrap/internal/storageapp"
)

const localMemberStateFile = "local-member-state.json"

type localMemberState struct {
	Cordoned bool `json:"cordoned"`
	Draining bool `json:"draining"`
}

func (m *MemberApplication) CordonMember(ctx context.Context, req storageapp.MemberMutationRequest) (*adminv1.StorageMember, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.StorageMember != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	if err := m.app.updateLocalMemberState(func(state *localMemberState) {
		state.Cordoned = true
	}); err != nil {
		return nil, err
	}
	return Inspect(m.app).GetAdminMember(ctx, "local")
}

func (m *MemberApplication) UncordonMember(ctx context.Context, req storageapp.MemberMutationRequest) (*adminv1.StorageMember, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.StorageMember != "local" {
		return nil, mapError(metastore.ErrNotFound)
	}
	if err := m.app.updateLocalMemberState(func(state *localMemberState) {
		state.Cordoned = false
	}); err != nil {
		return nil, err
	}
	return Inspect(m.app).GetAdminMember(ctx, "local")
}

func (m *MemberApplication) GetEvictionSafety(ctx context.Context, memberID string) (*adminv1.EvictionSafety, error) {
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
		return appstatus.New(appstatus.CodeFailedPrecondition, "local storage member is draining and cannot accept new writes")
	}
	if state.Cordoned {
		return appstatus.New(appstatus.CodeFailedPrecondition, "local storage member is cordoned and cannot accept new writes")
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
	stats, err := Inspect(a).localDiskStats(ctx)
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

func unsafeCapacityError(requiredBytes, availableBytes uint64, warnings []string) error {
	return appstatus.New(appstatus.CodeResourceExhausted, "local capacity profile cannot admit write", appstatus.WithDetails(storageapp.UnsafeCapacityDetail{
		CapacityProfileID: localCapacityProfileID,
		RequiredBytes:     requiredBytes,
		AvailableBytes:    availableBytes,
		Warnings:          append([]string(nil), warnings...),
	}))
}

func saturatingAddUint64(left, right uint64) uint64 {
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
	a.memberMu.RLock()
	defer a.memberMu.RUnlock()
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
	temp, tempPath, err := createLocalMemberStateTemp(dir)
	if err != nil {
		return err
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

func createLocalMemberStateTemp(dir string) (*os.File, string, error) {
	temp, err := os.CreateTemp(dir, "local-member-state-*.tmp")
	if err != nil {
		return nil, "", err
	}
	tempPath, err := safepath.UnderDir(dir, temp.Name())
	if err != nil {
		return nil, "", errors.Join(err, temp.Close(), removeLocalPath(dir, temp.Name()))
	}
	return temp, tempPath, nil
}

func syncLocalDir(path string) error {
	// #nosec G304 G703 -- callers pass configured local storage directories.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func removeLocalPath(root, path string) error {
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
