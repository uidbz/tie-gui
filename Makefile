GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

APPS := imgview tie-view tie-fm tie-audio-player

.PHONY: all install submodule $(APPS) test clean

all: install

# Ensure the vendored fyne fork submodule is checked out before building.
submodule:
	git submodule update --init --recursive

install: submodule
	go install $(addprefix ./cmd/,$(APPS))
	@echo "Installed $(APPS) to $(GOBIN)"

$(APPS): submodule
	go install ./cmd/$@
	@echo "Installed $@ to $(GOBIN)"

test:
	go test ./...

clean:
	rm -f $(addprefix $(GOBIN)/,$(APPS))
