package raft

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseOrdinal(hostname string) (int, error) {
	idx := strings.LastIndex(hostname, "-")
	if idx < 0 || idx == len(hostname)-1 {
		return 0, fmt.Errorf("raft: hostname %q has no ordinal suffix", hostname)
	}
	n, err := strconv.Atoi(hostname[idx+1:])
	if err != nil {
		return 0, fmt.Errorf("raft: hostname %q ordinal not a number: %w", hostname, err)
	}
	return n, nil
}

func OrdinalToRaftID(ordinal int) uint64 {
	return uint64(ordinal) + 1
}

func BuildK8sPeers(replicas int, headlessService, namespace string, peerPort int) map[uint64]string {
	peers := make(map[uint64]string, replicas)
	for i := range replicas {
		id := OrdinalToRaftID(i)
		addr := fmt.Sprintf("scrapd-%d.%s.%s.svc:%d", i, headlessService, namespace, peerPort)
		peers[id] = addr
	}
	return peers
}

func ParsePeersFlag(flag string) (map[uint64]string, error) {
	if flag == "" {
		return nil, fmt.Errorf("raft: empty peers flag")
	}

	peers := make(map[uint64]string)
	for _, part := range strings.Split(flag, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("raft: invalid peer entry %q (want id=host:port)", part)
		}
		id, err := strconv.ParseUint(kv[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("raft: invalid peer id %q: %w", kv[0], err)
		}
		if id == 0 {
			return nil, fmt.Errorf("raft: peer id 0 is reserved")
		}
		peers[id] = kv[1]
	}
	return peers, nil
}
