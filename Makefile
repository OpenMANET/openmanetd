# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GO=go
GO_MAJOR_VERSION = $(shell $(GO) version | cut -c 14- | cut -d' ' -f1 | cut -d'.' -f1)
GO_MINOR_VERSION = $(shell $(GO) version | cut -c 14- | cut -d' ' -f1 | cut -d'.' -f2)

GO_VERSION = $(GO_MAJOR_VERSION).$(GO_MINOR_VERSION)

PACKAGE_NAME = $(shell awk '/^module / {print $$2}' go.mod)

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: alfred
alfred: ## Make Alfred for Go Bindings
	make -C internal/alfred/alfred

.PHONY: frontend
frontend: ## Build the React frontend (outputs to static/).
	@find static/ -mindepth 1 ! -path 'static/whisper' ! -path 'static/whisper/*' -delete 2>/dev/null || true
	cd frontend && pnpm install && pnpm run build

.PHONY: sqlc-gen
sqlc-gen: ## Generate sqlc code
	$(GOBIN)/sqlc generate

.PHONY: build
build: fmt vet buf sqlc-gen frontend whisper-js ## Build manager binary.
	GOCACHE=$(HOME)/.gocache CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w" -o bin/openmanetd .

.PHONY: run
run: fmt vet buf sqlc-gen ## Run a controller from your host.
	go run ./main.go

.PHONY: buf
buf: ## Generate protobuf code
	buf format -w proto
	buf generate

.PHONY: proto-lint
proto-lint: ## Format proto files and lint them with buf
	buf format -w proto
	cd proto && buf lint

.PHONY: test
test: fmt vet buf sqlc-gen ## Run tests.
	go test ./internal/... -coverprofile=coverage.out -covermode=atomic

.PHONY: test-race
test-race: fmt vet buf sqlc-gen ## Run tests with race detector.
	go test -race -timeout 120s ./internal/... -coverprofile=coverage.out -covermode=atomic

.PHONY: integration-test
integration-test: fmt vet ## Run integration tests (no hardware required).
	go test -tags integration -timeout 60s ./internal/... -coverprofile=coverage.out -covermode=atomic

.PHONY: test-frontend
test-frontend: ## Run frontend tests.
	pnpm -C frontend install && pnpm -C frontend run test:coverage

.PHONY: lint-frontend
lint-frontend: ## Lint the React frontend with ESLint.
	pnpm -C frontend install && pnpm -C frontend run lint

.PHONY: lint-go
lint-go: ## Install golangci-lint if not present, then run it.
	$(GOBIN)/golangci-lint run --fix --timeout 5m

.PHONY: lint
lint: lint-go lint-frontend ## Run linters.

.PHONY: bench-comms
bench-comms: ## Run performance benchmarks on the comms package.
	go test ./internal/comms/ -bench=. -benchmem -count=3 -run=^$$ -timeout 120s

.PHONY: fuzz
fuzz: ## Run fuzz tests for 30 seconds each.
	go test ./internal/security/... -fuzz=Fuzz -fuzztime=30s -run=^$$
	go test ./internal/comms/... -fuzz=Fuzz -fuzztime=30s -run=^$$

.PHONY: build-lite
build-lite: fmt vet frontend ## Build lite binary without whisper WASM, UPX compressed (~5MB).
	@if [ -d static/whisper ]; then cp -r static/whisper /tmp/openmanetd-whisper-bak && rm -rf static/whisper; fi
	GOCACHE=$(HOME)/.gocache CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w" -o bin/openmanetd .
	@if [ -d /tmp/openmanetd-whisper-bak ]; then mv /tmp/openmanetd-whisper-bak static/whisper; fi
	@if command -v upx >/dev/null 2>&1; then \
		upx --lzma --best bin/openmanetd; \
	else \
		echo "WARNING: upx not found, skipping compression (install with: apt install upx-ucl)"; \
	fi
	@echo "Built bin/openmanetd (lite, no whisper, UPX compressed)"

.PHONY: whisper-js
whisper-js: ## Download whisper WASM JS into static/ for embedding (model downloaded at runtime).
	@mkdir -p static/whisper
	@if [ ! -f static/whisper/whisper-main.js ]; then \
		echo "Downloading whisper WASM JS..."; \
		curl -fSL -o static/whisper/whisper-main.js \
			"https://whisper.ggerganov.com/whisper-main.js"; \
	fi
	@echo "Whisper JS staged in static/whisper/ (model will be downloaded on-demand via WebUI)"

.PHONY: whisper-embed
whisper-embed: whisper-js ## Download whisper model into static/ for full embedding (dev/testing).
	@if [ ! -f static/whisper/ggml-tiny.en.bin ]; then \
		echo "Downloading whisper tiny.en model (75MB)..."; \
		curl -fSL -o static/whisper/ggml-tiny.en.bin \
			"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin"; \
	fi
	@echo "Whisper model staged in static/whisper/ (fully embedded in binary)"


.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/

