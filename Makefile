# ABOUTME: Makefile for the Charm CLI/server toolkit.
# ABOUTME: Provides targets for building, testing, linting, and Docker operations.

BINARY_NAME := charm
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)"

# Go settings
GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
	GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all build install clean test test-race test-coverage lint fmt vet \
        serve dev deps deps-update mod-tidy docker-build docker-run \
        release snapshot check help man

# Default target
all: lint test build

## Build targets

build: ## Build the binary
	go build $(LDFLAGS) -o $(BINARY_NAME) .

install: ## Install the binary to GOBIN
	go install $(LDFLAGS) .

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME)
	rm -rf dist/
	rm -f coverage.out

## Testing targets

test: ## Run all tests
	go test ./...

test-race: ## Run tests with race detector
	go test -race ./...

test-coverage: ## Run tests with coverage report
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

test-coverage-html: test-coverage ## Generate HTML coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## Linting targets

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format code with goimports
	goimports -w .

vet: ## Run go vet
	go vet ./...

## Development targets

dev: build ## Build and run locally
	./$(BINARY_NAME)

serve: build ## Build and run the server
	./$(BINARY_NAME) serve

## Dependency management

deps: ## Download dependencies
	go mod download

deps-update: ## Update dependencies
	go get -u ./...
	go mod tidy

mod-tidy: ## Tidy go.mod
	go mod tidy

## Docker targets

docker-build: build ## Build Docker image (requires pre-built binary)
	docker build -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .

docker-run: ## Run the Docker container
	docker run -it --rm \
		-p 35353:35353 \
		-p 35354:35354 \
		-p 35355:35355 \
		-p 35356:35356 \
		-v charm-data:/data \
		$(BINARY_NAME):latest

## Release targets

snapshot: ## Build a snapshot release with goreleaser
	goreleaser build --snapshot --clean

release: ## Create a full release with goreleaser (requires GITHUB_TOKEN)
	goreleaser release --clean

## Documentation targets

man: build ## Generate man pages
	./$(BINARY_NAME) man

## CI/Pre-commit targets

check: lint test ## Run all checks (lint + test)

## Help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
