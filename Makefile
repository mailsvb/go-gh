# Local builds. `make build` does what the CI "Package" step does
# (.github/workflows/build.yml) and produces bin/go-gh.app on macOS.
#
# The GitHub App client ID and slug are taken from the environment
# (GH_APP_CLIENT_ID, GH_APP_SLUG) or else read from the repository variables
# with the gh CLI. Override on the command line, e.g.
#   make build GH_APP_CLIENT_ID=Iv23... GH_APP_SLUG=my-app

APP_NAME      := go-gh
VERSION       ?= $(shell git describe --tags --always --dirty)
# Keep in sync with go.mod and the CI workflow.
WAILS_VERSION := v3.1.0

# `go install` puts wails3 into $(go env GOPATH)/bin, which is often not on
# PATH. The Wails Taskfiles call wails3 themselves, so export it for them too.
export PATH := $(shell go env GOPATH)/bin:$(PATH)

.DEFAULT_GOAL := build
.PHONY: build run dev test clean tools

# Installs the Wails CLI if it is missing.
tools:
	@command -v wails3 >/dev/null 2>&1 || { \
		echo "Installing wails3 $(WAILS_VERSION)…"; \
		go install github.com/wailsapp/wails/v3/cmd/wails3@$(WAILS_VERSION); \
	}

build: tools
	@client_id="$${GH_APP_CLIENT_ID:-$$(gh variable get GH_APP_CLIENT_ID 2>/dev/null)}"; \
	slug="$${GH_APP_SLUG:-$$(gh variable get GH_APP_SLUG 2>/dev/null)}"; \
	[ -n "$$client_id" ] || echo "warning: GH_APP_CLIENT_ID not set; the build will not be able to log in"; \
	[ -n "$$slug" ] || echo "warning: GH_APP_SLUG not set; install links will be missing"; \
	echo "Building $(APP_NAME) $(VERSION) (client ID: $${client_id:-none}, slug: $${slug:-none})"; \
	wails3 task package EXTRA_LDFLAGS="-X main.githubClientID=$$client_id -X main.githubAppSlug=$$slug -X main.version=$(VERSION)"

# Starts the packaged app (build first).
run:
	open "bin/$(APP_NAME).app"

# Hot-reloading development mode.
dev: tools
	wails3 dev -config ./build/config.yml

test:
	go vet ./...
	go test ./...
	cd frontend && npm run check

clean:
	rm -rf bin frontend/dist
