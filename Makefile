.PHONY: spike-write-path
.PHONY: spike-write-path-raft
.PHONY: spike-write-path-raft-durable
.PHONY: spike-write-path-raft-cluster

spike-write-path:
	go run ./cmd/scrap-spike

spike-write-path-raft:
	go run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable:
	go run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster:
	go run ./cmd/scrap-spike -raft-cluster-barrier
