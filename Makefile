GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all install submodule imgview tie-view clean

all: install

# Ensure the vendored fyne fork submodule is checked out before building.
submodule:
	git submodule update --init --recursive

install: submodule
	go install ./cmd/imgview ./cmd/tie-view
	@echo "Installed imgview and tie-view to $(GOBIN)"

imgview: submodule
	go install ./cmd/imgview
	@echo "Installed imgview to $(GOBIN)"

tie-view: submodule
	go install ./cmd/tie-view
	@echo "Installed tie-view to $(GOBIN)"

clean:
	rm -f $(GOBIN)/imgview $(GOBIN)/tie-view
