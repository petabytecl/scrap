package cmd

import (
	"strings"
	"testing"
)

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

func TestResolvePeersRejectsHalfSpecifiedClusterIdentity(t *testing.T) {
	// A pod that is meant to be clustered must not silently fall through to the
	// lone-node default when only one of the pair is set.
	t.Run("replicas without headless service", func(t *testing.T) {
		_, _, err := resolvePeers(Config{Replicas: 5})
		if err == nil {
			t.Fatal("expected error for SCRAP_REPLICAS without SCRAP_HEADLESS_SERVICE, got nil")
		}
		if !strings.Contains(err.Error(), "SCRAP_HEADLESS_SERVICE") {
			t.Fatalf("error = %v, want it to name SCRAP_HEADLESS_SERVICE", err)
		}
	})
	t.Run("headless service without replicas", func(t *testing.T) {
		_, _, err := resolvePeers(Config{HeadlessService: "scrap-headless"})
		if err == nil {
			t.Fatal("expected error for SCRAP_HEADLESS_SERVICE without SCRAP_REPLICAS, got nil")
		}
		if !strings.Contains(err.Error(), "SCRAP_REPLICAS") {
			t.Fatalf("error = %v, want it to name SCRAP_REPLICAS", err)
		}
	})
}

func TestResolveClientAddrsForK8sUseClientPort(t *testing.T) {
	peers := map[uint64]string{
		1: "scrapd-0.scrap-headless.scrap.svc:9091",
		2: "scrapd-1.scrap-headless.scrap.svc:9091",
		3: "scrapd-2.scrap-headless.scrap.svc:9091",
	}

	addrs := resolveClientAddrs(Config{
		ListenAddr:      ":9090",
		Replicas:        3,
		HeadlessService: "scrap-headless",
		Namespace:       "scrap",
	}, peers)

	if got := addrs[1]; got != "scrapd-0.scrap-headless.scrap.svc:9090" {
		t.Fatalf("client addr 1 = %q, want client port", got)
	}
	if got := peers[1]; got != "scrapd-0.scrap-headless.scrap.svc:9091" {
		t.Fatalf("peer addr 1 mutated to %q", got)
	}
}

func TestResolveClientAddrsFallbackCopiesPeerAddrs(t *testing.T) {
	peers := map[uint64]string{1: "localhost:9091"}

	addrs := resolveClientAddrs(Config{ListenAddr: "not-a-host-port"}, peers)
	addrs[1] = "changed"

	if got := peers[1]; got != "localhost:9091" {
		t.Fatalf("peer addr mutated to %q", got)
	}
}
