.PHONY: build test vet ui desktop desktop-dev serve release-snapshot dist-desktop version

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
dist-desktop: desktop
	mkdir -p dist
	cd desktop/build/bin && zip -r "../../../dist/sirdar-desktop_$(VERSION)_$(GOOS)_$(GOARCH).zip" .
