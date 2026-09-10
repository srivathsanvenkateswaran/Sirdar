.PHONY: build test vet ui desktop desktop-dev serve

# `wails` is installed with `go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`,
# which puts it in $(go env GOPATH)/bin. Override WAILS if it is not on PATH.
WAILS ?= wails
UI_DIST := internal/httpapi/ui/dist

build: ; go build -o sirdar ./cmd/sirdar
test:  ; go test ./...
vet:   ; go vet ./...

# Build the shared frontend and stage it where internal/httpapi embeds it.
ui:
	cd desktop/frontend && npm ci && npm run build
	rm -rf $(UI_DIST)
	mkdir -p $(UI_DIST)
	cp -R desktop/frontend/dist/. $(UI_DIST)/

desktop:
	cd desktop && $(WAILS) build

desktop-dev:
	cd desktop && $(WAILS) dev

serve:
	go run ./cmd/sirdar serve --open
