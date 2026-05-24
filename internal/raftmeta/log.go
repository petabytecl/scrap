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
	"time"

	"github.com/petabytecl/scrap/internal/closeutil"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/observe"
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
	mu          sync.Mutex
	path        string
	file        *os.File
	nextIndex   uint64
	syncLogFile func() error
	failureErr  error
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
	log := &Log{path: path, file: file, nextIndex: nextIndex}
	log.syncLogFile = log.defaultSyncLogFile
	return log, nil
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

// CheckHealth reports fail-closed command-log state without retrying ambiguous fsync failures.
func (l *Log) CheckHealth() error {
	if l == nil {
		return errors.New("raftmeta: command log is closed")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("raftmeta: command log is closed")
	}
	if l.failureErr != nil {
		return fmt.Errorf("raftmeta: command log is fail-closed; restart required: %w", l.failureErr)
	}
	return nil
}

func (l *Log) Append(command *metastorev1.ShardCommand) (Entry, error) {
	entries, err := l.AppendBatch([]*metastorev1.ShardCommand{command})
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, errors.New("raftmeta: no command appended")
	}
	return entries[0], nil
}

func (l *Log) AppendBatch(commands []*metastorev1.ShardCommand) ([]Entry, error) {
	payloads, err := marshalCommandBatch(commands)
	if err != nil {
		return nil, err
	}
	if len(payloads) == 0 {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil, errors.New("raftmeta: log is closed")
	}
	if l.failureErr != nil {
		return nil, l.failureErr
	}
	entries, frames, err := l.buildFramesLocked(commands, payloads)
	if err != nil {
		return nil, err
	}
	if err := l.writeFrames(frames); err != nil {
		return nil, l.recordFailureLocked("write", err)
	}
	start := time.Now()
	err = l.syncLogFile()
	outcome := observe.OutcomeSuccess
	if err != nil {
		outcome = observe.OutcomeError
	}
	observe.RecordMetadataCommandSyncLatency(outcome, time.Since(start))
	if err != nil {
		observe.RecordBatchFailure(observe.BatchComponentRaftMeta, observe.BatchStageSync)
		return nil, l.recordFailureLocked("sync", err)
	}
	observe.RecordMetadataCommandBatchSize(len(entries))
	nextIndex, err := addLogIndex(l.nextIndex, len(entries))
	if err != nil {
		return nil, l.recordFailureLocked("index advance", err)
	}
	l.nextIndex = nextIndex
	return entries, nil
}

func marshalCommandBatch(commands []*metastorev1.ShardCommand) ([][]byte, error) {
	payloads := make([][]byte, 0, len(commands))
	for _, command := range commands {
		payload, err := metastore.MarshalShardCommand(command)
		if err != nil {
			return nil, err
		}
		if len(payload) > MaxCommandBytes {
			return nil, fmt.Errorf("raftmeta: command is %d bytes; maximum is %d", len(payload), MaxCommandBytes)
		}
		payloads = append(payloads, payload)
	}
	return payloads, nil
}

func (l *Log) buildFramesLocked(commands []*metastorev1.ShardCommand, payloads [][]byte) ([]Entry, [][]byte, error) {
	entries := make([]Entry, 0, len(payloads))
	frames := make([][]byte, 0, len(payloads))
	for index, payload := range payloads {
		logIndex, err := addLogIndex(l.nextIndex, index)
		if err != nil {
			return nil, nil, err
		}
		frame, err := encodeFrame(logIndex, payload)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, Entry{Index: logIndex, Command: commands[index]})
		frames = append(frames, frame)
	}
	return entries, frames, nil
}

func addLogIndex(first uint64, offset int) (uint64, error) {
	offset64, err := safeconv.IntToUint64("command index offset", offset)
	if err != nil {
		return 0, err
	}
	value := first + offset64
	if value < first {
		return 0, errors.New("raftmeta: command index overflows uint64")
	}
	return value, nil
}

func (l *Log) writeFrames(frames [][]byte) error {
	for _, frame := range frames {
		written, err := l.file.Write(frame)
		if err != nil {
			return err
		}
		if written != len(frame) {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (l *Log) recordFailureLocked(stage string, err error) error {
	if l.failureErr == nil {
		l.failureErr = fmt.Errorf("raftmeta: command log %s failed: %w", stage, err)
	}
	return l.failureErr
}

func (l *Log) defaultSyncLogFile() error {
	if l.file == nil {
		return errors.New("raftmeta: log is closed")
	}
	return l.file.Sync()
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
	tail := compactedTail(entries, throughIndex)
	dir := filepath.Dir(l.path)
	tempPath, err := safepath.UnderDir(dir, l.path+".compact")
	if err != nil {
		return err
	}
	if err := writeEntries(tempPath, tail); err != nil {
		return err
	}
	if err := l.replaceWithCompactedLog(tempPath); err != nil {
		return err
	}
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

func compactedTail(entries []Entry, throughIndex uint64) []Entry {
	tail := entries[:0]
	for _, entry := range entries {
		if entry.Index > throughIndex {
			tail = append(tail, entry)
		}
	}
	return tail
}

func (l *Log) replaceWithCompactedLog(tempPath string) error {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	// #nosec G703 -- source and destination are validated under the configured raft directory.
	if err := os.Rename(tempPath, l.path); err != nil {
		l.reopenLogAfterFailedCompaction()
		return err
	}
	file, err := openLogFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND)
	if err != nil {
		return err
	}
	l.file = file
	return nil
}

func (l *Log) reopenLogAfterFailedCompaction() {
	file, err := openLogFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND)
	if err == nil {
		l.file = file
	}
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
