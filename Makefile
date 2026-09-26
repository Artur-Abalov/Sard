# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov

# Go module path prefix. Changing it: see docs/adr/0005-go-module-path.md.
MODULE_PATH := github.com/Artur-Abalov/sard

BIN := $(CURDIR)/.bin
GO_TOOLS := \
	github.com/bufbuild/buf/cmd/buf \
	google.golang.org/protobuf/cmd/protoc-gen-go \
	google.golang.org/grpc/cmd/protoc-gen-go-grpc \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint \
	github.com/avito-tech/go-mutesting/cmd/go-mutesting \
	./cmd/crap

.PHONY: tools gate gate-fast

## tools: build pinned dev tools (tools/go.mod) into .bin/
tools:
	cd tools && GOWORK=off go build -o $(BIN)/ $(GO_TOOLS)

## gate: full quality gate for every module (M=<module> for one)
gate:
	./scripts/gate.sh $(or $(M),all)

## gate-fast: quality gate without mutation testing
gate-fast:
	./scripts/gate.sh $(or $(M),all) fast
