.PHONY: build test lint proto e2e-setup e2e

build:
	go build ./...

test:
	go test ./... -timeout 60s

lint:
	buf lint
	go vet ./...

proto:
	buf generate

e2e-setup:
	./test/e2e/setup-kind.sh

e2e: e2e-setup
	SCRAP_E2E=1 go test ./test/e2e/ -v -timeout 300s
