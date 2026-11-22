GO ?= go
.DEFAULT_GOAL := check
include ../tools/lint.mk
OPENAPI_VALIDATOR ?= ../event-hub/.venv/bin/openapi-spec-validator

.PHONY: build fmt check-fmt check-contracts lint test check
build:
	$(GO) build -trimpath -o bin/agent-engine ./cmd/agent-engine
fmt:
	gofmt -w cmd internal
check-fmt:
	@files="$$(gofmt -l cmd internal)"; test -z "$$files" || { echo "$$files"; exit 1; }
lint: check-fmt check-contracts $(GOLANGCI_LINT)
	node --check extensions/capability-policy.mjs
	node --check extensions/context-receipt.mjs
	node --check extensions/human-tools.mjs
	$(GOLANGCI_LINT) run --config ../.golangci.yml ./...
check-contracts:
	$(OPENAPI_VALIDATOR) contracts/openapi.yaml
test:
	@test -x node_modules/.bin/pi || { echo 'Run npm ci in agent-engine first'; exit 1; }
	node --test extensions/*.test.mjs
	$(GO) test -race -count=1 -timeout 120s ./...
check: lint test
