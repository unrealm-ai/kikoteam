.DEFAULT_GOAL := lint
include tools/lint.mk

COMPONENTS := backend event-hub agent-engine sandbox-runner sandbox-runner/builtin-mcp
SELECTED := $(if $(COMPONENT),$(COMPONENT),$(COMPONENTS))
ifneq ($(filter-out $(COMPONENTS),$(SELECTED)),)
$(error Unknown or not yet implemented COMPONENT: $(filter-out $(COMPONENTS),$(SELECTED)))
endif

.PHONY: lint fmt check lint-workflows tools $(addprefix lint-,$(COMPONENTS)) $(addprefix fmt-,$(COMPONENTS)) $(addprefix check-,$(COMPONENTS))

tools: $(GOLANGCI_LINT) $(ACTIONLINT)

lint: $(if $(COMPONENT),,lint-workflows) $(addprefix lint-,$(SELECTED))
fmt: $(addprefix fmt-,$(SELECTED))
check: $(if $(COMPONENT),,lint-workflows) $(addprefix check-,$(SELECTED))

lint-workflows: $(ACTIONLINT)
	$(ACTIONLINT) -shellcheck= -pyflakes=

$(addprefix lint-,$(COMPONENTS)):
	$(MAKE) -C $(@:lint-%=%) lint

$(addprefix fmt-,$(COMPONENTS)):
	$(MAKE) -C $(@:fmt-%=%) fmt

$(addprefix check-,$(COMPONENTS)):
	$(MAKE) -C $(@:check-%=%) check
