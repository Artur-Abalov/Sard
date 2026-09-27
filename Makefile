# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov

# Go module path prefix. Changing it: see docs/adr/0005-go-module-path.md.
MODULE_PATH := github.com/Artur-Abalov/sard

BIN := $(CURDIR)/.bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
GO_MODULES := agent agent/plugins/sdk cli
GRADLE := ./gradlew --no-daemon -q
# Git ref the proto contract must stay compatible with (buf breaking).
PROTO_BASE ?= origin/main
COMPOSE := docker compose -f deploy/docker-compose.yml --env-file deploy/.env

GO_TOOLS := \
	github.com/bufbuild/buf/cmd/buf \
	google.golang.org/protobuf/cmd/protoc-gen-go \
	google.golang.org/grpc/cmd/protoc-gen-go-grpc \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint \
	github.com/avito-tech/go-mutesting/cmd/go-mutesting \
	./cmd/crap

.PHONY: tools gate gate-fast proto build build-agent build-cli test lint lint-proto breaking-proto lint-go lint-server lint-web web-deps license-check up down openapi

## proto: generate Go code from proto/ into proto/gen/go (committed)
proto: tools
	cd proto && $(BIN)/buf generate
	cd proto/gen/go && go mod tidy

## build: build every part
build: build-agent build-cli
	$(GRADLE) :server:bootJar -PsardVersion=$(VERSION)
	$(MAKE) web-deps
	cd web && npm run build

build-agent:
	cd agent && go build -ldflags "$(LDFLAGS)" -o bin/sard-agent ./cmd/sard-agent

build-cli:
	cd cli && go build -ldflags "$(LDFLAGS)" -o bin/sardctl ./cmd/sardctl

## test: run every test suite
test:
	@for m in $(GO_MODULES); do echo "== go test $$m"; (cd $$m && go test -count=1 ./...) || exit 1; done
	@echo "== server tests (Testcontainers: needs Docker)"
	$(GRADLE) :server:test
	$(MAKE) web-deps
	cd web && npm test

## lint: licenses, formatting, static analysis
lint: license-check lint-proto lint-go lint-server lint-web

lint-server:
	$(GRADLE) :server:spotlessCheck :server:detekt

lint-web: web-deps
	cd web && npm run lint && npm run typecheck

web-deps:
	test -d web/node_modules || (cd web && npm ci --no-audit --no-fund)

## openapi: export the server's OpenAPI document for the web client
openapi:
	$(GRADLE) :server:test --tests 'dev.sard.server.SardServerIntegrationTest'
	cp server/build/openapi/openapi.json web/src/api/openapi.json
	$(MAKE) web-deps
	cd web && npm run gen:api

## up: start PostgreSQL + sard-server (builds the image on first run)
up:
	test -f deploy/.env || cp deploy/.env.example deploy/.env
	$(COMPOSE) up -d --wait

## down: stop the local stack (data volume is kept)
down:
	$(COMPOSE) down

license-check:
	./scripts/license-check.sh

lint-proto: tools
	cd proto && $(BIN)/buf lint

## breaking-proto: the contract has no breaking changes against PROTO_BASE (default origin/main)
breaking-proto: tools
	cd proto && $(BIN)/buf breaking --against '$(CURDIR)/.git#ref=$(PROTO_BASE),subdir=proto'

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
