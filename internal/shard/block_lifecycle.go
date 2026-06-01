package shard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/petabytecl/scrap/internal/block"
)

const (
	LifecycleMarkerVersion = 1

	EvictionTriggerOperatorRequested = "operator_requested"
	EvictionReasonEvidenceRun        = "evidence_run"

	RestoreSourceBackend = "backend"
	RestoreReasonRead    = "read"
)

var ErrLifecycleMarkerInvalid = errors.New("shard: lifecycle marker invalid")

type LocalBlockState string

const (
	LocalBlockStateHot              LocalBlockState = "hot"
	LocalBlockStateEvicted          LocalBlockState = "evicted"
	LocalBlockStateHotCleanupNeeded LocalBlockState = "hot_cleanup_needed"
	LocalBlockStateMetadataLoss     LocalBlockState = "metadata_loss"
	LocalBlockStateUnexpectedLoss   LocalBlockState = "unexpected_loss"
)

type LocalBlockLifecycle struct {
	BlockID        uint64
	State          LocalBlockState
	ServingAllowed bool
	HealthDegraded bool
	EvictionMarker *EvictionMarker
	RestoreMarker  *RestoreMarker
}

type EvictionMarker struct {
	Version         int    `json:"version"`
	BlockID         uint64 `json:"block_id"`
	BackendKey      string `json:"backend_key"`
	SizeBytes       int64  `json:"size_bytes"`
	ValidationToken string `json:"validation_token"`
	EvictedAtUs     int64  `json:"evicted_at_us"`
	Trigger         string `json:"trigger"`
	Reason          string `json:"reason"`
}

type RestoreMarker struct {
	Version      int    `json:"version"`
	BlockID      uint64 `json:"block_id"`
	RestoredAtUs int64  `json:"restored_at_us"`
	Source       string `json:"source"`
	Reason       string `json:"reason"`
}

func EvictionMarkerPath(blocksDir string, blockID uint64) string {
	return filepath.Join(blocksDir, fmt.Sprintf("%016x.blk.eviction.json", blockID))
}

func RestoreMarkerPath(blocksDir string, blockID uint64) string {
	return filepath.Join(blocksDir, fmt.Sprintf("%016x.blk.restore.json", blockID))
}

func WriteEvictionMarker(blocksDir string, marker EvictionMarker) error {
	marker.Version = LifecycleMarkerVersion
	if err := validateEvictionMarker(marker, marker.BlockID); err != nil {
		return err
	}
	if err := writeMarkerJSON(EvictionMarkerPath(blocksDir, marker.BlockID), marker); err != nil {
		return fmt.Errorf("shard: write eviction marker: %w", err)
	}
	return nil
}

func ReadEvictionMarker(blocksDir string, blockID uint64) (EvictionMarker, error) {
	var marker EvictionMarker
	if err := readMarkerJSON(EvictionMarkerPath(blocksDir, blockID), &marker); err != nil {
		return EvictionMarker{}, err
	}
	if err := validateEvictionMarker(marker, blockID); err != nil {
		return EvictionMarker{}, err
	}
	return marker, nil
}

func WriteRestoreMarker(blocksDir string, marker RestoreMarker) error {
	marker.Version = LifecycleMarkerVersion
	if err := validateRestoreMarker(marker, marker.BlockID); err != nil {
		return err
	}
	if err := writeMarkerJSON(RestoreMarkerPath(blocksDir, marker.BlockID), marker); err != nil {
		return fmt.Errorf("shard: write restore marker: %w", err)
	}
	return nil
}

func ReadRestoreMarker(blocksDir string, blockID uint64) (RestoreMarker, error) {
	var marker RestoreMarker
	if err := readMarkerJSON(RestoreMarkerPath(blocksDir, blockID), &marker); err != nil {
		return RestoreMarker{}, err
	}
	if err := validateRestoreMarker(marker, blockID); err != nil {
		return RestoreMarker{}, err
	}
	return marker, nil
}

func ClassifyLocalBlock(blocksDir string, blockID uint64) (LocalBlockLifecycle, error) {
	blkExists, err := fileExists(block.FilePath(blocksDir, blockID))
	if err != nil {
		return LocalBlockLifecycle{}, err
	}
	idxExists, err := fileExists(block.IdxFilePath(blocksDir, blockID))
	if err != nil {
		return LocalBlockLifecycle{}, err
	}
	if !idxExists {
		return LocalBlockLifecycle{
			BlockID:        blockID,
			State:          LocalBlockStateMetadataLoss,
			HealthDegraded: true,
		}, nil
	}

	markers, err := readLocalLifecycleMarkers(blocksDir, blockID, blkExists)
	if err != nil {
		return markers.failure, err
	}
	return classifyLocalBlockState(blockID, blkExists, markers), nil
}

func classifyLocalBlockState(blockID uint64, blkExists bool, markers lifecycleMarkers) LocalBlockLifecycle {
	lifecycle := LocalBlockLifecycle{BlockID: blockID}
	if markers.hasEviction {
		lifecycle.EvictionMarker = &markers.eviction
	}
	if markers.hasRestore {
		lifecycle.RestoreMarker = &markers.restore
	}
	switch {
	case blkExists && markers.hasEviction:
		lifecycle.State = LocalBlockStateHotCleanupNeeded
		lifecycle.ServingAllowed = true
		lifecycle.HealthDegraded = true
	case blkExists:
		lifecycle.State = LocalBlockStateHot
		lifecycle.ServingAllowed = true
	case markers.hasEviction:
		lifecycle.State = LocalBlockStateEvicted
	default:
		lifecycle.State = LocalBlockStateUnexpectedLoss
		lifecycle.HealthDegraded = true
	}
	return lifecycle
}

type lifecycleMarkers struct {
	eviction    EvictionMarker
	restore     RestoreMarker
	hasEviction bool
	hasRestore  bool
	failure     LocalBlockLifecycle
}

func readLocalLifecycleMarkers(blocksDir string, blockID uint64, blkExists bool) (lifecycleMarkers, error) {
	evictionMarker, hasEvictionMarker, err := readOptionalEvictionMarker(blocksDir, blockID)
	if err != nil {
		return lifecycleMarkers{
			failure: LocalBlockLifecycle{
				BlockID:        blockID,
				State:          LocalBlockStateUnexpectedLoss,
				HealthDegraded: true,
			},
		}, err
	}

	restoreMarker, hasRestoreMarker, err := readOptionalRestoreMarker(blocksDir, blockID)
	if err != nil {
		return lifecycleMarkers{
			failure: LocalBlockLifecycle{
				BlockID:        blockID,
				State:          LocalBlockStateHot,
				ServingAllowed: blkExists,
				HealthDegraded: true,
			},
		}, err
	}

	return lifecycleMarkers{
		eviction:    evictionMarker,
		restore:     restoreMarker,
		hasEviction: hasEvictionMarker,
		hasRestore:  hasRestoreMarker,
	}, nil
}

func CleanupHotLifecycleMarkers(blocksDir string) error {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("shard: read lifecycle markers: %w", err)
	}

	for _, entry := range entries {
		blockID, ok := parseEvictionMarkerBlockID(entry.Name())
		if !ok {
			continue
		}
		lifecycle, err := ClassifyLocalBlock(blocksDir, blockID)
		if err != nil {
			return err
		}
		if lifecycle.State != LocalBlockStateHotCleanupNeeded {
			continue
		}
		if err := os.Remove(EvictionMarkerPath(blocksDir, blockID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("shard: remove stale eviction marker for block %d: %w", blockID, err)
		}
	}
	return nil
}

func (s *Shard) startLifecycleCleanup() {
	done := make(chan struct{})
	s.lifecycleCleanupDone = done
	go func() {
		defer close(done)
		if err := CleanupHotLifecycleMarkers(s.blocksDir); err != nil {
			s.logger.Warn("lifecycle cleanup failed", "error", err)
		}
	}()
}

func (s *Shard) WaitLifecycleCleanupForTest() {
	s.waitLifecycleCleanup()
}

func (s *Shard) waitLifecycleCleanup() {
	if s.lifecycleCleanupDone == nil {
		return
	}
	<-s.lifecycleCleanupDone
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("shard: stat %s: %w", path, err)
}

func readOptionalEvictionMarker(blocksDir string, blockID uint64) (EvictionMarker, bool, error) {
	marker, err := ReadEvictionMarker(blocksDir, blockID)
	if err == nil {
		return marker, true, nil
	}
	if os.IsNotExist(err) {
		return EvictionMarker{}, false, nil
	}
	return EvictionMarker{}, false, err
}

func readOptionalRestoreMarker(blocksDir string, blockID uint64) (RestoreMarker, bool, error) {
	marker, err := ReadRestoreMarker(blocksDir, blockID)
	if err == nil {
		return marker, true, nil
	}
	if os.IsNotExist(err) {
		return RestoreMarker{}, false, nil
	}
	return RestoreMarker{}, false, err
}

func validateEvictionMarker(marker EvictionMarker, blockID uint64) error {
	if err := validateMarkerHeader("eviction", marker.Version, marker.BlockID, blockID); err != nil {
		return err
	}
	switch {
	case marker.BackendKey == "":
		return fmt.Errorf("%w: eviction marker backend_key is required", ErrLifecycleMarkerInvalid)
	case marker.SizeBytes < 0:
		return fmt.Errorf("%w: eviction marker size_bytes is negative: %d", ErrLifecycleMarkerInvalid, marker.SizeBytes)
	case marker.ValidationToken == "":
		return fmt.Errorf("%w: eviction marker validation_token is required", ErrLifecycleMarkerInvalid)
	case marker.EvictedAtUs <= 0:
		return fmt.Errorf("%w: eviction marker evicted_at_us is required", ErrLifecycleMarkerInvalid)
	case marker.Trigger == "":
		return fmt.Errorf("%w: eviction marker trigger is required", ErrLifecycleMarkerInvalid)
	case marker.Reason == "":
		return fmt.Errorf("%w: eviction marker reason is required", ErrLifecycleMarkerInvalid)
	default:
		return nil
	}
}

func validateRestoreMarker(marker RestoreMarker, blockID uint64) error {
	if err := validateMarkerHeader("restore", marker.Version, marker.BlockID, blockID); err != nil {
		return err
	}
	switch {
	case marker.RestoredAtUs <= 0:
		return fmt.Errorf("%w: restore marker restored_at_us is required", ErrLifecycleMarkerInvalid)
	case marker.Source == "":
		return fmt.Errorf("%w: restore marker source is required", ErrLifecycleMarkerInvalid)
	case marker.Reason == "":
		return fmt.Errorf("%w: restore marker reason is required", ErrLifecycleMarkerInvalid)
	default:
		return nil
	}
}

func validateMarkerHeader(kind string, version int, markerBlockID, blockID uint64) error {
	switch {
	case version != LifecycleMarkerVersion:
		return fmt.Errorf("%w: %s marker version %d", ErrLifecycleMarkerInvalid, kind, version)
	case markerBlockID != blockID:
		return fmt.Errorf("%w: %s marker block_id mismatch: key %d value %d", ErrLifecycleMarkerInvalid, kind, blockID, markerBlockID)
	case markerBlockID == 0:
		return fmt.Errorf("%w: %s marker block_id is required", ErrLifecycleMarkerInvalid, kind)
	default:
		return nil
	}
}

func readMarkerJSON(path string, out any) error {
	f, err := os.Open(path) //nolint:gosec // lifecycle marker paths are derived from Shard blocksDir and Block ID.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: decode %s: %w", ErrLifecycleMarkerInvalid, path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON in %s", ErrLifecycleMarkerInvalid, path)
	}
	return nil
}

func writeMarkerJSON(path string, marker any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir marker dir: %w", err)
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode marker: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create marker temp file: %w", err)
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write marker temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync marker temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close marker temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish marker file: %w", err)
	}
	published = true
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync marker directory: %w", err)
	}
	return nil
}

func parseEvictionMarkerBlockID(name string) (uint64, bool) {
	const suffix = ".blk.eviction.json"
	if !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	id, err := strconv.ParseUint(strings.TrimSuffix(name, suffix), 16, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
