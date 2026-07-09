package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/petabytecl/scrap/internal/routing"
	"github.com/petabytecl/scrap/internal/security"
)

const placementIdentityFileName = "placement-identity.json"

type placementIdentityRecord struct {
	Digest string `json:"digest"`
}

type startupTopology struct {
	Placement           routing.Placement
	RouteMapSummary     string
	LocalShardIDs       []uint64
	SingleShardFallback bool
}

type placementFileConfig struct {
	SlotCount   int
	Shards      []uint64
	Ranges      []routing.SlotRange
	LocalShards []uint64
}

func (c *placementFileConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		SlotCount   int                 `json:"slot_count"`
		Shards      []uint64            `json:"shards"`
		Ranges      []routing.SlotRange `json:"ranges"`
		LocalShards []uint64            `json:"local_shards"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	*c = placementFileConfig{
		SlotCount:   raw.SlotCount,
		Shards:      append([]uint64(nil), raw.Shards...),
		Ranges:      append([]routing.SlotRange(nil), raw.Ranges...),
		LocalShards: append([]uint64(nil), raw.LocalShards...),
	}
	return nil
}

func validateStartupRoutingGates(cfg Config) (routing.Placement, error) {
	topology, err := validateStartupTopology(cfg)
	if err != nil {
		return routing.Placement{}, err
	}
	return topology.Placement, nil
}

func validateStartupTopology(cfg Config) (startupTopology, error) {
	placementFile := strings.TrimSpace(cfg.ShardPlacementFile)
	if placementFile == "" {
		if cfg.SecurityMode == security.ModeProduction {
			return startupTopology{}, placementConfigError("is required in production mode")
		}
		placement, err := routing.NewPlacement(routing.SingleShardConfig(appShardID))
		if err != nil {
			return startupTopology{}, fmt.Errorf("default Shard placement: %w", err)
		}
		return startupTopology{
			Placement:           placement,
			RouteMapSummary:     placement.RouteMapSummaryString(),
			LocalShardIDs:       []uint64{appShardID},
			SingleShardFallback: true,
		}, nil
	}

	fileCfg, err := loadPlacementFileConfig(placementFile)
	if err != nil {
		return startupTopology{}, err
	}
	placement, err := routing.NewPlacement(fileCfg.routingPlacementConfig())
	if err != nil {
		return startupTopology{}, fmt.Errorf("SCRAP_SHARD_PLACEMENT_FILE: %w", err)
	}
	if cfg.SecurityMode == security.ModeProduction && len(placementShardSet(placement)) < 2 {
		return startupTopology{}, placementConfigError("production placement must configure at least two Shards")
	}
	localShardIDs, err := validateLocalShardIDs(placement, fileCfg.LocalShards)
	if err != nil {
		return startupTopology{}, err
	}
	return startupTopology{
		Placement:       placement,
		RouteMapSummary: placement.RouteMapSummaryString(),
		LocalShardIDs:   localShardIDs,
	}, nil
}

func loadShardPlacementFile(path string) (routing.Placement, error) {
	fileCfg, err := loadPlacementFileConfig(path)
	if err != nil {
		return routing.Placement{}, err
	}
	placement, err := routing.NewPlacement(fileCfg.routingPlacementConfig())
	if err != nil {
		return routing.Placement{}, fmt.Errorf("SCRAP_SHARD_PLACEMENT_FILE: %w", err)
	}
	return placement, nil
}

func loadPlacementFileConfig(path string) (placementFileConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Operator-configured Shard placement path is the intended startup gate input.
	if err != nil {
		return placementFileConfig{}, placementConfigError("read placement failed")
	}

	var cfg placementFileConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return placementFileConfig{}, placementConfigError("parse placement JSON: %v", err)
	}
	if err := rejectTrailingPlacementJSON(decoder); err != nil {
		return placementFileConfig{}, err
	}
	return cfg, nil
}

func placementConfigError(format string, args ...any) error {
	return fmt.Errorf("SCRAP_SHARD_PLACEMENT_FILE: %w: "+format, append([]any{routing.ErrInvalidPlacement}, args...)...)
}

func rejectTrailingPlacementJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return placementConfigError("parse placement JSON: multiple JSON values")
		}
		return placementConfigError("parse placement JSON: %v", err)
	}
	return nil
}

func (c placementFileConfig) routingPlacementConfig() routing.PlacementConfig {
	return routing.PlacementConfig{
		SlotCount: c.SlotCount,
		Shards:    append([]uint64(nil), c.Shards...),
		Ranges:    append([]routing.SlotRange(nil), c.Ranges...),
	}
}

func validateLocalShardIDs(placement routing.Placement, localShardIDs []uint64) ([]uint64, error) {
	if len(localShardIDs) == 0 {
		return nil, placementConfigError("local_shards must be present and non-empty")
	}
	knownShards := placementShardSet(placement)
	seen := make(map[uint64]struct{}, len(localShardIDs))
	ids := make([]uint64, 0, len(localShardIDs))
	for _, shardID := range localShardIDs {
		if _, ok := seen[shardID]; ok {
			return nil, placementConfigError("local_shards lists Shard %d more than once", shardID)
		}
		if _, ok := knownShards[shardID]; !ok {
			return nil, placementConfigError("local_shards references unknown Shard %d", shardID)
		}
		seen[shardID] = struct{}{}
		ids = append(ids, shardID)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids, nil
}

func placementShardSet(placement routing.Placement) map[uint64]struct{} {
	shards := make(map[uint64]struct{})
	for _, r := range placement.RouteMapSummary() {
		shards[r.ShardID] = struct{}{}
	}
	return shards
}

// persistPlacementIdentity stores a durable digest of the configured placement
// and rejects silent slot remaps that would move existing Transactions (ADR 0035).
func persistPlacementIdentity(dataDir string, topology startupTopology) error {
	digest := placementIdentityDigest(topology)
	path := filepath.Join(dataDir, placementIdentityFileName)
	existing, err := os.ReadFile(path) //nolint:gosec // Placement identity lives under the operator-configured data dir.
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read placement identity: %w", err)
		}
		return writePlacementIdentity(path, digest)
	}
	var record placementIdentityRecord
	if err := json.Unmarshal(existing, &record); err != nil {
		return placementConfigError("parse placement identity: %v", err)
	}
	if record.Digest == "" {
		return placementConfigError("placement identity digest is required")
	}
	if record.Digest != digest {
		return placementConfigError("placement identity mismatch: configured digest %s does not match persisted %s", digest, record.Digest)
	}
	return nil
}

func placementIdentityDigest(topology startupTopology) string {
	sum := sha256.Sum256([]byte(topology.RouteMapSummary))
	return hex.EncodeToString(sum[:])
}

func writePlacementIdentity(path, digest string) error {
	payload, err := json.Marshal(placementIdentityRecord{Digest: digest})
	if err != nil {
		return fmt.Errorf("marshal placement identity: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write placement identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist placement identity: %w", err)
	}
	return nil
}
