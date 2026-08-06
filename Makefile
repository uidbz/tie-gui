GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all install submodule imgview tieview clean

all: install

# Ensure the vendored fyne fork submodule is checked out before building.
submodule:
	git submodule update --init --recursive

install: submodule
	go install ./cmd/imgview ./cmd/tieview
	@echo "Installed imgview and tieview to $(GOBIN)"

imgview: submodule
	go install ./cmd/imgview
	@echo "Installed imgview to $(GOBIN)"

tieview: submodule
	go install ./cmd/tieview
	@echo "Installed tieview to $(GOBIN)"

clean:
	rm -f $(GOBIN)/imgview $(GOBIN)/tieview
