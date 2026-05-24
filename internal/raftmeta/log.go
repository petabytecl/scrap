package raftmeta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/petabytecl/scrap/internal/closeutil"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/safepath"
)

const (
	commandLogName      = "shard-command.log"
	commandLogHeaderLen = 12
	commandLogCRCLen    = 4
	MaxCommandBytes     = 16 * 1024 * 1024
	commandLogFilePerm  = 0o600
)

var commandLogCRCTable = crc32.MakeTable(crc32.Castagnoli)

type Entry struct {
	Index   uint64
	Command *metastorev1.ShardCommand
}

type Log struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	nextIndex uint64
}

func OpenLog(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path, err := safepath.UnderDir(dir, filepath.Join(dir, commandLogName))
	if err != nil {
		return nil, err
	}
	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	file, err := openLogFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		if err := syncDir(dir); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	nextIndex := uint64(1)
	if len(entries) > 0 {
		nextIndex = entries[len(entries)-1].Index + 1
	}
	return &Log{path: path, file: file, nextIndex: nextIndex}, nil
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Log) Append(command *metastorev1.ShardCommand) (Entry, error) {
	payload, err := metastore.MarshalShardCommand(command)
	if err != nil {
		return Entry{}, err
	}
	if len(payload) > MaxCommandBytes {
		return Entry{}, fmt.Errorf("raftmeta: command is %d bytes; maximum is %d", len(payload), MaxCommandBytes)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return Entry{}, errors.New("raftmeta: log is closed")
	}
	index := l.nextIndex
	frame, err := encodeFrame(index, payload)
	if err != nil {
		return Entry{}, err
	}
	written, err := l.file.Write(frame)
	if err != nil {
		return Entry{}, err
	}
	if written != len(frame) {
		return Entry{}, io.ErrShortWrite
	}
	if err := l.file.Sync(); err != nil {
		return Entry{}, err
	}
	l.nextIndex++
	return Entry{Index: index, Command: command}, nil
}

func (l *Log) Replay() ([]Entry, error) {
	return readEntries(l.path)
}

func (l *Log) LastIndex() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextIndex == 0 {
		return 0
	}
	return l.nextIndex - 1
}

func (l *Log) EnsureNextIndex(nextIndex uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextIndex < nextIndex {
		l.nextIndex = nextIndex
	}
}

func (l *Log) Compact(throughIndex uint64) error {
	if throughIndex == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("raftmeta: log is closed")
	}
	entries, err := readEntries(l.path)
	if err != nil {
		return err
	}
	tail := entries[:0]
	for _, entry := range entries {
		if entry.Index > throughIndex {
			tail = append(tail, entry)
		}
	}
	dir := filepath.Dir(l.path)
	tempPath, err := safepath.UnderDir(dir, l.path+".compact")
	if err != nil {
		return err
	}
	if err := writeEntries(tempPath, tail); err != nil {
		return err
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	// #nosec G703 -- source and destination are validated under the configured raft directory.
	if err := os.Rename(tempPath, l.path); err != nil {
		if file, openErr := openLogFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND); openErr == nil {
			l.file = file
		}
		return err
	}
	file, err := openLogFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND)
	if err != nil {
		return err
	}
	l.file = file
	if err := syncDir(dir); err != nil {
		return err
	}
	if len(tail) > 0 {
		l.nextIndex = tail[len(tail)-1].Index + 1
	} else if l.nextIndex <= throughIndex {
		l.nextIndex = throughIndex + 1
	}
	return nil
}

func readEntries(path string) ([]Entry, error) {
	file, err := openLogReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(file)

	var entries []Entry
	expectedIndex := uint64(0)
	for {
		entry, nextIndex, done, err := readEntry(file, expectedIndex)
		if err != nil {
			return nil, err
		}
		if done {
			return entries, nil
		}
		entries = append(entries, entry)
		expectedIndex = nextIndex
	}
}

func readEntry(file *os.File, expectedIndex uint64) (Entry, uint64, bool, error) {
	var header [commandLogHeaderLen]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return Entry{}, expectedIndex, true, nil
		}
		return Entry{}, expectedIndex, false, err
	}
	index, err := validateEntryHeader(header, expectedIndex)
	if err != nil {
		return Entry{}, expectedIndex, false, err
	}
	payload, err := readEntryPayload(file, header, index)
	if err != nil {
		return Entry{}, expectedIndex, false, err
	}
	command, err := metastore.UnmarshalShardCommand(payload)
	if err != nil {
		return Entry{}, expectedIndex, false, err
	}
	return Entry{Index: index, Command: command}, index + 1, false, nil
}

func validateEntryHeader(header [commandLogHeaderLen]byte, expectedIndex uint64) (uint64, error) {
	index := binary.BigEndian.Uint64(header[0:8])
	if index == 0 {
		return 0, errors.New("raftmeta: command index 0 is invalid")
	}
	if expectedIndex == 0 {
		return index, nil
	}
	if index != expectedIndex {
		return 0, fmt.Errorf("raftmeta: command index %d after %d", index, expectedIndex-1)
	}
	return index, nil
}

func readEntryPayload(file *os.File, header [commandLogHeaderLen]byte, index uint64) ([]byte, error) {
	length := binary.BigEndian.Uint32(header[8:12])
	if length > MaxCommandBytes {
		return nil, fmt.Errorf("raftmeta: command length %d exceeds maximum %d", length, MaxCommandBytes)
	}
	payloadLength, err := safeconv.Uint64ToInt("raftmeta: command length", uint64(length))
	if err != nil {
		return nil, err
	}
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, err
	}
	var checksum [commandLogCRCLen]byte
	if _, err := io.ReadFull(file, checksum[:]); err != nil {
		return nil, err
	}
	want := binary.BigEndian.Uint32(checksum[:])
	if got := checksumFrame(header[:], payload); got != want {
		return nil, fmt.Errorf("raftmeta: command log checksum mismatch at index %d", index)
	}
	return payload, nil
}

func writeEntries(path string, entries []Entry) error {
	file, err := openLogFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		payload, err := metastore.MarshalShardCommand(entry.Command)
		if err != nil {
			_ = file.Close()
			return err
		}
		frame, err := encodeFrame(entry.Index, payload)
		if err != nil {
			_ = file.Close()
			return err
		}
		if _, err := file.Write(frame); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func encodeFrame(index uint64, payload []byte) ([]byte, error) {
	if len(payload) > MaxCommandBytes {
		return nil, fmt.Errorf("raftmeta: command is %d bytes; maximum is %d", len(payload), MaxCommandBytes)
	}
	payloadLen, err := safeconv.IntToUint32("raft command payload length", len(payload))
	if err != nil {
		return nil, err
	}
	frame := make([]byte, commandLogHeaderLen+len(payload)+commandLogCRCLen)
	binary.BigEndian.PutUint64(frame[0:8], index)
	binary.BigEndian.PutUint32(frame[8:12], payloadLen)
	copy(frame[commandLogHeaderLen:], payload)
	crc := checksumFrame(frame[:commandLogHeaderLen], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-commandLogCRCLen:], crc)
	return frame, nil
}

func checksumFrame(header, payload []byte) uint32 {
	crc := crc32.New(commandLogCRCTable)
	_, _ = crc.Write(header)
	_, _ = crc.Write(payload)
	return crc.Sum32()
}

func syncDir(path string) error {
	// #nosec G304 G703 -- callers pass configured raft metadata directories.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func openLogReadFile(path string) (*os.File, error) {
	// #nosec G304 G703 -- raft metadata log paths are validated under the configured raft directory.
	return os.Open(path)
}

func openLogFile(path string, flag int) (*os.File, error) {
	// #nosec G304 G703 -- raft metadata log paths are validated under the configured raft directory.
	return os.OpenFile(path, flag, commandLogFilePerm)
}
