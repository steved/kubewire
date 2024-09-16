GOENV := CGO_ENABLED=0
GO    := $(GOENV) go
SHELL := /bin/bash

ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GOOS=$(shell go env GOOS)
GOARCH=$(shell go env GOARCH)

BIN ?= kw

BIN_DIR ?= $(shell pwd)/.bin
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

.PHONY: build
build: docgen
	$(GO) build -o $(BIN_DIR)/$(BIN) .

docker:
	docker build . \
		-t ghcr.io/steved/kubewire:latest \
		--platform linux/amd64 --push

.PHONY: test
test:
	$(GO) test -v -timeout=5m ./...

.PHONY: docgen
docgen:
	rm -r ./docs/*
	$(GO) run main.go docgen

ifeq (,$(shell command -v golangci-lint))
GOLANGCI_LINT=$(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.60.3
else
GOLANGCI_LINT=golangci-lint
endif

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run

.PHONY: tidy
tidy:
	@rm -f go.sum; go mod tidy

.DEFAULT_GOAL:=help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
