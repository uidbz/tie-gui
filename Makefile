GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

APPS := imgview tie-view tie-fm tie-audio

# tie requires a Go newer than some distro packages ship. Fetch the toolchain
# go.mod asks for on demand, unless the caller pinned one themselves (GOTOOLCHAIN=local
# in the environment wins).
export GOTOOLCHAIN ?= auto

# Fyne targets the X11 driver by default on Linux; on a pure Wayland session
# (WAYLAND_DISPLAY set, no Xwayland DISPLAY) the apps need the Wayland driver
# and GLES/EGL tags. A bare XDG_SESSION_TYPE=wayland check is not enough here:
# XWayland may serve X11 apps, in which case the X11 tags remain correct.
PURE_WAYLAND := $(shell sh -c '[ -n "$$WAYLAND_DISPLAY" ] && [ -z "$$DISPLAY" ] && echo yes')
GOTAGS :=
ifeq ($(PURE_WAYLAND),yes)
GOTAGS := -tags "wayland egl gles gles2"
endif

.PHONY: all install submodule $(APPS) test race vet fmtcheck lint check clean

all: install

# Ensure the vendored fyne fork submodule is checked out before building.
submodule:
	git submodule update --init --recursive

install: submodule
	go install $(GOTAGS) $(addprefix ./cmd/,$(APPS))
	@echo "Installed $(APPS) to $(GOBIN)"

$(APPS): submodule
	go install $(GOTAGS) ./cmd/$@
	@echo "Installed $@ to $(GOBIN)"

test:
	go test $(GOTAGS) ./...

# The race detector is the guard every threading bug in docs/code-review.md
# would have caught, and the tree is clean under it, so it is not optional.
race:
	go test -race -count=1 $(GOTAGS) ./...

vet:
	go vet ./...

fmtcheck:
	@out=$$(gofmt -l . 2>/dev/null | grep -v '^third_party/'); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

# Optional: staticcheck is not wired into CI yet, because the tree does not
# pass it today. Run it while working; fix what you touch.
lint:
	@command -v staticcheck >/dev/null 2>&1 || { \
		echo "staticcheck not installed: go install honnef.co/go/tools/cmd/staticcheck@latest"; \
		exit 1; }
	staticcheck ./...

check: vet fmtcheck race

clean:
	rm -f $(addprefix $(GOBIN)/,$(APPS))
