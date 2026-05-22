.PHONY: proto
.PHONY: proto-check
.PHONY: fmt-check
.PHONY: lint
.PHONY: test
.PHONY: test-race
.PHONY: build
.PHONY: check
.PHONY: spike-write-path
.PHONY: spike-write-path-raft
.PHONY: spike-write-path-raft-durable
.PHONY: spike-write-path-raft-cluster

GO ?= go
BUF ?= buf
PROTO_BREAKING_REF ?= main
PROTO_BREAKING_AGAINST ?= .git#branch=$(PROTO_BREAKING_REF)

proto:
	$(BUF) generate

proto-check:
	$(BUF) lint
	@if git cat-file -e "$(PROTO_BREAKING_REF):buf.yaml" 2>/dev/null && git cat-file -e "$(PROTO_BREAKING_REF):proto" 2>/dev/null; then \
		$(BUF) breaking --against "$(PROTO_BREAKING_AGAINST)"; \
	else \
		echo "buf breaking skipped: $(PROTO_BREAKING_REF) has no proto module yet"; \
	fi
	$(BUF) generate
	git diff --exit-code -- internal/gen

fmt-check:
	@test -z "$$(gofmt -l .)"

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

build:
	$(GO) build ./cmd/scrapd ./cmd/scrap-spike

check: fmt-check proto-check lint test test-race build

spike-write-path:
	$(GO) run ./cmd/scrap-spike

spike-write-path-raft:
	$(GO) run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable:
	$(GO) run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster:
	$(GO) run ./cmd/scrap-spike -raft-cluster-barrier
