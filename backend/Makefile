GO ?= go
.DEFAULT_GOAL := build
include ../tools/lint.mk
VERSION ?= dev
# Each integration case replays isolated PostgreSQL migrations; the repository
# suite now has more than 150 cases. Keep a bounded per-package budget.
TEST_TIMEOUT ?= 300s
OPENAPI_VALIDATOR ?= ../event-hub/.venv/bin/openapi-spec-validator

.PHONY: build fmt check-fmt vet lint test test-integration check-contracts check

build:
	$(GO) build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/backend ./cmd/backend

fmt:
	gofmt -w cmd internal contracts/taskdelegation

check-fmt:
	@files="$$(gofmt -l ../shared/trigger cmd internal contracts/taskdelegation)" || exit $$?; test -z "$$files" || { echo "$$files"; exit 1; }

vet:
	$(GO) vet ./...

lint: check-fmt check-contracts $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) config verify --config ../.golangci.yml
	$(GOLANGCI_LINT) run --config ../.golangci.yml ./...
	cd ../shared/trigger && $(GOLANGCI_LINT) run --config ../../.golangci.yml ./...

test:
	cd ../shared/trigger && $(GO) test -race ./...
	$(GO) test -race -timeout $(TEST_TIMEOUT) ./...

test-integration:
	cd ../shared/trigger && $(GO) test -race ./...
	@test -n "$(BACKEND_TEST_DATABASE_URL)" || { echo "BACKEND_TEST_DATABASE_URL is required"; exit 1; }
	$(GO) test -race -count=1 -timeout $(TEST_TIMEOUT) ./...

check-contracts:
	$(OPENAPI_VALIDATOR) contracts/openapi.yaml
	$(OPENAPI_VALIDATOR) contracts/internal/openapi.yaml
	$(OPENAPI_VALIDATOR) contracts/model-proxy/openapi.yaml

check: lint test-integration
