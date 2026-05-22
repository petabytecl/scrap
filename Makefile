.PHONY: proto
.PHONY: proto-check
.PHONY: spike-write-path
.PHONY: spike-write-path-raft
.PHONY: spike-write-path-raft-durable
.PHONY: spike-write-path-raft-cluster

PROTO_BREAKING_REF ?= main
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)

proto:
	buf generate

proto-check:
	buf lint
	@if git cat-file -e "$(PROTO_BREAKING_REF):buf.yaml" 2>/dev/null && git cat-file -e "$(PROTO_BREAKING_REF):proto" 2>/dev/null; then \
		buf breaking --against "$(PROTO_BREAKING_AGAINST)"; \
	else \
		echo "buf breaking skipped: $(PROTO_BREAKING_REF) has no proto module yet"; \
	fi
	buf generate
	git diff --exit-code -- internal/gen

spike-write-path:
	go run ./cmd/scrap-spike

spike-write-path-raft:
	go run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable:
	go run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster:
	go run ./cmd/scrap-spike -raft-cluster-barrier
