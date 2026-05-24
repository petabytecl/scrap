package localstorage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/closeutil"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/safepath"
)

const (
	prepareLogName          = "local.openlog"
	prepareLogHeaderLen     = 4
	prepareLogCRCLen        = 4
	maxPrepareLogPayloadLen = 64 * 1024 * 1024
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
	path, err := safepath.UnderDir(dir, filepath.Join(dir, prepareLogName))
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	file, err := openLocalFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if err := syncLocalDir(dir); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, err
		}
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
	if len(payload) > maxPrepareLogPayloadLen {
		return fmt.Errorf("localstorage: prepare record too large: %d", len(payload))
	}
	payloadLen, err := safeconv.IntToUint32("prepare log payload length", len(payload))
	if err != nil {
		return err
	}
	frame := make([]byte, prepareLogHeaderLen+len(payload)+prepareLogCRCLen)
	binary.BigEndian.PutUint32(frame[:prepareLogHeaderLen], payloadLen)
	copy(frame[prepareLogHeaderLen:], payload)
	crc := crc32.Checksum(payload, crcTable)
	binary.BigEndian.PutUint32(frame[len(frame)-prepareLogCRCLen:], crc)
	if _, err := l.file.Write(frame); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *prepareLog) Recover() ([]metastore.Document, error) {
	file, err := os.OpenFile(l.path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closeutil.Ignore(file)

	var documents []metastore.Document
	var validOffset int64
	for {
		document, advance, action, err := readPrepareLogRecord(file)
		if err != nil {
			return nil, err
		}
		switch action {
		case prepareLogContinue:
		case prepareLogDone:
			return documents, nil
		case prepareLogTruncate:
			return documents, truncatePrepareLogTail(file, validOffset)
		}
		documents = append(documents, document)
		validOffset += advance
	}
}

type prepareLogReadAction int

const (
	prepareLogContinue prepareLogReadAction = iota
	prepareLogDone
	prepareLogTruncate
)

func readPrepareLogRecord(file *os.File) (metastore.Document, int64, prepareLogReadAction, error) {
	var header [prepareLogHeaderLen]byte
	action, err := readPrepareLogBytes(file, header[:], true)
	if action != prepareLogContinue || err != nil {
		return metastore.Document{}, 0, action, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > maxPrepareLogPayloadLen {
		return metastore.Document{}, 0, prepareLogTruncate, nil
	}
	payload := make([]byte, int(length))
	action, err = readPrepareLogBytes(file, payload, false)
	if action != prepareLogContinue || err != nil {
		return metastore.Document{}, 0, action, err
	}
	var checksum [prepareLogCRCLen]byte
	action, err = readPrepareLogBytes(file, checksum[:], false)
	if action != prepareLogContinue || err != nil {
		return metastore.Document{}, 0, action, err
	}
	want := binary.BigEndian.Uint32(checksum[:])
	if got := crc32.Checksum(payload, crcTable); got != want {
		return metastore.Document{}, 0, prepareLogTruncate, nil
	}
	document, err := metastore.UnmarshalDocument(payload)
	if err != nil {
		return metastore.Document{}, 0, prepareLogContinue, err
	}
	advance := int64(prepareLogHeaderLen + len(payload) + prepareLogCRCLen)
	return document, advance, prepareLogContinue, nil
}

func readPrepareLogBytes(file *os.File, data []byte, eofEndsLog bool) (prepareLogReadAction, error) {
	if _, err := io.ReadFull(file, data); err != nil {
		if eofEndsLog && errors.Is(err, io.EOF) {
			return prepareLogDone, nil
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return prepareLogTruncate, nil
		}
		return prepareLogContinue, err
	}
	return prepareLogContinue, nil
}

func truncatePrepareLogTail(file *os.File, validOffset int64) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == validOffset {
		return nil
	}
	if err := file.Truncate(validOffset); err != nil {
		return err
	}
	return file.Sync()
}
