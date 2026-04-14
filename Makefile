# Variables
BINARY=poisearch
DESTDIR?=/usr/local/bin
CONFIG?=config.toml
PBF?=
LIMIT?=10

# Phony targets
.PHONY: help build install clean \
        test integration fmt lint deps \
        build-index serve search \
        bench bench-slow \
        open clean-index

# Default target
all: fmt lint build test

help:
	@echo "Build & Install:"
	@echo "  build              Build the $(BINARY) binary"
	@echo "  install            Install $(BINARY) to $(DESTDIR)"
	@echo "  clean              Remove build artifacts"
	@echo ""
	@echo "Development:"
	@echo "  test               Run all unit tests (fast)"
	@echo "  integration        Run integration tests (slow)"
	@echo "  fmt                Format code and auto-fix issues"
	@echo "  lint               Run linter"
	@echo "  deps               Install dev tooling"
	@echo ""
	@echo "Runtime (requires config.toml):"
	@echo "  build-index [PBF=] Build the POI index from a PBF file"
	@echo "  serve              Start the search API server"
	@echo "  search [Q=] [LIMIT=] Run a search query"
	@echo "  open               Open the search API in browser"
	@echo "  clean-index        Remove the index directory"
	@echo ""
	@echo "Benchmarks:"
	@echo "  bench              Run benchmarks (analyzer + geometry modes)"
	@echo "  bench-slow         Run benchmarks with a larger dataset"

# Build & Install
build:
	go build -o $(BINARY) ./cmd/poisearch

install: build
	sudo install -m 0755 $(BINARY) $(DESTDIR)/$(BINARY)

clean:
	rm -f $(BINARY)
	rm -rf *.bleve/

# Development
test:
	go test ./internal/... ./cmd/... -count=1

integration:
	go test ./tests/... -count=1 -v

fmt:
	golangci-lint fmt
	go fix ./...

lint:
	golangci-lint run --fix ./...

deps:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install gotest.tools/gotestsum@latest

# Runtime targets
build-index: build
	@if [ -z "$(PBF)" ]; then \
		echo "Error: PBF=<path> is required"; \
		exit 1; \
	fi
	./$(BINARY) --config $(CONFIG) build $(PBF)

serve: build
	./$(BINARY) --config $(CONFIG) serve

search: build
	@curl -s "http://localhost:9889/search?q=$(Q)&limit=$(LIMIT)" | python3 -m json.tool 2>/dev/null || \
	curl -s "http://localhost:9889/search?q=$(Q)&limit=$(LIMIT)"

open:
	@python3 -m webbrowser "http://localhost:9889/search?q=restaurant&limit=10" 2>/dev/null || \
	xdg-open "http://localhost:9889/search?q=restaurant&limit=10" 2>/dev/null || \
	open "http://localhost:9889/search?q=restaurant&limit=10" 2>/dev/null || \
	echo "Open http://localhost:9889/search?q=restaurant&limit=10 in your browser"

clean-index:
	@if [ -d "$(shell grep -oP 'index_path\s*=\s*"\K[^"]+' $(CONFIG) 2>/dev/null || echo pois.bleve)" ]; then \
		rm -rf $(shell grep -oP 'index_path\s*=\s*"\K[^"]+' $(CONFIG) 2>/dev/null || echo pois.bleve); \
		echo "Index removed."; \
	else \
		echo "No index found at configured path."; \
	fi

# Benchmarks
bench:
	go run ./cmd/bench

bench-slow:
	go run ./cmd/bench -slow
