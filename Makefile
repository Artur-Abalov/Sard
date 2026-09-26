# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov

# Go module path prefix. Changing it: see docs/adr/0005-go-module-path.md.
MODULE_PATH := github.com/Artur-Abalov/sard

BIN := $(CURDIR)/.bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
GO_MODULES := agent agent/plugins/sdk cli

GO_TOOLS := \
	github.com/bufbuild/buf/cmd/buf \
	google.golang.org/protobuf/cmd/protoc-gen-go \
	google.golang.org/grpc/cmd/protoc-gen-go-grpc \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint \
	github.com/avito-tech/go-mutesting/cmd/go-mutesting \
	./cmd/crap

.PHONY: tools gate gate-fast proto build test lint lint-proto lint-go license-check

## proto: generate Go code from proto/ into proto/gen/go (committed)
proto: tools
	cd proto && $(BIN)/buf generate
	cd proto/gen/go && go mod tidy

## build: build every part
build:
	cd agent && go build -ldflags "$(LDFLAGS)" -o bin/sard-agent ./cmd/sard-agent
	cd cli && go build -ldflags "$(LDFLAGS)" -o bin/sardctl ./cmd/sardctl

## test: run every test suite
test:
	@for m in $(GO_MODULES); do echo "== go test $$m"; (cd $$m && go test -count=1 ./...) || exit 1; done

## lint: licenses, formatting, static analysis
lint: license-check lint-proto lint-go

license-check:
	./scripts/license-check.sh

lint-proto: tools
	cd proto && $(BIN)/buf lint

lint-go: tools
	@for m in $(GO_MODULES) proto/gen/go; do \
		echo "== lint $$m"; \
		test -z "$$(cd $$m && gofmt -l .)" || { (cd $$m && gofmt -l .); exit 1; }; \
		(cd $$m && go vet ./... && $(BIN)/golangci-lint run --config $(CURDIR)/.golangci.yml ./...) || exit 1; \
	done


## tools: build pinned dev tools (tools/go.mod) into .bin/
tools:
	cd tools && GOWORK=off go build -o $(BIN)/ $(GO_TOOLS)

## gate: full quality gate for every module (M=<module> for one)
gate:
	./scripts/gate.sh $(or $(M),all)

## gate-fast: quality gate without mutation testing
gate-fast:
	./scripts/gate.sh $(or $(M),all) fast
