package writepath

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
)

const raftDiskLogFile = "raft-wal.jsonl"

var raftDiskLogCRCTable = crc32.MakeTable(crc32.Castagnoli)

type raftMutableStorage interface {
	raft.Storage
	ApplySnapshot(pb.Snapshot) error
	SetHardState(pb.HardState) error
	Append([]pb.Entry) error
}

type raftStorageWithConfState struct {
	memory    *raft.MemoryStorage
	confState pb.ConfState
}

func newRaftStorageWithConfState(voters ...uint64) *raftStorageWithConfState {
	return &raftStorageWithConfState{
		memory:    raft.NewMemoryStorage(),
		confState: pb.ConfState{Voters: voters},
	}
}

func (s *raftStorageWithConfState) InitialState() (pb.HardState, pb.ConfState, error) {
	hardState, confState, err := s.memory.InitialState()
	if err != nil {
		return pb.HardState{}, pb.ConfState{}, err
	}
	if len(confState.Voters) == 0 && len(confState.Learners) == 0 {
		confState = s.confState
	}
	return hardState, confState, nil
}

func (s *raftStorageWithConfState) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	return s.memory.Entries(lo, hi, maxSize)
}

func (s *raftStorageWithConfState) Term(i uint64) (uint64, error) {
	return s.memory.Term(i)
}

func (s *raftStorageWithConfState) LastIndex() (uint64, error) {
	return s.memory.LastIndex()
}

func (s *raftStorageWithConfState) FirstIndex() (uint64, error) {
	return s.memory.FirstIndex()
}

func (s *raftStorageWithConfState) Snapshot() (pb.Snapshot, error) {
	return s.memory.Snapshot()
}

func (s *raftStorageWithConfState) ApplySnapshot(snapshot pb.Snapshot) error {
	return s.memory.ApplySnapshot(snapshot)
}

func (s *raftStorageWithConfState) SetHardState(hardState pb.HardState) error {
	return s.memory.SetHardState(hardState)
}

func (s *raftStorageWithConfState) Append(entries []pb.Entry) error {
	return s.memory.Append(entries)
}

type raftDiskLog struct {
	file       *os.File
	storage    *raftStorageWithConfState
	hasEntries bool
	nextID     uint64
	applied    int
	appliedIDs map[uint64]struct{}
	records    map[string]DocumentRecord
}

type raftDiskRecord struct {
	Type      string       `json:"type"`
	HardState pb.HardState `json:"hard_state"`
	Entry     pb.Entry     `json:"entry"`
	Snapshot  pb.Snapshot  `json:"snapshot"`
	CRC32C    string       `json:"crc32c"`
}

func openRaftDiskLog(dir string) (*raftDiskLog, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, raftDiskLogFile)
	storage := newRaftStorageWithConfState(1)
	log := &raftDiskLog{
		storage:    storage,
		appliedIDs: make(map[uint64]struct{}),
		records:    make(map[string]DocumentRecord),
	}

	hardState, entries, err := readRaftDiskRecords(path)
	if err != nil {
		return nil, err
	}
	if !raft.IsEmptyHardState(hardState) {
		if err := storage.SetHardState(hardState); err != nil {
			return nil, err
		}
	}
	if len(entries) > 0 {
		if err := storage.Append(entries); err != nil {
			return nil, err
		}
		log.hasEntries = true
		log.replayCommitted(entries, hardState.Commit)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		_ = file.Close()
		return nil, err
	}
	log.file = file
	return log, nil
}

func readRaftDiskRecords(path string) (pb.HardState, []pb.Entry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return pb.HardState{}, nil, nil
	}
	if err != nil {
		return pb.HardState{}, nil, err
	}
	defer file.Close()

	var hardState pb.HardState
	var entries []pb.Entry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record raftDiskRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return pb.HardState{}, nil, err
		}
		if err := validateRaftDiskRecord(record); err != nil {
			return pb.HardState{}, nil, err
		}
		switch record.Type {
		case "hard_state":
			hardState = record.HardState
		case "entry":
			entries = append(entries, record.Entry)
		case "snapshot":
			// Snapshots are recorded for format completeness but not produced by
			// the current spike harness.
		default:
			return pb.HardState{}, nil, errors.New("unknown raft disk record type")
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return pb.HardState{}, nil, err
	}
	return hardState, entries, nil
}

func (l *raftDiskLog) SaveReady(ready raft.Ready) error {
	encoder := json.NewEncoder(l.file)
	if !raft.IsEmptyHardState(ready.HardState) {
		if err := encodeRaftDiskRecord(encoder, raftDiskRecord{
			Type:      "hard_state",
			HardState: ready.HardState,
		}); err != nil {
			return err
		}
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := encodeRaftDiskRecord(encoder, raftDiskRecord{
			Type:     "snapshot",
			Snapshot: ready.Snapshot,
		}); err != nil {
			return err
		}
	}
	for _, entry := range ready.Entries {
		if err := encodeRaftDiskRecord(encoder, raftDiskRecord{
			Type:  "entry",
			Entry: entry,
		}); err != nil {
			return err
		}
	}
	return l.file.Sync()
}

func encodeRaftDiskRecord(encoder *json.Encoder, record raftDiskRecord) error {
	checksum, err := checksumRaftDiskRecord(record)
	if err != nil {
		return err
	}
	record.CRC32C = checksum
	return encoder.Encode(record)
}

func validateRaftDiskRecord(record raftDiskRecord) error {
	if record.CRC32C == "" {
		return errors.New("raft disk record missing crc32c")
	}
	want, err := checksumRaftDiskRecord(record)
	if err != nil {
		return err
	}
	if record.CRC32C != want {
		return fmt.Errorf("raft disk record crc32c mismatch: got %s want %s", record.CRC32C, want)
	}
	return nil
}

func checksumRaftDiskRecord(record raftDiskRecord) (string, error) {
	record.CRC32C = ""
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", crc32.Checksum(payload, raftDiskLogCRCTable)), nil
}

func (l *raftDiskLog) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *raftDiskLog) replayCommitted(entries []pb.Entry, commitIndex uint64) {
	for _, entry := range entries {
		if entry.Index > commitIndex || entry.Type != pb.EntryNormal || len(entry.Data) == 0 {
			continue
		}
		var command raftCommitCommand
		if err := json.Unmarshal(entry.Data, &command); err != nil || command.ID == 0 {
			continue
		}
		if _, ok := l.appliedIDs[command.ID]; ok {
			continue
		}
		l.appliedIDs[command.ID] = struct{}{}
		l.applied++
		if command.ID > l.nextID {
			l.nextID = command.ID
		}
		l.records[committedDocumentRecordKey(command.Record)] = command.Record
	}
}
