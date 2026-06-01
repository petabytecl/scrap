package shard

import (
	"fmt"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
)

type confirmedUploadAuthorityRecord struct {
	Version         int                         `json:"version"`
	BlockID         uint64                      `json:"block_id"`
	ShardID         uint64                      `json:"shard_id"`
	ConfirmedAtUs   int64                       `json:"confirmed_at_us"`
	SealedSizeBytes int64                       `json:"sealed_size_bytes"`
	BlockObject     index.BackendObjectMetadata `json:"block_object"`
	IndexObject     index.BackendObjectMetadata `json:"index_object"`
}

func confirmedUploadAuthorityPath(blocksDir string, blockID uint64) string {
	return filepath.Join(blocksDir, fmt.Sprintf("%016x.confirmed-upload.json", blockID))
}

func writeConfirmedUploadAuthority(blocksDir string, upload index.ConfirmedUpload) error {
	record := confirmedUploadAuthorityRecord{
		Version:         localblock.MarkerVersion,
		BlockID:         upload.BlockID,
		ShardID:         upload.ShardID,
		ConfirmedAtUs:   upload.ConfirmedAtUs,
		SealedSizeBytes: upload.SealedSizeBytes,
		BlockObject:     upload.BlockObject,
		IndexObject:     upload.IndexObject,
	}
	if err := validateConfirmedUploadAuthorityRecord(record, upload.BlockID); err != nil {
		return err
	}
	if err := localblock.WriteJSONMarker(confirmedUploadAuthorityPath(blocksDir, upload.BlockID), record); err != nil {
		return fmt.Errorf("shard: write committed ConfirmUpload authority: %w", err)
	}
	return nil
}

func readConfirmedUploadAuthority(blocksDir string, blockID uint64) (index.ConfirmedUpload, error) {
	var record confirmedUploadAuthorityRecord
	if err := localblock.ReadJSONMarker(confirmedUploadAuthorityPath(blocksDir, blockID), &record); err != nil {
		return index.ConfirmedUpload{}, err
	}
	if err := validateConfirmedUploadAuthorityRecord(record, blockID); err != nil {
		return index.ConfirmedUpload{}, err
	}
	return index.ConfirmedUpload{
		BlockID:         record.BlockID,
		ShardID:         record.ShardID,
		ConfirmedAtUs:   record.ConfirmedAtUs,
		SealedSizeBytes: record.SealedSizeBytes,
		BlockObject:     record.BlockObject,
		IndexObject:     record.IndexObject,
	}, nil
}

func validateConfirmedUploadAuthorityRecord(record confirmedUploadAuthorityRecord, blockID uint64) error {
	if err := localblock.ValidateMarkerHeader("committed ConfirmUpload authority", record.Version, record.BlockID, blockID); err != nil {
		return err
	}
	switch {
	case record.ConfirmedAtUs < 0:
		return fmt.Errorf("%w: committed ConfirmUpload confirmed_at_us is negative: %d", localblock.ErrMarkerInvalid, record.ConfirmedAtUs)
	case record.SealedSizeBytes < 0:
		return fmt.Errorf("%w: committed ConfirmUpload sealed_size_bytes is negative: %d", localblock.ErrMarkerInvalid, record.SealedSizeBytes)
	}
	if err := validateConfirmedUploadAuthorityObject("block", record.BlockObject); err != nil {
		return err
	}
	return validateConfirmedUploadAuthorityObject("index", record.IndexObject)
}

func validateConfirmedUploadAuthorityObject(kind string, object index.BackendObjectMetadata) error {
	switch {
	case object.Key == "":
		return fmt.Errorf("%w: committed ConfirmUpload %s object key is required", localblock.ErrMarkerInvalid, kind)
	case object.SizeBytes < 0:
		return fmt.Errorf("%w: committed ConfirmUpload %s object size_bytes is negative: %d", localblock.ErrMarkerInvalid, kind, object.SizeBytes)
	case object.ValidationToken == "":
		return fmt.Errorf("%w: committed ConfirmUpload %s object validation_token is required", localblock.ErrMarkerInvalid, kind)
	default:
		return nil
	}
}
