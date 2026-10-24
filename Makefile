GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

APPS := imgview tie-view tie-fm tie-audio

# Desktop integration: `make install` also drops a .desktop entry and (where
# the app has one) a 256x256 icon into the per-user XDG data directory, so the
# apps show up in the desktop environment's application launcher. An explicit
# XDG_DATA_HOME wins; otherwise use the spec's ~/.local/share default.
XDG_DATA_HOME := $(or $(XDG_DATA_HOME),$(HOME)/.local/share)
APPLICATIONS_DIR := $(XDG_DATA_HOME)/applications
ICON_DIR := $(XDG_DATA_HOME)/icons/hicolor/256x256/apps

# Per-app fields: name|Exec field code|Comment|Categories|Keywords. The field
# code is the launcher argument for files (%F) or URLs (%U); empty means the
# app takes no startup argument. tie-fm and tie-audio do not parse startup
# arguments, so they only take the bare Exec path.
DESKTOP_imgview := imgview|%F|View local images and archives|Graphics;Viewer;|image;viewer;photo;archive;
DESKTOP_tie-view := tie-view|%F|View and tag images stored in tie|Graphics;Viewer;|tie;image;viewer;tag;
DESKTOP_tie-fm := tie-fm||Twin-panel file manager for local files and tie|System;FileTools;FileManager;|tie;file;manager;tag;
DESKTOP_tie-audio := tie-audio||Play music stored in tie|AudioVideo;Audio;Player;|tie;audio;music;player;

# Fyne targets the X11 driver by default on Linux; on a pure Wayland session
# (WAYLAND_DISPLAY set, no Xwayland DISPLAY) the apps need the Wayland driver
# and GLES/EGL tags. A bare XDG_SESSION_TYPE=wayland check is not enough here:
# XWayland may serve X11 apps, in which case the X11 tags remain correct.
PURE_WAYLAND := $(shell sh -c '[ -n "$$WAYLAND_DISPLAY" ] && [ -z "$$DISPLAY" ] && echo yes')
GOTAGS :=
ifeq ($(PURE_WAYLAND),yes)
GOTAGS := -tags "wayland egl gles gles2"
endif

.PHONY: all install install-desktop uninstall submodule $(APPS) test clean

all: install

# Ensure the vendored fyne fork submodule is checked out before building.
submodule:
	git submodule update --init --recursive

install: submodule
	go install $(GOTAGS) $(addprefix ./cmd/,$(APPS))
	@$(MAKE) --no-print-directory install-desktop
	@echo "Installed $(APPS) to $(GOBIN)"

# Desktop entries use the absolute installed binary path because a launcher's
# PATH is not guaranteed to include $(GOBIN). Quote it for the Exec key and
# escape the characters the desktop-entry grammar treats specially.
install-desktop:
	@mkdir -p "$(APPLICATIONS_DIR)" "$(ICON_DIR)"
	@set -eu; \
	set -f; \
	exec_dir=$$(printf '%s' '$(GOBIN)' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/%/%%/g'); \
	for entry in $(foreach app,$(APPS),'$(DESKTOP_$(app))'); do \
		IFS='|'; set -- $$entry; unset IFS; \
		app=$$1; fieldcode=$${2:-}; comment=$${3:-}; categories=$${4:-}; keywords=$${5:-}; \
		icon=""; \
		if [ -f "cmd/$$app/Icon.png" ]; then \
			install -m 644 "cmd/$$app/Icon.png" "$(ICON_DIR)/$$app.png"; \
			icon=$$app; \
		fi; \
		{ \
			printf '[Desktop Entry]\n'; \
			printf 'Type=Application\n'; \
			printf 'Name=%s\n' "$$app"; \
			printf 'Comment=%s\n' "$$comment"; \
			printf 'Exec="%s/%s"' "$$exec_dir" "$$app"; \
			if [ -n "$$fieldcode" ]; then printf ' %s' "$$fieldcode"; fi; \
			printf '\n'; \
			if [ -n "$$icon" ]; then printf 'Icon=%s\n' "$$icon"; fi; \
			printf 'Categories=%s\n' "$$categories"; \
			printf 'Keywords=%s\n' "$$keywords"; \
			printf 'Terminal=false\n'; \
		} > "$(APPLICATIONS_DIR)/$$app.desktop"; \
	done
	@command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$(APPLICATIONS_DIR)" >/dev/null 2>&1 || true
	@echo "Installed desktop entries in $(APPLICATIONS_DIR)"

uninstall:
	rm -f $(addprefix $(GOBIN)/,$(APPS))
	rm -f $(addprefix $(APPLICATIONS_DIR)/,$(addsuffix .desktop,$(APPS)))
	rm -f $(addprefix $(ICON_DIR)/,$(addsuffix .png,$(APPS)))
	@command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$(APPLICATIONS_DIR)" >/dev/null 2>&1 || true

$(APPS): submodule
	go install $(GOTAGS) ./cmd/$@
	@echo "Installed $@ to $(GOBIN)"

test:
	go test $(GOTAGS) ./...

clean:
	rm -f $(addprefix $(GOBIN)/,$(APPS))
