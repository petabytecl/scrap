package localstorage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/metastore"
)

const (
	prepareLogName      = "local.openlog"
	prepareLogHeaderLen = 4
	prepareLogCRCLen    = 4
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type prepareLog struct {
	path string
	file *os.File
}

func openPrepareLog(dir string) (*prepareLog, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, prepareLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &prepareLog{path: path, file: file}, nil
}

func (l *prepareLog) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *prepareLog) Append(document metastore.Document) error {
	payload, err := metastore.MarshalDocument(document)
	if err != nil {
		return err
	}
	if uint64(len(payload)) > uint64(^uint32(0)) {
		return fmt.Errorf("localstorage: prepare record too large: %d", len(payload))
	}
	frame := make([]byte, prepareLogHeaderLen+len(payload)+prepareLogCRCLen)
	binary.BigEndian.PutUint32(frame[:prepareLogHeaderLen], uint32(len(payload)))
	copy(frame[prepareLogHeaderLen:], payload)
	crc := crc32.Checksum(payload, crcTable)
	binary.BigEndian.PutUint32(frame[len(frame)-prepareLogCRCLen:], crc)
	if _, err := l.file.Write(frame); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *prepareLog) Recover() ([]metastore.Document, error) {
	file, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var documents []metastore.Document
	for {
		var header [prepareLogHeaderLen]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return documents, nil
			}
			return nil, err
		}
		length := binary.BigEndian.Uint32(header[:])
		payload := make([]byte, length)
		if _, err := io.ReadFull(file, payload); err != nil {
			return nil, err
		}
		var checksum [prepareLogCRCLen]byte
		if _, err := io.ReadFull(file, checksum[:]); err != nil {
			return nil, err
		}
		want := binary.BigEndian.Uint32(checksum[:])
		if got := crc32.Checksum(payload, crcTable); got != want {
			return nil, fmt.Errorf("localstorage: prepare log checksum mismatch")
		}
		document, err := metastore.UnmarshalDocument(payload)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
}
