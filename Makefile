.PHONY: proto
.PHONY: proto-check
.PHONY: fmt-check
.PHONY: lint
.PHONY: vuln
.PHONY: test
.PHONY: test-compat
.PHONY: test-race
.PHONY: build
.PHONY: check
.PHONY: release-check
.PHONY: spike-write-path
.PHONY: spike-write-path-raft
.PHONY: spike-write-path-raft-durable
.PHONY: spike-write-path-raft-cluster

GO ?= go
BUF ?= buf
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.3.0
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
	$(GOLANGCI_LINT) run --timeout=5m

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

test:
	$(GO) test ./...

test-compat:
	$(GO) test ./internal/compat

test-race:
	$(GO) test -race ./...

build:
	$(GO) build ./cmd/scrapd ./cmd/scrap-spike ./cmd/scrapctl ./cmd/scrap-release-gate

check: fmt-check proto-check test-compat lint test test-race build

release-check:
	@test -n "$(RELEASE_EVIDENCE_MANIFEST)" || (echo "RELEASE_EVIDENCE_MANIFEST is required" >&2; exit 2)
	$(GO) run ./cmd/scrap-release-gate --tier release --manifest "$(RELEASE_EVIDENCE_MANIFEST)"

spike-write-path:
	$(GO) run ./cmd/scrap-spike

spike-write-path-raft:
	$(GO) run ./cmd/scrap-spike -raft-barrier

spike-write-path-raft-durable:
	$(GO) run ./cmd/scrap-spike -raft-durable-barrier

spike-write-path-raft-cluster:
	$(GO) run ./cmd/scrap-spike -raft-cluster-barrier
