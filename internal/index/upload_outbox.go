package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/cockroachdb/pebble"
)

var ErrPendingUploadNotFound = errors.New("index: pending upload not found")

const (
	pendingUploadPrefix         = "\x00upload\x00"
	pendingUploadUpperBound     = "\x00upload\x01"
	pendingUploadValueVersionV1 = 0x01
	pendingUploadValueVersion   = 0x02
	pendingUploadKeyLen         = len(pendingUploadPrefix) + sizeBlockID
	pendingUploadValueLenV1     = 1 + sizeBlockID + 8 + 8 // version + shard_id + sealed_size + sealed_at_us
	pendingUploadValueLen       = pendingUploadValueLenV1 + 8
)

type PendingUpload struct {
	BlockID          uint64
	ShardID          uint64
	SealedSizeBytes  int64
	SealedAtUs       int64
	UploadGeneration int64
}

type PendingUploadIterator interface {
	Next() (PendingUpload, error)
}

func (idx *Index) PutPendingUpload(upload PendingUpload) error {
	if upload.BlockID == 0 {
		return errors.New("index: pending upload block_id is required")
	}
	if upload.SealedSizeBytes < 0 {
		return fmt.Errorf("index: pending upload sealed size is negative: %d", upload.SealedSizeBytes)
	}
	if upload.SealedAtUs < 0 {
		return fmt.Errorf("index: pending upload sealed_at_us is negative: %d", upload.SealedAtUs)
	}
	if upload.UploadGeneration < 0 {
		return fmt.Errorf("index: pending upload generation is negative: %d", upload.UploadGeneration)
	}
	return idx.db.Set(pendingUploadKey(upload.BlockID), encodePendingUpload(upload), pebble.Sync)
}

func (idx *Index) DeletePendingUpload(blockID uint64) error {
	if blockID == 0 {
		return errors.New("index: pending upload block_id is required")
	}
	return idx.db.Delete(pendingUploadKey(blockID), pebble.Sync)
}

func (idx *Index) GetPendingUpload(blockID uint64) (PendingUpload, error) {
	if blockID == 0 {
		return PendingUpload{}, errors.New("index: pending upload block_id is required")
	}
	val, closer, err := idx.db.Get(pendingUploadKey(blockID))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return PendingUpload{}, ErrPendingUploadNotFound
		}
		return PendingUpload{}, fmt.Errorf("index: get pending upload: %w", err)
	}
	defer func() { _ = closer.Close() }()

	return decodePendingUpload(pendingUploadKey(blockID), val)
}

func (idx *Index) PendingUploads() (PendingUploadIterator, error) {
	iter, err := idx.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(pendingUploadPrefix),
		UpperBound: []byte(pendingUploadUpperBound),
	})
	if err != nil {
		return nil, fmt.Errorf("index: pending upload iter: %w", err)
	}
	defer func() {
		_ = iter.Close()
	}()

	uploads := make([]PendingUpload, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		val, err := iter.ValueAndErr()
		if err != nil {
			return nil, fmt.Errorf("index: pending upload iter value: %w", err)
		}
		upload, err := decodePendingUpload(iter.Key(), val)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("index: pending upload iter: %w", err)
	}
	return &pendingUploadIterator{uploads: uploads}, nil
}

func pendingUploadKey(blockID uint64) []byte {
	key := make([]byte, pendingUploadKeyLen)
	copy(key, pendingUploadPrefix)
	binary.BigEndian.PutUint64(key[len(pendingUploadPrefix):], blockID)
	return key
}

func encodePendingUpload(upload PendingUpload) []byte {
	buf := make([]byte, pendingUploadValueLen)
	buf[0] = pendingUploadValueVersion
	binary.LittleEndian.PutUint64(buf[1:9], upload.ShardID)
	putNonNegativeInt64(buf[9:17], upload.SealedSizeBytes)
	putNonNegativeInt64(buf[17:25], upload.SealedAtUs)
	putNonNegativeInt64(buf[25:33], upload.UploadGeneration)
	return buf
}

func decodePendingUpload(key, val []byte) (PendingUpload, error) {
	if len(key) != pendingUploadKeyLen {
		return PendingUpload{}, fmt.Errorf("index: pending upload key length %d", len(key))
	}
	version, err := pendingUploadValueVersionForDecode(val)
	if err != nil {
		return PendingUpload{}, err
	}

	sealedSize, err := readNonNegativeInt64(val[9:17], "sealed_size")
	if err != nil {
		return PendingUpload{}, err
	}
	sealedAt, err := readNonNegativeInt64(val[17:25], "sealed_at_us")
	if err != nil {
		return PendingUpload{}, err
	}
	uploadGeneration, err := decodePendingUploadGeneration(version, val)
	if err != nil {
		return PendingUpload{}, err
	}

	return PendingUpload{
		BlockID:          binary.BigEndian.Uint64(key[len(pendingUploadPrefix):]),
		ShardID:          binary.LittleEndian.Uint64(val[1:9]),
		SealedSizeBytes:  sealedSize,
		SealedAtUs:       sealedAt,
		UploadGeneration: uploadGeneration,
	}, nil
}

func pendingUploadValueVersionForDecode(val []byte) (byte, error) {
	if len(val) == 0 {
		return 0, fmt.Errorf("index: pending upload value length %d", len(val))
	}
	switch version := val[0]; version {
	case pendingUploadValueVersionV1:
		return version, requirePendingUploadValueLen(val, pendingUploadValueLenV1)
	case pendingUploadValueVersion:
		return version, requirePendingUploadValueLen(val, pendingUploadValueLen)
	default:
		return 0, fmt.Errorf("index: pending upload value version %d", version)
	}
}

func requirePendingUploadValueLen(val []byte, want int) error {
	if len(val) != want {
		return fmt.Errorf("index: pending upload value length %d", len(val))
	}
	return nil
}

func decodePendingUploadGeneration(version byte, val []byte) (int64, error) {
	if version == pendingUploadValueVersionV1 {
		return 0, nil
	}
	return readNonNegativeInt64(val[25:33], "upload_generation")
}

func putNonNegativeInt64(buf []byte, v int64) {
	binary.LittleEndian.PutUint64(buf, uint64(v)) //nolint:gosec // caller validates non-negative values.
}

func readNonNegativeInt64(buf []byte, field string) (int64, error) {
	raw := binary.LittleEndian.Uint64(buf)
	if raw > math.MaxInt64 {
		return 0, fmt.Errorf("index: pending upload %s overflows int64", field)
	}
	return int64(raw), nil
}

type pendingUploadIterator struct {
	uploads []PendingUpload
	next    int
}

func (i *pendingUploadIterator) Next() (PendingUpload, error) {
	if i.next >= len(i.uploads) {
		return PendingUpload{}, io.EOF
	}

	upload := i.uploads[i.next]
	i.next++
	return upload, nil
}
