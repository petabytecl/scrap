package localblock

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
	MarkerVersion = 1

	EvictionTriggerOperatorRequested = "operator_requested"
	EvictionReasonEvidenceRun        = "evidence_run"

	RestoreSourceBackend    = "backend"
	RestoreSourcePeer       = "peer"
	RestoreReasonRead       = "read"
	RestoreReasonValidation = "validation"
	RestoreReasonRepair     = "repair"
)

var ErrMarkerInvalid = errors.New("localblock: lifecycle marker invalid")

type State string

const (
	StateHot              State = "hot"
	StateEvicted          State = "evicted"
	StateHotCleanupNeeded State = "hot_cleanup_needed"
	StateMetadataLoss     State = "metadata_loss"
	StateUnexpectedLoss   State = "unexpected_loss"
)

type Lifecycle struct {
	BlockID        uint64
	State          State
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
	marker.Version = MarkerVersion
	if err := validateEvictionMarker(marker, marker.BlockID); err != nil {
		return err
	}
	if err := WriteJSONMarker(EvictionMarkerPath(blocksDir, marker.BlockID), marker); err != nil {
		return fmt.Errorf("localblock: write eviction marker: %w", err)
	}
	return nil
}

func ReadEvictionMarker(blocksDir string, blockID uint64) (EvictionMarker, error) {
	var marker EvictionMarker
	if err := ReadJSONMarker(EvictionMarkerPath(blocksDir, blockID), &marker); err != nil {
		return EvictionMarker{}, err
	}
	if err := validateEvictionMarker(marker, blockID); err != nil {
		return EvictionMarker{}, err
	}
	return marker, nil
}

func WriteRestoreMarker(blocksDir string, marker RestoreMarker) error {
	marker.Version = MarkerVersion
	if err := validateRestoreMarker(marker, marker.BlockID); err != nil {
		return err
	}
	if err := WriteJSONMarker(RestoreMarkerPath(blocksDir, marker.BlockID), marker); err != nil {
		return fmt.Errorf("localblock: write restore marker: %w", err)
	}
	return nil
}

func ReadRestoreMarker(blocksDir string, blockID uint64) (RestoreMarker, error) {
	var marker RestoreMarker
	if err := ReadJSONMarker(RestoreMarkerPath(blocksDir, blockID), &marker); err != nil {
		return RestoreMarker{}, err
	}
	if err := validateRestoreMarker(marker, blockID); err != nil {
		return RestoreMarker{}, err
	}
	return marker, nil
}

func Classify(blocksDir string, blockID uint64) (Lifecycle, error) {
	blkExists, err := fileExists(block.FilePath(blocksDir, blockID))
	if err != nil {
		return Lifecycle{}, err
	}
	idxExists, err := fileExists(block.IdxFilePath(blocksDir, blockID))
	if err != nil {
		return Lifecycle{}, err
	}
	if !idxExists {
		return Lifecycle{
			BlockID:        blockID,
			State:          StateMetadataLoss,
			HealthDegraded: true,
		}, nil
	}

	markers, err := readMarkers(blocksDir, blockID, blkExists)
	if err != nil {
		return markers.failure, err
	}
	return classifyState(blockID, blkExists, markers), nil
}

func classifyState(blockID uint64, blkExists bool, markers markers) Lifecycle {
	lifecycle := Lifecycle{BlockID: blockID}
	if markers.hasEviction {
		lifecycle.EvictionMarker = &markers.eviction
	}
	if markers.hasRestore {
		lifecycle.RestoreMarker = &markers.restore
	}
	switch {
	case blkExists && markers.hasEviction:
		lifecycle.State = StateHotCleanupNeeded
		lifecycle.ServingAllowed = true
		lifecycle.HealthDegraded = true
	case blkExists:
		lifecycle.State = StateHot
		lifecycle.ServingAllowed = true
	case markers.hasEviction:
		lifecycle.State = StateEvicted
	default:
		lifecycle.State = StateUnexpectedLoss
		lifecycle.HealthDegraded = true
	}
	return lifecycle
}

type markers struct {
	eviction    EvictionMarker
	restore     RestoreMarker
	hasEviction bool
	hasRestore  bool
	failure     Lifecycle
}

func readMarkers(blocksDir string, blockID uint64, blkExists bool) (markers, error) {
	evictionMarker, hasEvictionMarker, err := readOptionalEvictionMarker(blocksDir, blockID)
	if err != nil {
		return markers{
			failure: Lifecycle{
				BlockID:        blockID,
				State:          StateUnexpectedLoss,
				HealthDegraded: true,
			},
		}, err
	}

	restoreMarker, hasRestoreMarker, err := readOptionalRestoreMarker(blocksDir, blockID)
	if err != nil {
		return markers{
			failure: Lifecycle{
				BlockID:        blockID,
				State:          StateHot,
				ServingAllowed: blkExists,
				HealthDegraded: true,
			},
		}, err
	}

	return markers{
		eviction:    evictionMarker,
		restore:     restoreMarker,
		hasEviction: hasEvictionMarker,
		hasRestore:  hasRestoreMarker,
	}, nil
}

// MarkerCleanupFailure reports one Block whose eviction marker could not be
// classified or removed during CleanupHotMarkers. The marker is left in place.
type MarkerCleanupFailure struct {
	BlockID uint64
	Err     error
}

func CleanupHotMarkers(blocksDir string) ([]MarkerCleanupFailure, error) {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("localblock: read lifecycle markers: %w", err)
	}

	var failures []MarkerCleanupFailure
	removed := false
	for _, entry := range entries {
		blockID, ok := parseEvictionMarkerBlockID(entry.Name())
		if !ok {
			continue
		}
		markerRemoved, err := cleanupHotMarker(blocksDir, blockID)
		if err != nil {
			// One unreadable marker must not abort the sweep for every other
			// Block (#467): leave it in place for the operator and continue.
			failures = append(failures, MarkerCleanupFailure{BlockID: blockID, Err: err})
			continue
		}
		removed = removed || markerRemoved
	}
	if removed {
		if err := SyncDirectory(blocksDir); err != nil {
			return failures, fmt.Errorf("localblock: sync marker cleanup: %w", err)
		}
	}
	return failures, nil
}

func cleanupHotMarker(blocksDir string, blockID uint64) (bool, error) {
	lifecycle, err := Classify(blocksDir, blockID)
	if err != nil {
		return false, err
	}
	if lifecycle.State != StateHotCleanupNeeded {
		return false, nil
	}
	if err := os.Remove(EvictionMarkerPath(blocksDir, blockID)); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("localblock: remove stale eviction marker for Block %d: %w", blockID, err)
	}
	return true, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("localblock: stat %s: %w", path, err)
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
	if err := ValidateMarkerHeader("eviction", marker.Version, marker.BlockID, blockID); err != nil {
		return err
	}
	switch {
	case marker.BackendKey == "":
		return fmt.Errorf("%w: eviction marker backend_key is required", ErrMarkerInvalid)
	case marker.SizeBytes < 0:
		return fmt.Errorf("%w: eviction marker size_bytes is negative: %d", ErrMarkerInvalid, marker.SizeBytes)
	case marker.ValidationToken == "":
		return fmt.Errorf("%w: eviction marker validation_token is required", ErrMarkerInvalid)
	case marker.EvictedAtUs <= 0:
		return fmt.Errorf("%w: eviction marker evicted_at_us is required", ErrMarkerInvalid)
	case marker.Trigger == "":
		return fmt.Errorf("%w: eviction marker trigger is required", ErrMarkerInvalid)
	case marker.Reason == "":
		return fmt.Errorf("%w: eviction marker reason is required", ErrMarkerInvalid)
	default:
		return nil
	}
}

func validateRestoreMarker(marker RestoreMarker, blockID uint64) error {
	if err := ValidateMarkerHeader("restore", marker.Version, marker.BlockID, blockID); err != nil {
		return err
	}
	switch {
	case marker.RestoredAtUs <= 0:
		return fmt.Errorf("%w: restore marker restored_at_us is required", ErrMarkerInvalid)
	case marker.Source == "":
		return fmt.Errorf("%w: restore marker source is required", ErrMarkerInvalid)
	case marker.Reason == "":
		return fmt.Errorf("%w: restore marker reason is required", ErrMarkerInvalid)
	default:
		return nil
	}
}

func ValidateMarkerHeader(kind string, version int, markerBlockID, blockID uint64) error {
	switch {
	case version != MarkerVersion:
		return fmt.Errorf("%w: %s marker version %d", ErrMarkerInvalid, kind, version)
	case markerBlockID != blockID:
		return fmt.Errorf("%w: %s marker block_id mismatch: key %d value %d", ErrMarkerInvalid, kind, blockID, markerBlockID)
	case markerBlockID == 0:
		return fmt.Errorf("%w: %s marker block_id is required", ErrMarkerInvalid, kind)
	default:
		return nil
	}
}

func ReadJSONMarker(path string, out any) error {
	f, err := os.Open(path) //nolint:gosec // local marker paths are derived from data directories and Block IDs.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: decode %s: %w", ErrMarkerInvalid, path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON in %s", ErrMarkerInvalid, path)
	}
	return nil
}

func WriteJSONMarker(path string, marker any) error {
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
	if err := SyncDirectory(dir); err != nil {
		return fmt.Errorf("sync marker directory: %w", err)
	}
	return nil
}

func SyncDirectory(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // directory path is derived from controlled data directories.
	if err != nil {
		return fmt.Errorf("localblock: open directory for sync: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("localblock: sync directory: %w", err)
	}
	return nil
}

func ParseEvictionMarkerBlockID(name string) (uint64, bool) {
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

func parseEvictionMarkerBlockID(name string) (uint64, bool) {
	return ParseEvictionMarkerBlockID(name)
}
