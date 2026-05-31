package shard

import "testing"

func TestLeaderClientAddrPrefersClientAddress(t *testing.T) {
	s := &Shard{
		peers: map[uint64]string{
			1: "scrapd-0.scrap-headless.scrap.svc:9091",
		},
		clientAddrs: map[uint64]string{
			1: "scrapd-0.scrap-headless.scrap.svc:9090",
		},
	}

	if got := s.leaderClientAddr(1); got != "scrapd-0.scrap-headless.scrap.svc:9090" {
		t.Fatalf("leader client addr = %q, want client port", got)
	}
}

func TestLeaderClientAddrFallsBackToPeerAddress(t *testing.T) {
	s := &Shard{
		peers: map[uint64]string{
			1: "localhost:9091",
		},
	}

	if got := s.leaderClientAddr(1); got != "localhost:9091" {
		t.Fatalf("leader client addr = %q, want peer fallback", got)
	}
}
