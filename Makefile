GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

APPS := imgview tie-view tie-fm tie-audio

# Fyne targets the X11 driver by default on Linux; on a pure Wayland session
# (WAYLAND_DISPLAY set, no Xwayland DISPLAY) the apps need the Wayland driver
# and GLES/EGL tags. A bare XDG_SESSION_TYPE=wayland check is not enough here:
# XWayland may serve X11 apps, in which case the X11 tags remain correct.
PURE_WAYLAND := $(shell sh -c '[ -n "$$WAYLAND_DISPLAY" ] && [ -z "$$DISPLAY" ] && echo yes')
GOTAGS :=
ifeq ($(PURE_WAYLAND),yes)
GOTAGS := -tags "wayland egl gles gles2"
endif

.PHONY: all install submodule $(APPS) test clean

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

clean:
	rm -f $(addprefix $(GOBIN)/,$(APPS))
