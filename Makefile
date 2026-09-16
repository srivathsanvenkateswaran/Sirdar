.PHONY: build test vet ui desktop desktop-windows desktop-dev install serve release-snapshot dist-desktop dist-desktop-windows version tokens check-tokens site

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

desktop:
	cd desktop && $(WAILS) build -ldflags "-X main.version=$(VERSION)"

# The Windows app, cross-compiled from whatever machine you are on. Wails v2
# builds windows/amd64 with CGO off, so a macOS or Linux box produces a real
# Sirdar.exe; only running it needs Windows. The output lands beside the
# host build in desktop/build/bin, which is why dist-desktop keeps the two
# apart by name.
desktop-windows:
	cd desktop && $(WAILS) build -platform windows/amd64 -ldflags "-X main.version=$(VERSION)"

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
