# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov

# Go module path prefix. Changing it: see docs/adr/0005-go-module-path.md.
MODULE_PATH := github.com/Artur-Abalov/sard

BIN := $(CURDIR)/.bin
# The PostgreSQL of an installation (DB1): the server's image and the base of the -pg18 agent.
include deploy/postgres.env
include test/images.env
# docker-compose.build.yml builds the -pg18 agent of make up on it.
export SARD_AGENT_PG_BASE_IMAGE
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
GO_MODULES := agent agent/plugins/sdk cli
# LC_ALL=C.UTF-8: Kotlin test class file names come from Cyrillic spec-quoted
# titles; the JVM derives file-name encoding (sun.jnu.encoding) from the
# process locale only, never from JVM flags.
# One Gradle build at a time: the SubagentStop hook runs `make gate-fast M=<module>` (for the
# changed modules, scripts/claude/gate-changed.sh) after every
# subagent, and two builds sharing server/build corrupt each other's test results
# (EOFException reading test-results/binary). flock is absent on macOS; there the
# lock is skipped.
BUILD_LOCK := $(if $(shell command -v flock),mkdir -p $(CURDIR)/.gradle && flock -w 1800 $(CURDIR)/.gradle/sard-build.lock,)
GRADLE := $(BUILD_LOCK) env LC_ALL=C.UTF-8 ./gradlew --no-daemon -q
# Git ref the proto contract must stay compatible with (buf breaking).
PROTO_BASE ?= origin/main
# Local stack: the server image is built from this checkout
# (docker-compose.build.yml) with the agent packages of dist/, so its version is
# this checkout's VERSION, not the release version in deploy/.env.
COMPOSE := env SARD_VERSION=$(VERSION) docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml --env-file deploy/.env
# Build once, test that (docs/adr/0045-build-once.md): each artifact is built by
# one target into one place; images, tests and releases take it from there.
# Release agent packages (make package): DIST, for ARCHES (empty: amd64 and arm64).
# DIST must be inside the repository: the server image copies it from the build context.
DIST ?= $(CURDIR)/dist
ARCHES ?=
# The server jar, console included, exported from deploy/server/Dockerfile (make server-jar).
SERVER_JAR_DIR ?= $(CURDIR)/build/server-jar
SERVER_IMAGE ?= sard-server:dev
# Extra `docker buildx build` flags for the jar: a layer cache in CI, proxy
# settings behind a TLS-intercepting proxy.
SERVER_BUILD_FLAGS ?=
# End-to-end tests (test/e2e): images assembled from the artifacts above.
E2E_ARCH ?= $(shell go env GOARCH)
E2E_BUILD := $(CURDIR)/test/e2e/build
# The e2e stand's agent (GO_TAGS=e2e, ADR 0036): the T3 classes only, never shipped.
STAND_DIST ?= $(E2E_BUILD)/stand-dist
E2E_SERVER_IMAGE ?= sard-server:e2e
E2E_AGENT_IMAGE ?= sard-agent:e2e
# The same agent on the pinned PostgreSQL (deploy/agent/Dockerfile, BASE_IMAGE): the compose file's sard-self (F6).
E2E_AGENT_PG_IMAGE ?= $(E2E_AGENT_IMAGE)-pg18
E2E_STAND_AGENT_IMAGE ?= sard-agent-stand:e2e
# The agent on the official postgres image (F1 ПГ20): pg_dump 18, and 14 for the old-client scenario.
E2E_PG18_AGENT_IMAGE ?= sard-agent-pg18:e2e
E2E_PG14_AGENT_IMAGE ?= sard-agent-pg14:e2e
# The stand's SFTP server (test/e2e/sftp, ADR 0047).
E2E_SFTP_IMAGE ?= sard-sftp:e2e
# The machine of the recovery drill (test/e2e/recovery-vm, F7).
E2E_RECOVERY_VM_IMAGE ?= sard-recovery-vm:e2e
# Extra `docker buildx build` flags for the stand images (apt): proxy settings
# and the "build-ca" secret behind a TLS-intercepting proxy.
E2E_BUILD_FLAGS ?=
# One shard of the e2e classes, i/n (scripts/e2e-shard.sh, CI); empty runs them all.
E2E_SHARD ?=

GO_TOOLS := \
	github.com/bufbuild/buf/cmd/buf \
	google.golang.org/protobuf/cmd/protoc-gen-go \
	google.golang.org/grpc/cmd/protoc-gen-go-grpc \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint \
	github.com/avito-tech/go-mutesting/cmd/go-mutesting \
	github.com/goreleaser/nfpm/v2/cmd/nfpm \
	golang.org/x/tools/cmd/deadcode \
	./cmd/crap \
	./cmd/vulngate

# The CI-only tools, a module of their own (tools/ci): their dependencies clash with the ones above.
CI_TOOLS := \
	github.com/rhysd/actionlint/cmd/actionlint \
	golang.org/x/vuln/cmd/govulncheck

.PHONY: tools ci-tools lint-workflows vuln-scan gate gate-fast proto build build-agent build-cli package package-stand server-jar server-image image e2e e2e-images e2e-assemble e2e-agent-images e2e-agent-pg-image e2e-pg-agent-images e2e-test test lint lint-proto breaking-proto lint-go lint-server lint-web web-deps license-check check-sudo-u check-pins up down openapi

## proto: generate Go code from proto/ into proto/gen/go (committed)
proto: tools
	cd proto && $(BIN)/buf generate
	cd proto/gen/go && go mod tidy

## build: build every part; the console (web/dist) goes into the server jar (S10, ADR 0040)
build: build-agent build-cli
	$(MAKE) web-deps
	cd web && SARD_VERSION=$(VERSION) npm run build
	$(GRADLE) :server:bootJar -PsardVersion=$(VERSION) -PsardConsoleDist=web/dist

build-agent:
	cd agent && go build -ldflags "$(LDFLAGS)" -o bin/sard-agent ./cmd/sard-agent

## package: sard-agent + pinned restic as tar.gz, deb and rpm in DIST (ARCHES, default amd64 and arm64)
package: tools
	DIST=$(abspath $(DIST)) VERSION=$(VERSION) ./scripts/package-agent.sh $(ARCHES)

## package-stand: the e2e stand's agent packages (GO_TAGS=e2e, ADR 0036) for E2E_ARCH in STAND_DIST
package-stand: tools
	GO_TAGS=e2e DIST=$(abspath $(STAND_DIST)) VERSION=$(VERSION) ./scripts/package-agent.sh $(E2E_ARCH)

## server-jar: build sard-server.jar, console included, once into SERVER_JAR_DIR
server-jar:
	docker buildx build $(SERVER_BUILD_FLAGS) --build-arg SARD_VERSION=$(VERSION) --target server-jar \
		-o type=local,dest=$(SERVER_JAR_DIR) -f deploy/server/Dockerfile .

## server-image: assemble sard-server from SERVER_JAR_DIR and the packages in DIST; compiles nothing
server-image:
	docker buildx build --load --build-context server-jar=$(SERVER_JAR_DIR) --build-arg SARD_VERSION=$(VERSION) \
		--build-arg AGENT_PACKAGES=$(patsubst $(CURDIR)/%,%,$(abspath $(DIST))) \
		-f deploy/server/Dockerfile -t $(SERVER_IMAGE) .

## image: sard-server image of the current code with its agent packages
image: package server-jar
	$(MAKE) server-image

# agent_image: the agent image (deploy/agent/Dockerfile) of the tar.gz in $(1)
# for E2E_ARCH, tagged $(2); the build context is $(1) itself.
define agent_image
	docker buildx build $(E2E_BUILD_FLAGS) --load --platform linux/$(E2E_ARCH) --build-arg SARD_VERSION=$(VERSION) \
		$(3) -f deploy/agent/Dockerfile -t $(2) $(1)
endef

# agent_image_unpacked: an e2e image of the tar.gz in $(1) for E2E_ARCH, tagged $(2),
# laid out in E2E_BUILD/$(3); the Dockerfile is $(4),
# $(5) are extra build flags.
define agent_image_unpacked
	rm -rf $(E2E_BUILD)/$(3) && mkdir -p $(E2E_BUILD)/$(3)/empty
	tar -xzf $(1)/sard-agent_$(VERSION)_linux_$(E2E_ARCH).tar.gz --strip-components=1 -C $(E2E_BUILD)/$(3)
	docker buildx build $(E2E_BUILD_FLAGS) --load $(5) -f $(4) -t $(2) $(E2E_BUILD)/$(3)
endef

## e2e-images: build the release and stand packages and the jar once, then assemble the e2e images
e2e-images:
	$(MAKE) package DIST=$(E2E_BUILD)/dist ARCHES=$(E2E_ARCH)
	$(MAKE) package-stand
	$(MAKE) server-jar
	$(MAKE) e2e-assemble DIST=$(E2E_BUILD)/dist

## e2e-assemble: the e2e images from built artifacts only (DIST, STAND_DIST, SERVER_JAR_DIR)
e2e-assemble: e2e-agent-images e2e-pg-agent-images
	$(MAKE) server-image SERVER_IMAGE=$(E2E_SERVER_IMAGE)

## e2e-agent-images: the release agent images (plain and -pg18) and the stand's from the tar.gz in DIST and STAND_DIST, and the stand's SFTP server
e2e-agent-images: e2e-agent-pg-image
	$(call agent_image,$(DIST),$(E2E_AGENT_IMAGE))
	$(call agent_image,$(STAND_DIST),$(E2E_STAND_AGENT_IMAGE))
	docker buildx build $(E2E_BUILD_FLAGS) --load -t $(E2E_SFTP_IMAGE) test/e2e/sftp
	docker buildx build $(E2E_BUILD_FLAGS) --load -t $(E2E_RECOVERY_VM_IMAGE) test/e2e/recovery-vm

## e2e-agent-pg-image: the -pg18 release agent alone (the compose file's sard-self), from the tar.gz in DIST
e2e-agent-pg-image:
	$(call agent_image,$(DIST),$(E2E_AGENT_PG_IMAGE),--build-arg BASE_IMAGE=$(SARD_AGENT_PG_BASE_IMAGE))

## e2e-pg-agent-images: the agent on postgres:18 and on postgres:14 (test/e2e/agent/Dockerfile.postgres) from the tar.gz in DIST
e2e-pg-agent-images:
	$(call agent_image_unpacked,$(DIST),$(E2E_PG18_AGENT_IMAGE),pg18-agent-image,test/e2e/agent/Dockerfile.postgres,--build-arg BASE_IMAGE=$(TEST_IMAGE_POSTGRES_18))
	$(call agent_image_unpacked,$(DIST),$(E2E_PG14_AGENT_IMAGE),pg14-agent-image,test/e2e/agent/Dockerfile.postgres,--build-arg BASE_IMAGE=$(TEST_IMAGE_POSTGRES_14))

## e2e-test: the end-to-end tests against the assembled images, E2E_SHARD=i/n for one shard (needs Docker)
e2e-test:
	@tests=; if [ -n "$(E2E_SHARD)" ]; then tests="$$(scripts/e2e-shard.sh $(E2E_SHARD))" || exit 1; echo "e2e shard $(E2E_SHARD):" $$tests; fi; \
	$(GRADLE) :e2e:test $$(for c in $$tests; do printf -- '--tests %s ' "$$c"; done) -Pe2e.serverImage=$(E2E_SERVER_IMAGE) -Pe2e.agentImage=$(E2E_AGENT_IMAGE) -Pe2e.agentPgImage=$(E2E_AGENT_PG_IMAGE) \
		-Pe2e.standAgentImage=$(E2E_STAND_AGENT_IMAGE) -Pe2e.sftpImage=$(E2E_SFTP_IMAGE) -Pe2e.recoveryVmImage=$(E2E_RECOVERY_VM_IMAGE) \
		-Pe2e.pg18AgentImage=$(E2E_PG18_AGENT_IMAGE) -Pe2e.pg14AgentImage=$(E2E_PG14_AGENT_IMAGE) -Pe2e.version=$(VERSION)

## e2e: build the images, then run the end-to-end tests (needs Docker)
e2e: e2e-images
	$(MAKE) e2e-test

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
lint: license-check check-sudo-u check-pins lint-proto lint-go lint-server lint-web

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

## up: start PostgreSQL + sard-server (builds the image, with the agent packages of dist/, on first run)
up: package
	./scripts/ensure-env.sh
	$(COMPOSE) up -d --wait

## down: stop the local stack (data volume is kept)
down:
	$(COMPOSE) down

license-check:
	./scripts/license-check.sh

## check-sudo-u: nothing tells to run the agent as another user through sudo (rule A8, Q5 Р4), and the test of that check
check-sudo-u:
	./scripts/test-check-sudo-u.sh
	./scripts/check-sudo-u.sh

## check-pins: every action by commit SHA, every image by digest, no secret within reach of a PR, the release identity (SC1), and the tests of these checks and of release-cosign.sh
check-pins:
	./scripts/test-check-pins.sh
	./scripts/test-lib-test-image.sh
	./scripts/test-check-workflow-secrets.sh
	./scripts/test-check-identity.sh
	./scripts/test-release-cosign.sh
	./scripts/check-pins.sh
	./scripts/check-workflow-secrets.sh
	./scripts/check-identity.sh

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

## ci-tools: build the pinned CI tools (tools/ci/go.mod) into .bin/
ci-tools:
	cd tools/ci && GOWORK=off go build -o $(BIN)/ $(CI_TOOLS)

## lint-workflows: actionlint and zizmor over .github/workflows (SC1); zizmor runs in Docker
lint-workflows: ci-tools
	$(BIN)/actionlint
	. tools/images.env && docker run --rm -v "$(CURDIR):/src:ro" -w /src -e GH_TOKEN "$$TOOL_IMAGE_ZIZMOR" .github/workflows

## vuln-scan: govulncheck over the Go modules and grype over the lock files, by the threshold of SC1
vuln-scan: tools ci-tools
	./scripts/vuln-scan.sh go
	./scripts/vuln-scan.sh deps

## gate: full quality gate for every module (M=<module> for one)
gate:
	$(BUILD_LOCK) env LC_ALL=C.UTF-8 ./scripts/gate.sh $(or $(M),all)

## gate-fast: quality gate without mutation testing
gate-fast:
	$(BUILD_LOCK) env LC_ALL=C.UTF-8 ./scripts/gate.sh $(or $(M),all) fast
