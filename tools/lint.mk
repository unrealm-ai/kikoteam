LINT_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
include $(LINT_ROOT)/tools/versions.mk
GO ?= go
GOLANGCI_LINT := $(LINT_ROOT)/.tools/golangci-lint-$(GOLANGCI_LINT_VERSION)/golangci-lint
ACTIONLINT := $(LINT_ROOT)/.tools/actionlint-$(ACTIONLINT_VERSION)/actionlint

$(GOLANGCI_LINT):
	mkdir -p "$(@D)"
	GOBIN="$(@D)" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(ACTIONLINT):
	mkdir -p "$(@D)"
	GOBIN="$(@D)" $(GO) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)
