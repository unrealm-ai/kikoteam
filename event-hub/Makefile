GO ?= go
.DEFAULT_GOAL := build
include ../tools/lint.mk
VERSION ?= dev
OPENAPI_VALIDATOR ?= .venv/bin/openapi-spec-validator

.PHONY: build fmt check-fmt vet lint test test-integration schema check-schema check-history check-goose check-contracts check

build:
	$(GO) build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/event-hub ./cmd/event-hub

fmt:
	gofmt -w cmd internal database/postgres/*.go

check-fmt:
	@files="$$(gofmt -l ../shared/trigger cmd internal database/postgres/*.go third_party/goose/provider.go third_party/goose/provider_run.go third_party/goose/provider_conn.go)" || exit $$?; test -z "$$files" || { echo "$$files"; exit 1; }

lint: check-fmt check-goose check-contracts $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) config verify --config ../.golangci.yml
	$(GOLANGCI_LINT) run --config ../.golangci.yml ./...
	cd ../shared/trigger && $(GOLANGCI_LINT) run --config ../../.golangci.yml ./...

vet:
	$(GO) vet ./...

test:
	cd ../shared/trigger && $(GO) test -race ./...
	$(GO) test -race -timeout 120s ./...

test-integration:
	cd ../shared/trigger && $(GO) test -race ./...
	EVENT_HUB_REQUIRE_PG=1 $(GO) test -race -count=1 -timeout 120s ./...

schema:
	$(GO) run ./cmd/schema

check-schema:
	$(GO) run ./cmd/schema -check

check-history:
	python3 scripts/check_migration_history.py "$(BASE_REF)"

check-goose:
	python3 scripts/check_goose.py

check-contracts:
	$(OPENAPI_VALIDATOR) contracts/openapi.yaml

check: lint test-integration check-schema
