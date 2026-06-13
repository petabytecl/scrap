package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PlacementConfig is the JSON-serializable routing placement input.
type PlacementConfig struct {
	SlotCount int         `json:"slot_count"`
	Shards    []uint64    `json:"shards"`
	Ranges    []SlotRange `json:"ranges"`
}

// SlotRange assigns an inclusive slot range to a known Shard.
type SlotRange struct {
	ShardID   uint64 `json:"shard_id"`
	StartSlot int    `json:"start_slot"`
	EndSlot   int    `json:"end_slot"`
}

// UnmarshalJSON rejects omitted or null fields even when zero is a valid value.
func (r *SlotRange) UnmarshalJSON(data []byte) error {
	var raw struct {
		ShardID   *uint64 `json:"shard_id"`
		StartSlot *int    `json:"start_slot"`
		EndSlot   *int    `json:"end_slot"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	switch {
	case raw.ShardID == nil:
		return invalidPlacement("slot range requires shard_id")
	case raw.StartSlot == nil:
		return invalidPlacement("slot range requires start_slot")
	case raw.EndSlot == nil:
		return invalidPlacement("slot range requires end_slot")
	}
	*r = SlotRange{
		ShardID:   *raw.ShardID,
		StartSlot: *raw.StartSlot,
		EndSlot:   *raw.EndSlot,
	}
	return nil
}

// Route is bounded route metadata for a Transaction lookup.
type Route struct {
	Slot    uint16
	ShardID uint64
}

// Placement is an immutable slot-to-Shard map.
type Placement struct {
	slots  [SlotCount]uint64
	seen   [SlotCount]bool
	ranges []SlotRange
}

// SingleShardConfig returns a complete placement for local development and
// tests that intentionally run one Shard.
func SingleShardConfig(shardID uint64) PlacementConfig {
	return PlacementConfig{
		SlotCount: SlotCount,
		Shards:    []uint64{shardID},
		Ranges:    []SlotRange{{ShardID: shardID, StartSlot: 0, EndSlot: SlotCount - 1}},
	}
}

// NewPlacement validates and freezes a slot-to-Shard placement.
func NewPlacement(cfg PlacementConfig) (Placement, error) {
	if cfg.SlotCount != SlotCount {
		return Placement{}, invalidPlacement("slot_count must be %d", SlotCount)
	}
	if len(cfg.Shards) == 0 {
		return Placement{}, invalidPlacement("at least one Shard is required")
	}
	if len(cfg.Ranges) == 0 {
		return Placement{}, invalidPlacement("at least one slot range is required")
	}

	shards, err := knownShards(cfg.Shards)
	if err != nil {
		return Placement{}, err
	}

	ranges := append([]SlotRange(nil), cfg.Ranges...)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].StartSlot == ranges[j].StartSlot {
			return ranges[i].EndSlot < ranges[j].EndSlot
		}
		return ranges[i].StartSlot < ranges[j].StartSlot
	})

	placement := Placement{
		ranges: ranges,
	}
	for _, r := range ranges {
		if err := placement.addRange(shards, r); err != nil {
			return Placement{}, err
		}
	}
	for slot := range SlotCount {
		if !placement.seen[slot] {
			return Placement{}, invalidPlacement("slot %d has no owning Shard", slot)
		}
	}
	return placement, nil
}

func knownShards(shards []uint64) (map[uint64]struct{}, error) {
	known := make(map[uint64]struct{}, len(shards))
	for _, shardID := range shards {
		if _, exists := known[shardID]; exists {
			return nil, invalidPlacement("Shard %d is listed more than once", shardID)
		}
		known[shardID] = struct{}{}
	}
	return known, nil
}

func (p *Placement) addRange(shards map[uint64]struct{}, r SlotRange) error {
	if _, exists := shards[r.ShardID]; !exists {
		return invalidPlacement("slot range references unknown Shard %d", r.ShardID)
	}
	if r.StartSlot < 0 || r.EndSlot < 0 || r.StartSlot >= SlotCount || r.EndSlot >= SlotCount {
		return invalidPlacement("slot range %d-%d is outside 0-%d", r.StartSlot, r.EndSlot, SlotCount-1)
	}
	if r.StartSlot > r.EndSlot {
		return invalidPlacement("slot range %d-%d is reversed", r.StartSlot, r.EndSlot)
	}
	for slot := r.StartSlot; slot <= r.EndSlot; slot++ {
		if p.seen[slot] {
			return invalidPlacement("slot %d has duplicate ownership", slot)
		}
		p.seen[slot] = true
		p.slots[slot] = r.ShardID
	}
	return nil
}

// Lookup returns the owning Shard route for a Transaction identifier.
func (p Placement) Lookup(transactionID string) (Route, error) {
	slot, err := SlotForTransaction(transactionID)
	if err != nil {
		return Route{}, err
	}
	if !p.seen[slot] {
		return Route{}, fmt.Errorf("%w: slot has no owning Shard", ErrRouteNotFound)
	}
	return Route{Slot: slot, ShardID: p.slots[slot]}, nil
}

// RoutesOnlyToShard reports whether every slot routes to the given Shard.
func (p Placement) RoutesOnlyToShard(shardID uint64) bool {
	if len(p.ranges) == 0 {
		return false
	}
	for slot := range SlotCount {
		if !p.seen[slot] || p.slots[slot] != shardID {
			return false
		}
	}
	return true
}

// RouteMapSummary returns a stable copy of the configured slot ranges.
func (p Placement) RouteMapSummary() []SlotRange {
	return append([]SlotRange(nil), p.ranges...)
}

// RouteMapSummaryString returns a deterministic, bounded route-map summary.
func (p Placement) RouteMapSummaryString() string {
	ranges := p.RouteMapSummary()
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		parts = append(parts, fmt.Sprintf("%d-%d:shard=%d", r.StartSlot, r.EndSlot, r.ShardID))
	}
	return strings.Join(parts, ",")
}
