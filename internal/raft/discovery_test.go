package raft_test

import (
	"testing"

	scrapraft "github.com/petabytecl/scrap/internal/raft"
)

func TestParseOrdinalFromHostname(t *testing.T) {
	tests := []struct {
		hostname string
		ordinal  int
		wantErr  bool
	}{
		{"scrapd-0", 0, false},
		{"scrapd-2", 2, false},
		{"scrapd-99", 99, false},
		{"badname", 0, true},
		{"scrapd-", 0, true},
		{"scrapd-abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			ord, err := scrapraft.ParseOrdinal(tt.hostname)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.hostname)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ord != tt.ordinal {
				t.Fatalf("ordinal: got %d, want %d", ord, tt.ordinal)
			}
		})
	}
}

func TestOrdinalToRaftID(t *testing.T) {
	if scrapraft.OrdinalToRaftID(0) != 1 {
		t.Fatalf("ordinal 0 should be raft ID 1")
	}
	if scrapraft.OrdinalToRaftID(2) != 3 {
		t.Fatalf("ordinal 2 should be raft ID 3")
	}
}

func TestBuildPeerList(t *testing.T) {
	peers := scrapraft.BuildK8sPeers(3, "scrap-headless", "default", 9091)

	if len(peers) != 3 {
		t.Fatalf("peer count: got %d, want 3", len(peers))
	}

	want := map[uint64]string{
		1: "scrapd-0.scrap-headless.default.svc:9091",
		2: "scrapd-1.scrap-headless.default.svc:9091",
		3: "scrapd-2.scrap-headless.default.svc:9091",
	}

	for id, addr := range want {
		if peers[id] != addr {
			t.Fatalf("peer %d: got %q, want %q", id, peers[id], addr)
		}
	}
}

func TestParsePeersFlag(t *testing.T) {
	peers, err := scrapraft.ParsePeersFlag("1=localhost:9091,2=localhost:9092,3=localhost:9093")
	if err != nil {
		t.Fatalf("ParsePeersFlag: %v", err)
	}

	if len(peers) != 3 {
		t.Fatalf("peer count: got %d, want 3", len(peers))
	}
	if peers[1] != "localhost:9091" {
		t.Fatalf("peer 1: got %q", peers[1])
	}
	if peers[3] != "localhost:9093" {
		t.Fatalf("peer 3: got %q", peers[3])
	}
}

func TestParsePeersFlagInvalid(t *testing.T) {
	tests := []string{
		"",
		"invalid",
		"1=host,bad",
		"0=localhost:9091",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := scrapraft.ParsePeersFlag(input)
			if err == nil {
				t.Fatalf("expected error for %q", input)
			}
		})
	}
}
