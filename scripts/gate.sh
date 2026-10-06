#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Quality gate per module. Thresholds live here and nowhere else.
#
#   ./scripts/gate.sh <module|all> [fast]
#
# Modules: proto gen sdk agent cli tools server web.
# "fast" skips mutation testing. Exit code is non-zero on the first failure.
set -euo pipefail
export LC_ALL=C.UTF-8
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/.bin"

COVERAGE_MIN=80    # % of statements (Go) / instructions (JVM)
CRAP_MAX=6         # per function
MUTATION_MIN=0.80  # killed / (killed + survived)
# Cyclomatic complexity <= 8 is enforced by .golangci.yml, detekt and oxlint.

ALL_MODULES=(proto gen sdk tools agent cli server web)

say() { printf '\n== gate %s: %s\n' "$1" "$2"; }
die() { printf 'gate: FAILED — %s\n' "$*" >&2; exit 1; }

module_dir() {
  case "$1" in
    proto) echo proto ;;
    gen) echo proto/gen/go ;;
    sdk) echo agent/plugins/sdk ;;
    agent | cli | tools | server | web) echo "$1" ;;
    *) die "unknown module '$1' (known: ${ALL_MODULES[*]})" ;;
  esac
}

need_tools() {
  [ -x "$BIN/crap" ] && [ -x "$BIN/golangci-lint" ] && [ -x "$BIN/go-mutesting" ] || make -C "$ROOT" tools
}

# Go modules outside go.work (tools) are built with GOWORK=off.
go_env() { if [ "$1" = tools ]; then echo off; else echo "${GOWORK:-}"; fi; }

gate_go_basic() {
  local m="$1" dir="$ROOT/$(module_dir "$1")"
  say "$m" "gofmt, vet, golangci-lint"
  local unformatted
  unformatted="$(cd "$dir" && gofmt -l .)"
  [ -z "$unformatted" ] || die "$m: gofmt needed: $unformatted"
  (cd "$dir" && GOWORK="$(go_env "$m")" go vet ./...) || die "$m: go vet"
  (cd "$dir" && GOWORK="$(go_env "$m")" "$BIN/golangci-lint" run --config "$ROOT/.golangci.yml" ./...) || die "$m: golangci-lint"
}

gate_go() {
  local m="$1" mode="$2" dir="$ROOT/$(module_dir "$1")"
  need_tools
  gate_go_basic "$m"
  local profile="$dir/.cover.out"
  say "$m" "tests + coverage >= ${COVERAGE_MIN}%"
  # Coverage counts every package of this module hit by any of its tests
  # (e.g. plugins exercised by the registry contract test), nothing else.
  local pkgs
  pkgs="$(cd "$dir" && GOWORK="$(go_env "$m")" go list ./... | paste -sd, -)"
  (cd "$dir" && GOWORK="$(go_env "$m")" go test -count=1 -coverpkg="$pkgs" -coverprofile="$profile" ./...) || die "$m: tests"
  local total
  total="$(cd "$dir" && GOWORK="$(go_env "$m")" go tool cover -func="$profile" | awk '/^total:/ {sub("%","",$3); print $3}')"
  echo "coverage: ${total}%"
  awk -v t="$total" -v min="$COVERAGE_MIN" 'BEGIN { exit !(t >= min) }' || die "$m: coverage ${total}% < ${COVERAGE_MIN}%"
  say "$m" "CRAP <= $CRAP_MAX"
  "$BIN/crap" -go-profile "$profile" -go-src "$dir" -threshold "$CRAP_MAX" || die "$m: CRAP"
  [ "$m" = agent ] && gate_agent_integration && gate_agent_stand
  [ "$mode" = fast ] && return 0
  say "$m" "mutation score >= $MUTATION_MIN"
  local log score
  log="$(mktemp)"
  (cd "$dir" && GOWORK="$(go_env "$m")" "$BIN/go-mutesting" ./... >"$log" 2>&1) || true
  grep '^FAIL' "$log" || true
  score="$(sed -n 's/^The mutation score is \([0-9.]*\).*/\1/p' "$log")"
  [ -n "$score" ] || { tail -20 "$log"; die "$m: go-mutesting produced no score"; }
  echo "mutation score: $score"
  awk -v s="$score" -v min="$MUTATION_MIN" 'BEGIN { exit !(s >= min) }' || die "$m: mutation score $score < $MUTATION_MIN"
}

# Integration tests run the pinned restic (agent/internal/restic/restic-version)
# against a local repository in temporary directories: the wrapper itself
# and plugins driven through pluginhost, and sard-agent repo init / repo list.
AGENT_INTEGRATION_PKGS=(./internal/restic/... ./internal/pluginhost/... ./cmd/sard-agent/...)
gate_agent_integration() {
  local dir="$ROOT/agent"
  say agent "integration tests with the pinned restic"
  "$ROOT/scripts/fetch-restic.sh" || die "agent: fetch restic"
  (cd "$dir" && go vet -tags integration "${AGENT_INTEGRATION_PKGS[@]}") || die "agent: go vet (integration)"
  (cd "$dir" && "$BIN/golangci-lint" run --build-tags integration --config "$ROOT/.golangci.yml" "${AGENT_INTEGRATION_PKGS[@]}") || die "agent: golangci-lint (integration)"
  (cd "$dir" && go test -count=1 -race -tags integration "${AGENT_INTEGRATION_PKGS[@]}") || die "agent: integration tests"
}

# The e2e stand's agent build (tag e2e, make e2e-images) adds its plugins to
# the registry (agent/plugins/stand_e2e.go); a release build never does.
gate_agent_stand() {
  local dir="$ROOT/agent"
  say agent "e2e stand build (tag e2e)"
  (cd "$dir" && go vet -tags e2e ./plugins/...) || die "agent: go vet (e2e)"
  (cd "$dir" && "$BIN/golangci-lint" run --build-tags e2e --config "$ROOT/.golangci.yml" ./plugins/...) || die "agent: golangci-lint (e2e)"
  (cd "$dir" && go test -count=1 -tags e2e ./plugins/...) || die "agent: tests (e2e)"
}

gate_gen() {
  local dir="$ROOT/proto/gen/go"
  say gen "generated code builds and vets"
  (cd "$dir" && go build ./... && go vet ./...) || die "gen: build"
}

gate_proto() {
  need_tools
  say proto "buf lint + buf breaking against ${PROTO_BASE:-origin/main} + generated code is up to date"
  make -C "$ROOT" proto
  git -C "$ROOT" diff --exit-code -- proto/gen || die "proto: generated code differs from committed code; run make proto and commit"
  make -C "$ROOT" lint-proto || die "proto: buf lint"
  make -C "$ROOT" breaking-proto || die "proto: breaking change against ${PROTO_BASE:-origin/main}"
}

gate_server() {
  local mode="$1"
  need_tools
  say server "spotless, detekt, tests, coverage >= ${COVERAGE_MIN}%"
  (cd "$ROOT" && ./gradlew --no-daemon -q :server:check) || die "server: check"
  # Report-level INSTRUCTION counter is the last one in the JaCoCo XML.
  grep -o '<counter type="INSTRUCTION"[^>]*>' "$ROOT/server/build/reports/jacoco/test/jacocoTestReport.xml" | tail -1 |
    awk -F'"' '{ printf "coverage: %.1f%% (instructions)\n", 100 * $6 / ($4 + $6) }'
  say server "CRAP <= $CRAP_MAX"
  "$BIN/crap" -jacoco "$ROOT/server/build/reports/jacoco/test/jacocoTestReport.xml" -threshold "$CRAP_MAX" || die "server: CRAP"
  [ "$mode" = fast ] && return 0
  # mutflow: a second, mutated compilation; any surviving mutant fails the
  # build (stricter than MUTATION_MIN). See docs/adr/0006-mutation-testing.md.
  say server "mutation testing (mutflow, no survivors)"
  (cd "$ROOT" && ./gradlew --no-daemon -q -Pmutflow.enabled=true :server:test --rerun) || die "server: mutation testing"
}

gate_web() {
  say web "oxlint + prettier, typecheck, unit tests, build"
  [ -d "$ROOT/web/node_modules" ] || (cd "$ROOT/web" && npm ci --no-audit --no-fund) || die "web: npm ci"
  (cd "$ROOT/web" && npm run lint && npm run typecheck && npm test && npm run build) || die "web"
}

gate_one() {
  local m="$1" mode="$2" dir
  dir="$ROOT/$(module_dir "$m")"
  if [ ! -d "$dir" ]; then
    say "$m" "skipped: $dir does not exist yet"
    return 0
  fi
  case "$m" in
    proto) gate_proto ;;
    gen) gate_gen ;;
    server) gate_server "$mode" ;;
    web) gate_web ;;
    *) gate_go "$m" "$mode" ;;
  esac
}

main() {
  local target="${1:-}" mode="${2:-full}"
  [ -n "$target" ] || die "usage: gate.sh <module|all> [fast]"
  [ "$mode" = full ] || [ "$mode" = fast ] || die "mode must be 'fast' or omitted"
  if [ "$target" = all ]; then
    for m in "${ALL_MODULES[@]}"; do gate_one "$m" "$mode"; done
  else
    gate_one "$target" "$mode"
  fi
  printf '\ngate: PASSED (%s, %s)\n' "$target" "$mode"
}

main "$@"
