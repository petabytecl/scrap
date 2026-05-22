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

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
)

const (
	commandLogName      = "shard-command.log"
	commandLogHeaderLen = 12
	commandLogCRCLen    = 4
	MaxCommandBytes     = 16 * 1024 * 1024
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
	path := filepath.Join(dir, commandLogName)
	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
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
	frame := encodeFrame(index, payload)
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

func readEntries(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []Entry
	expectedIndex := uint64(1)
	for {
		var header [commandLogHeaderLen]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return entries, nil
			}
			return nil, err
		}
		index := binary.BigEndian.Uint64(header[0:8])
		if index != expectedIndex {
			return nil, fmt.Errorf("raftmeta: command index %d after %d", index, expectedIndex-1)
		}
		length := binary.BigEndian.Uint32(header[8:12])
		if length > MaxCommandBytes {
			return nil, fmt.Errorf("raftmeta: command length %d exceeds maximum %d", length, MaxCommandBytes)
		}
		payload := make([]byte, length)
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
		command, err := metastore.UnmarshalShardCommand(payload)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Index: index, Command: command})
		expectedIndex++
	}
}

func encodeFrame(index uint64, payload []byte) []byte {
	frame := make([]byte, commandLogHeaderLen+len(payload)+commandLogCRCLen)
	binary.BigEndian.PutUint64(frame[0:8], index)
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[commandLogHeaderLen:], payload)
	crc := checksumFrame(frame[:commandLogHeaderLen], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-commandLogCRCLen:], crc)
	return frame
}

func checksumFrame(header []byte, payload []byte) uint32 {
	crc := crc32.New(commandLogCRCTable)
	_, _ = crc.Write(header)
	_, _ = crc.Write(payload)
	return crc.Sum32()
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
