# Hermes Desktop. Go ≥ 1.27.3 (MyGo v0.10.0 needs it).
GO ?= go
PKG := ./cmd/hermes-desktop
LDFLAGS := -s -w -X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build test vet check cross demo screenshots bench icon clean

all: check build

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/hermes-desktop $(PKG)

vet:
	$(GO) vet ./...
	$(GO) vet -tags demo ./...

test:
	$(GO) test -race ./...

# What CI and every change run: vet, then the full suite.
check: vet test

# Every desktop target, cross-compiled from any OS (pure Go, CGO_ENABLED=0).
cross:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/hermes-desktop-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/hermes-desktop-darwin-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS) -H windowsgui' -o dist/hermes-desktop-windows-amd64.exe $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS) -H windowsgui' -o dist/hermes-desktop-windows-arm64.exe $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/hermes-desktop-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/hermes-desktop-linux-arm64 $(PKG)

# The app against an in-process fake instance (design data).
demo:
	$(GO) build -tags demo -o bin/hermes-desktop-demo $(PKG)

screenshots:
	scripts/screenshots.sh screenshots/linux

bench:
	$(GO) run ./cmd/hermes-bench

# The app icon (drawn by internal/appicon) as files: the 1024 px PNG the
# packager uses (resources/icon.png) and the README's copies.
icon:
	$(GO) run ./cmd/hermes-icon -size 1024 -o assets/icon.png -sheet assets/icon-colours.png
	cp assets/icon.png resources/icon.png

clean:
	rm -rf bin dist screenshots/local
