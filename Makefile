# Build the web UI, embed it, and build the Go binary.
#   make            # generate + frontend + build
#   make dev        # run the Go server; then `cd frontend && pnpm dev` for the UI with hot reload

BIN      ?= bin/agent-web
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG      := github.com/jclab-joseph/agent-web/internal/version
LDFLAGS  := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
WEBDIST  := internal/webui/dist

.PHONY: all generate frontend build test dev clean

all: generate frontend build

# OpenAPI document from the Go API, then the TypeScript client from it.
generate:
	go generate ./...
	cd frontend && pnpm install --frozen-lockfile && pnpm gen:api

frontend:
	cd frontend && pnpm install --frozen-lockfile && pnpm build
	find $(WEBDIST) -mindepth 1 ! -name .gitkeep -delete
	cp -r frontend/.output/public/. $(WEBDIST)/

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/agent-web

test:
	go vet ./...
	go test -race ./...
	cd frontend && pnpm typecheck

dev:
	go run ./cmd/agent-web --data-dir .data

clean:
	rm -rf bin frontend/.output
	find $(WEBDIST) -mindepth 1 ! -name .gitkeep -delete
