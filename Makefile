.PHONY: build test vet ui desktop desktop-windows desktop-linux desktop-dev install serve release-snapshot dist-desktop dist-desktop-windows version tokens check-tokens site

# `wails` is installed with `go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`,
# which puts it in $(go env GOPATH)/bin. Override WAILS if it is not on PATH.
WAILS ?= wails
UI_DIST := internal/httpapi/ui/dist

# Same shape .goreleaser.yaml's ldflags use for main.version: the nearest
# tag, or `<tag>-<n>-g<sha>` past it, or the bare commit if there is no tag
# yet at all.
VERSION := $(shell git describe --tags --always)
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

build: ; go build -o sirdar ./cmd/sirdar
test:  ; go test ./...
vet:   ; go vet ./...

# Prints the version a local build would stamp in, computed the same way a
# release does but without needing a tag on HEAD.
version:
	@echo $(VERSION)

# Build the shared frontend and stage it where internal/httpapi embeds it.
ui:
	cd desktop/frontend && npm ci && npm run build
	rm -rf $(UI_DIST)
	mkdir -p $(UI_DIST)
	cp -R desktop/frontend/dist/. $(UI_DIST)/

# Ubuntu 24.04 and every other current distro dropped libwebkit2gtk-4.0-dev,
# and Wails v2 builds against 4.1 only under this tag. Empty on macOS and
# Windows, which have nothing to select.
DESKTOP_TAGS := $(if $(filter linux,$(GOOS)),-tags webkit2_41,)

desktop:
	cd desktop && $(WAILS) build $(DESKTOP_TAGS) -ldflags "-X main.version=$(VERSION)"

# The Windows app, cross-compiled from whatever machine you are on. Wails v2
# builds windows/amd64 with CGO off, so a macOS or Linux box produces a real
# Sirdar.exe; only running it needs Windows. The output lands beside the
# host build in desktop/build/bin, which is why dist-desktop keeps the two
# apart by name.
desktop-windows:
	cd desktop && $(WAILS) build -platform windows/amd64 -ldflags "-X main.version=$(VERSION)"

# The Linux app plus the launcher entry, icon and installer that make the
# result installable — the same contents the release zip carries.
#
# **This only works on a Linux host.** Unlike desktop-windows above there is
# no cross-build to be had: the Linux app is a GTK/WebKitGTK program, cgo
# links it against libgtk-3 and libwebkit2gtk-4.1, and a macOS or Windows
# machine has neither those headers nor a C toolchain aimed at them. The
# guard below says so rather than letting the C compiler say it three
# screens later. `.github/workflows/desktop.yml` is what builds this for
# people who are not on Linux.
#
# Deps on Ubuntu/Debian: libgtk-3-dev libwebkit2gtk-4.1-dev.
# On Fedora:             gtk3-devel webkit2gtk4.1-devel.
desktop-linux:
	@[ "$(GOOS)" = "linux" ] || { \
	  echo "make desktop-linux: this is a $(GOOS) host. The Linux app needs GTK and WebKitGTK headers"; \
	  echo "                    and cannot be cross-compiled; build it on Linux, or take the artifact"; \
	  echo "                    from the desktop workflow. See docs/release.md."; exit 1; }
	$(MAKE) desktop
	./scripts/package-linux.sh

desktop-dev:
	cd desktop && $(WAILS) dev

serve:
	go run ./cmd/sirdar serve --open

# Local equivalent of the CLI half of the release workflow: builds every
# darwin/linux/windows x amd64/arm64 binary into dist/ without publishing
# anything. Delete dist/ when done looking.
release-snapshot:
	goreleaser build --snapshot --clean

# Zips the desktop build for the machine you're running on, named the same
# way the release workflow's desktop job names its uploads. Only covers this
# machine's OS/arch; the workflow covers all three platforms.
# Build the desktop app and put it where Launchpad finds it. The copy replaces
# whatever is there; quit a running Sirdar first or macOS keeps the old code.
install: desktop
	rm -rf /Applications/Sirdar.app
	cp -R desktop/build/bin/Sirdar.app /Applications/Sirdar.app

# Two zips, named the way the release workflow's desktop job names its
# uploads: this machine's own app, and the cross-compiled Windows one. The
# host zip excludes Sirdar.exe because both builds land in the same
# build/bin and a leftover .exe would otherwise ride along inside the
# macOS or Linux archive.
dist-desktop: desktop
	@# On a Linux host the zip is an install, not a loose binary: the
	@# launcher entry, the icon and install.sh go in beside the app, the
	@# same three files the release workflow stages.
	@if [ "$(GOOS)" = "linux" ]; then ./scripts/package-linux.sh; fi
	mkdir -p dist
	cd desktop/build/bin && zip -r "../../../dist/sirdar-desktop_$(VERSION)_$(GOOS)_$(GOARCH).zip" . -x 'Sirdar.exe'
	$(MAKE) dist-desktop-windows

dist-desktop-windows: desktop-windows
	mkdir -p dist
	cd desktop/build/bin && zip "../../../dist/sirdar-desktop_$(VERSION)_windows_amd64.zip" Sirdar.exe

# Design tokens. desktop/frontend/src/styles/tokens.css is the source; the
# landing site and the docs site each read a byte-identical copy of it. Run
# this after touching the source, and commit the copies alongside it.
TOKENS_SRC := desktop/frontend/src/styles/tokens.css

tokens:
	cp $(TOKENS_SRC) site/tokens.css
	@# The docs site gets the same copy once it is wired up to read one
	@# (docs/design/01-tokens.md, section 5). Until that file exists this is a no-op.
	@if [ -f docs/stylesheets/tokens.css ]; then cp $(TOKENS_SRC) docs/stylesheets/tokens.css; fi

# Fails if a copy has drifted from the source. CI runs this.
check-tokens:
	./scripts/check-tokens.sh

# Preview the landing page. No build step: site/ is the deliverable.
site:
	python3 -m http.server 8000 --directory site
