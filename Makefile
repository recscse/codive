VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v1.1.0")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")

LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.GitCommit=$(COMMIT) -X main.BuildDate=$(DATE)

TARGETS := \
	windows/amd64 \
	windows/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64

.PHONY: all build build-all test clean

all: build

build:
	@echo "Building local binary..."
	go build -ldflags "$(LDFLAGS)" -o ctxd .

test:
	@echo "Running tests..."
	go test -count=1 -v ./...

build-all: clean
	@echo "Building cross-platform release binaries for version $(VERSION)..."
	@mkdir -p dist
	@for target in $(TARGETS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		output="dist/ctxd-$$os-$$arch"; \
		if [ "$$os" = "windows" ]; then output="$$output.exe"; fi; \
		echo "Building $$output ($$target)..."; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o "$$output" . || exit 1; \
	done
	@echo "All binaries successfully compiled to dist/"

clean:
	@echo "Cleaning dist directory..."
	@rm -rf dist
