package raftmeta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

const (
	snapshotName      = "shard-snapshot.snap"
	snapshotHeaderLen = 4
	snapshotCRCLen    = 4
	MaxSnapshotBytes  = 64 * 1024 * 1024
)

var errSnapshotNotFound = errors.New("raftmeta: snapshot not found")

type Member struct {
	RaftID   uint64
	MemberID string
	Role     metastorev1.MembershipRole
}

func (m Member) IsVoter() bool {
	return m.Role == metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER
}

type SnapshotInfo struct {
	LastIndex    uint64
	Path         string
	Documents    int
	Transactions int
	UploadJobs   int
	Repairs      int
	Members      int
	Commands     int
}

func snapshotPath(dir string) string {
	return filepath.Join(dir, snapshotName)
}

func writeSnapshotFile(dir string, snapshot *metastorev1.ShardSnapshot) error {
	payload, err := metastore.MarshalShardSnapshot(snapshot)
	if err != nil {
		return err
	}
	if len(payload) > MaxSnapshotBytes {
		return fmt.Errorf("raftmeta: snapshot is %d bytes; maximum is %d", len(payload), MaxSnapshotBytes)
	}
	frame := make([]byte, snapshotHeaderLen+len(payload)+snapshotCRCLen)
	binary.BigEndian.PutUint32(frame[:snapshotHeaderLen], uint32(len(payload)))
	copy(frame[snapshotHeaderLen:], payload)
	crc := checksumFrame(frame[:snapshotHeaderLen], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-snapshotCRCLen:], crc)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := snapshotPath(dir)
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(frame); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func readSnapshotFile(dir string) (*metastorev1.ShardSnapshot, error) {
	file, err := os.Open(snapshotPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errSnapshotNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var header [snapshotHeaderLen]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > MaxSnapshotBytes {
		return nil, fmt.Errorf("raftmeta: snapshot length %d exceeds maximum %d", length, MaxSnapshotBytes)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, err
	}
	var checksum [snapshotCRCLen]byte
	if _, err := io.ReadFull(file, checksum[:]); err != nil {
		return nil, err
	}
	want := binary.BigEndian.Uint32(checksum[:])
	if got := checksumFrame(header[:], payload); got != want {
		return nil, errors.New("raftmeta: snapshot checksum mismatch")
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	} else if n != 0 {
		return nil, errors.New("raftmeta: snapshot has trailing data")
	}
	return metastore.UnmarshalShardSnapshot(payload)
}

func snapshotInfo(dir string, snapshot *metastorev1.ShardSnapshot) SnapshotInfo {
	if snapshot == nil {
		return SnapshotInfo{}
	}
	return SnapshotInfo{
		LastIndex:    snapshot.GetLastIndex(),
		Path:         snapshotPath(dir),
		Documents:    len(snapshot.GetDocuments()),
		Transactions: len(snapshot.GetTransactions()),
		UploadJobs:   len(snapshot.GetUploadIntents()),
		Repairs:      len(snapshot.GetRepairStates()),
		Members:      len(snapshot.GetMembership().GetMembers()),
		Commands:     len(snapshot.GetCommandReceipts()),
	}
}

func membershipToProto(members []Member) *metastorev1.MembershipState {
	out := &metastorev1.MembershipState{
		Members: make([]*metastorev1.MembershipMember, 0, len(members)),
	}
	for _, member := range members {
		out.Members = append(out.Members, &metastorev1.MembershipMember{
			RaftId:   member.RaftID,
			MemberId: member.MemberID,
			Role:     member.Role,
		})
	}
	return out
}

func membershipFromProto(state *metastorev1.MembershipState) []Member {
	if state == nil {
		return nil
	}
	members := make([]Member, 0, len(state.GetMembers()))
	for _, member := range state.GetMembers() {
		members = append(members, Member{
			RaftID:   member.GetRaftId(),
			MemberID: member.GetMemberId(),
			Role:     member.GetRole(),
		})
	}
	return members
}

func defaultMembers() []Member {
	return []Member{
		{
			RaftID:   1,
			MemberID: "local",
			Role:     metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER,
		},
	}
}

func normalizeMembers(members []Member) []Member {
	if len(members) == 0 {
		return defaultMembers()
	}
	out := append([]Member(nil), members...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].RaftID < out[j].RaftID
	})
	return out
}

func defaultLocalMemberID(members []Member) string {
	for _, member := range members {
		if member.IsVoter() {
			return member.MemberID
		}
	}
	if len(members) > 0 {
		return members[0].MemberID
	}
	return "local"
}
