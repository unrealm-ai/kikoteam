GO ?= go
.DEFAULT_GOAL := check
include ../tools/lint.mk
OPENAPI_VALIDATOR ?= ../event-hub/.venv/bin/openapi-spec-validator
.PHONY: build fmt check-fmt check-contracts lint test check
build:
	$(GO) build -trimpath -o bin/sandbox-runner ./cmd/sandbox-runner
fmt:
	gofmt -w cmd internal
check-fmt:
	@files="$$(gofmt -l cmd internal)"; test -z "$$files" || { echo "$$files"; exit 1; }
check-contracts:
	$(OPENAPI_VALIDATOR) contracts/openapi.yaml
lint: check-fmt check-contracts $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --config ../.golangci.yml ./cmd/... ./internal/...
test:
	$(GO) test -race -count=1 -timeout 120s ./cmd/... ./internal/...
check: lint test
