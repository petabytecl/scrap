package main

import "testing"

func TestResolvePeersDefaultSingleNode(t *testing.T) {
	peers, raftID, err := resolvePeers(Config{})
	if err != nil {
		t.Fatalf("resolvePeers: %v", err)
	}
	if raftID != 1 {
		t.Errorf("raftID = %d, want 1", raftID)
	}
	if len(peers) != 1 || peers[1] != "localhost:9091" {
		t.Errorf("peers = %v, want {1: localhost:9091}", peers)
	}
}

func TestResolvePeersFromFlag(t *testing.T) {
	peers, _, err := resolvePeers(Config{PeersFlag: "1=localhost:9091,2=localhost:9092"})
	if err != nil {
		t.Fatalf("resolvePeers: %v", err)
	}
	if len(peers) != 2 {
		t.Errorf("peers = %v, want 2 entries", peers)
	}
}

func TestResolvePeersInvalidFlag(t *testing.T) {
	if _, _, err := resolvePeers(Config{PeersFlag: "not-valid"}); err == nil {
		t.Fatal("expected error for invalid --peers, got nil")
	}
}
