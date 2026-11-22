.DEFAULT_GOAL := check
OPENAPI_VALIDATOR ?= ../../event-hub/.venv/bin/openapi-spec-validator

.PHONY: lint check check-contracts build test fmt

lint: check-contracts
	npm run check:static

check: check-contracts
	npm run check

check-contracts:
	$(OPENAPI_VALIDATOR) contracts/management.openapi.yaml

build:
	npm run build

test:
	npm run test:process

fmt:
	npm run fmt
