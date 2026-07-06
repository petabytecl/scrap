package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/routing"
)

func TestPlacementRoutesTransactionsWithStableHashSlots(t *testing.T) {
	placement, err := routing.NewPlacement(routing.PlacementConfig{
		SlotCount: routing.SlotCount,
		Shards:    []uint64{7, 9},
		Ranges: []routing.SlotRange{
			{ShardID: 7, StartSlot: 0, EndSlot: 511},
			{ShardID: 9, StartSlot: 512, EndSlot: 1023},
		},
	})
	if err != nil {
		t.Fatalf("NewPlacement: %v", err)
	}

	tests := []struct {
		transactionID string
		wantSlot      uint16
		wantShardID   uint64
	}{
		{transactionID: "tx-alpha", wantSlot: 88, wantShardID: 7},
		{transactionID: "tx-bravo", wantSlot: 592, wantShardID: 9},
		{transactionID: "transaction-999", wantSlot: 971, wantShardID: 9},
	}
	for _, tt := range tests {
		t.Run(tt.transactionID, func(t *testing.T) {
			assertTransactionRoute(t, placement, tt.transactionID, tt.wantSlot, tt.wantShardID)
		})
	}
}

func TestSlotForTransactionAcceptsExactIdentifiers(t *testing.T) {
	// The public contract preserves identifiers exactly (no trimming or
	// normalization), so a whitespace-only Transaction ID must route.
	if _, err := routing.SlotForTransaction(" "); err != nil {
		t.Fatalf("SlotForTransaction(%q) = %v, want routable", " ", err)
	}
	if _, err := routing.SlotForTransaction(""); !errors.Is(err, routing.ErrInvalidTransaction) {
		t.Fatalf("SlotForTransaction(\"\") error = %v, want ErrInvalidTransaction", err)
	}
}

func assertTransactionRoute(t *testing.T, placement routing.Placement, transactionID string, wantSlot uint16, wantShardID uint64) {
	t.Helper()
	slot, err := routing.SlotForTransaction(transactionID)
	if err != nil {
		t.Fatalf("SlotForTransaction: %v", err)
	}
	if slot != wantSlot {
		t.Fatalf("SlotForTransaction() = %d, want %d", slot, wantSlot)
	}

	route, err := placement.Lookup(transactionID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if route.Slot != wantSlot {
		t.Errorf("route slot = %d, want %d", route.Slot, wantSlot)
	}
	if route.ShardID != wantShardID {
		t.Errorf("route ShardID = %d, want %d", route.ShardID, wantShardID)
	}
}

func TestPlacementValidatesSlotCoverageAndShardOwnership(t *testing.T) {
	tests := []struct {
		name string
		cfg  routing.PlacementConfig
	}{
		{
			name: "missing slot",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0},
				Ranges:    []routing.SlotRange{{ShardID: 0, StartSlot: 0, EndSlot: 1022}},
			},
		},
		{
			name: "overlapping slot",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0, 1},
				Ranges: []routing.SlotRange{
					{ShardID: 0, StartSlot: 0, EndSlot: 511},
					{ShardID: 1, StartSlot: 511, EndSlot: 1023},
				},
			},
		},
		{
			name: "out of range",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0},
				Ranges:    []routing.SlotRange{{ShardID: 0, StartSlot: 0, EndSlot: 1024}},
			},
		},
		{
			name: "reversed range",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0},
				Ranges:    []routing.SlotRange{{ShardID: 0, StartSlot: 7, EndSlot: 6}},
			},
		},
		{
			name: "empty shard set",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Ranges:    []routing.SlotRange{{ShardID: 0, StartSlot: 0, EndSlot: 1023}},
			},
		},
		{
			name: "unknown shard",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0},
				Ranges:    []routing.SlotRange{{ShardID: 1, StartSlot: 0, EndSlot: 1023}},
			},
		},
		{
			name: "duplicate shard",
			cfg: routing.PlacementConfig{
				SlotCount: routing.SlotCount,
				Shards:    []uint64{0, 0},
				Ranges:    []routing.SlotRange{{ShardID: 0, StartSlot: 0, EndSlot: 1023}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := routing.NewPlacement(tt.cfg)
			if !errors.Is(err, routing.ErrInvalidPlacement) {
				t.Fatalf("NewPlacement() error = %v, want ErrInvalidPlacement", err)
			}
		})
	}
}

func TestPlacementRouteMapSummaryIsDeterministicAndImmutable(t *testing.T) {
	cfg := routing.PlacementConfig{
		SlotCount: routing.SlotCount,
		Shards:    []uint64{7, 9},
		Ranges: []routing.SlotRange{
			{ShardID: 9, StartSlot: 512, EndSlot: 1023},
			{ShardID: 7, StartSlot: 0, EndSlot: 511},
		},
	}
	placement, err := routing.NewPlacement(cfg)
	if err != nil {
		t.Fatalf("NewPlacement: %v", err)
	}

	cfg.Shards[0] = 99
	cfg.Ranges[0].ShardID = 99
	got := placement.RouteMapSummary()
	want := []routing.SlotRange{
		{ShardID: 7, StartSlot: 0, EndSlot: 511},
		{ShardID: 9, StartSlot: 512, EndSlot: 1023},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RouteMapSummary() = %#v, want %#v", got, want)
	}
	if summary := placement.RouteMapSummaryString(); summary != "0-511:shard=7,512-1023:shard=9" {
		t.Fatalf("RouteMapSummaryString() = %q", summary)
	}

	got[0].ShardID = 99
	again := placement.RouteMapSummary()
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("RouteMapSummary() mutated by caller = %#v, want %#v", again, want)
	}
}

func TestRouterRecordsBoundedLookupTelemetry(t *testing.T) {
	placement, err := routing.NewPlacement(routing.PlacementConfig{
		SlotCount: routing.SlotCount,
		Shards:    []uint64{7, 9},
		Ranges: []routing.SlotRange{
			{ShardID: 7, StartSlot: 0, EndSlot: 511},
			{ShardID: 9, StartSlot: 512, EndSlot: 1023},
		},
	})
	if err != nil {
		t.Fatalf("NewPlacement: %v", err)
	}

	recorder := &recordingLookupRecorder{}
	router := routing.NewRouter(placement, routing.WithLookupRecorder(recorder))

	route, err := router.Lookup(context.Background(), "doc-secret-tenant-a")
	if err != nil {
		t.Fatalf("Lookup routed transaction: %v", err)
	}
	if route.ShardID != 7 {
		t.Fatalf("route ShardID = %d, want 7", route.ShardID)
	}

	_, err = router.Lookup(context.Background(), "")
	if !errors.Is(err, routing.ErrInvalidTransaction) {
		t.Fatalf("Lookup empty transaction error = %v, want ErrInvalidTransaction", err)
	}

	want := []routing.LookupRecord{
		{Outcome: routing.LookupOutcomeRouted, Reason: routing.LookupReasonMatched, ShardID: 7, ShardIDValid: true},
		{Outcome: routing.LookupOutcomeRejected, Reason: routing.LookupReasonInvalidTransaction},
	}
	if !reflect.DeepEqual(recorder.records, want) {
		t.Fatalf("records = %#v, want %#v", recorder.records, want)
	}
	if recorder.records[1].ShardIDValid {
		t.Fatalf("rejected record marked ShardID valid: %#v", recorder.records[1])
	}

	rendered := fmt.Sprint(recorder.records)
	for _, forbidden := range []string{"doc-secret-tenant-a", "doc-secret", "tenant-a"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("lookup telemetry record leaked %q in %q", forbidden, rendered)
		}
	}
}

type recordingLookupRecorder struct {
	records []routing.LookupRecord
}

func (r *recordingLookupRecorder) RecordRoutingLookup(_ context.Context, record routing.LookupRecord) {
	r.records = append(r.records, record)
}

func TestPlacementJSONRejectsMissingZeroValuedRangeFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing Shard ID",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"start_slot":0,"end_slot":1023}]}`,
		},
		{
			name: "null start slot",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"shard_id":0,"start_slot":null,"end_slot":1023}]}`,
		},
		{
			name: "missing end slot",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"shard_id":0,"start_slot":0}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg routing.PlacementConfig
			err := json.Unmarshal([]byte(tt.body), &cfg)
			if !errors.Is(err, routing.ErrInvalidPlacement) {
				t.Fatalf("Unmarshal error = %v, want ErrInvalidPlacement", err)
			}
		})
	}
}
