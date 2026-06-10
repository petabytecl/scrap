package index

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cockroachdb/pebble"
)

var ErrConfirmedUploadNotFound = errors.New("index: confirmed upload not found")

const (
	confirmedUploadPrefix       = "\x00confirmed-upload\x00"
	confirmedUploadUpperBound   = "\x00confirmed-upload\x01"
	confirmedUploadValueVersion = 0x01
	confirmedUploadKeyLen       = len(confirmedUploadPrefix) + sizeBlockID
)

type BackendObjectMetadata struct {
	Key             string `json:"key"`
	SizeBytes       int64  `json:"size_bytes"`
	ValidationToken string `json:"validation_token"`
}

type ConfirmedUpload struct {
	BlockID          uint64
	ShardID          uint64
	ConfirmedAtUs    int64
	SealedSizeBytes  int64
	UploadGeneration int64
	BlockObject      BackendObjectMetadata
	IndexObject      BackendObjectMetadata
}

type ConfirmedUploadIterator interface {
	Next() (ConfirmedUpload, error)
}

type confirmedUploadRecord struct {
	Version          byte                  `json:"version"`
	BlockID          uint64                `json:"block_id"`
	ShardID          uint64                `json:"shard_id"`
	ConfirmedAtUs    int64                 `json:"confirmed_at_us"`
	SealedSizeBytes  int64                 `json:"sealed_size_bytes"`
	UploadGeneration int64                 `json:"upload_generation"`
	BlockObject      BackendObjectMetadata `json:"block_object"`
	IndexObject      BackendObjectMetadata `json:"index_object"`
}

func (idx *Index) PutConfirmedUpload(upload ConfirmedUpload) error {
	if err := validateConfirmedUpload(upload); err != nil {
		return err
	}
	val, err := encodeConfirmedUpload(upload)
	if err != nil {
		return err
	}
	return idx.db.Set(confirmedUploadKey(upload.BlockID), val, pebble.Sync)
}

func (idx *Index) GetConfirmedUpload(blockID uint64) (ConfirmedUpload, error) {
	if blockID == 0 {
		return ConfirmedUpload{}, errors.New("index: confirmed upload block_id is required")
	}
	val, closer, err := idx.db.Get(confirmedUploadKey(blockID))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return ConfirmedUpload{}, ErrConfirmedUploadNotFound
		}
		return ConfirmedUpload{}, fmt.Errorf("index: get confirmed upload: %w", err)
	}
	defer func() { _ = closer.Close() }()

	return decodeConfirmedUpload(blockID, val)
}

func (idx *Index) ConfirmedUploads() (ConfirmedUploadIterator, error) {
	iter, err := idx.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(confirmedUploadPrefix),
		UpperBound: []byte(confirmedUploadUpperBound),
	})
	if err != nil {
		return nil, fmt.Errorf("index: confirmed upload iter: %w", err)
	}
	defer func() {
		_ = iter.Close()
	}()

	uploads := make([]ConfirmedUpload, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		blockID, err := confirmedUploadBlockID(iter.Key())
		if err != nil {
			return nil, err
		}
		val, err := iter.ValueAndErr()
		if err != nil {
			return nil, fmt.Errorf("index: confirmed upload iter value: %w", err)
		}
		upload, err := decodeConfirmedUpload(blockID, val)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("index: confirmed upload iter: %w", err)
	}
	return &confirmedUploadIterator{uploads: uploads}, nil
}

func confirmedUploadKey(blockID uint64) []byte {
	key := make([]byte, confirmedUploadKeyLen)
	copy(key, confirmedUploadPrefix)
	binary.BigEndian.PutUint64(key[len(confirmedUploadPrefix):], blockID)
	return key
}

func confirmedUploadBlockID(key []byte) (uint64, error) {
	if len(key) != confirmedUploadKeyLen {
		return 0, fmt.Errorf("index: confirmed upload key length %d", len(key))
	}
	return binary.BigEndian.Uint64(key[len(confirmedUploadPrefix):]), nil
}

func encodeConfirmedUpload(upload ConfirmedUpload) ([]byte, error) {
	record := confirmedUploadRecord{
		Version:          confirmedUploadValueVersion,
		BlockID:          upload.BlockID,
		ShardID:          upload.ShardID,
		ConfirmedAtUs:    upload.ConfirmedAtUs,
		SealedSizeBytes:  upload.SealedSizeBytes,
		UploadGeneration: upload.UploadGeneration,
		BlockObject:      upload.BlockObject,
		IndexObject:      upload.IndexObject,
	}
	val, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("index: encode confirmed upload: %w", err)
	}
	return val, nil
}

func decodeConfirmedUpload(blockID uint64, val []byte) (ConfirmedUpload, error) {
	var record confirmedUploadRecord
	if err := json.Unmarshal(val, &record); err != nil {
		return ConfirmedUpload{}, fmt.Errorf("index: decode confirmed upload: %w", err)
	}
	if record.Version != confirmedUploadValueVersion {
		return ConfirmedUpload{}, fmt.Errorf("index: confirmed upload value version %d", record.Version)
	}
	upload := ConfirmedUpload{
		BlockID:          record.BlockID,
		ShardID:          record.ShardID,
		ConfirmedAtUs:    record.ConfirmedAtUs,
		SealedSizeBytes:  record.SealedSizeBytes,
		UploadGeneration: record.UploadGeneration,
		BlockObject:      record.BlockObject,
		IndexObject:      record.IndexObject,
	}
	if upload.BlockID != blockID {
		return ConfirmedUpload{}, fmt.Errorf("index: confirmed upload block_id mismatch: key %d value %d", blockID, upload.BlockID)
	}
	if err := validateConfirmedUpload(upload); err != nil {
		return ConfirmedUpload{}, err
	}
	return upload, nil
}

func validateConfirmedUpload(upload ConfirmedUpload) error {
	if upload.BlockID == 0 {
		return errors.New("index: confirmed upload block_id is required")
	}
	if upload.ConfirmedAtUs < 0 {
		return fmt.Errorf("index: confirmed upload confirmed_at_us is negative: %d", upload.ConfirmedAtUs)
	}
	if upload.SealedSizeBytes < 0 {
		return fmt.Errorf("index: confirmed upload sealed size is negative: %d", upload.SealedSizeBytes)
	}
	if upload.UploadGeneration < 0 {
		return fmt.Errorf("index: confirmed upload generation is negative: %d", upload.UploadGeneration)
	}
	if err := validateBackendObjectMetadata("block", upload.BlockObject); err != nil {
		return err
	}
	if err := validateBackendObjectMetadata("index", upload.IndexObject); err != nil {
		return err
	}
	return nil
}

func validateBackendObjectMetadata(kind string, meta BackendObjectMetadata) error {
	if meta.Key == "" {
		return fmt.Errorf("index: confirmed upload %s object key is required", kind)
	}
	if meta.SizeBytes < 0 {
		return fmt.Errorf("index: confirmed upload %s object size is negative: %d", kind, meta.SizeBytes)
	}
	if meta.ValidationToken == "" {
		return fmt.Errorf("index: confirmed upload %s object validation token is required", kind)
	}
	return nil
}

type confirmedUploadIterator struct {
	uploads []ConfirmedUpload
	next    int
}

func (i *confirmedUploadIterator) Next() (ConfirmedUpload, error) {
	if i.next >= len(i.uploads) {
		return ConfirmedUpload{}, io.EOF
	}

	upload := i.uploads[i.next]
	i.next++
	return upload, nil
}
