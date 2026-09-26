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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/.bin"

COVERAGE_MIN=80    # % of statements (Go) / instructions (JVM)
CRAP_MAX=6         # per function
MUTATION_MIN=0.80  # killed / (killed + survived)
# Cyclomatic complexity <= 8 is enforced by .golangci.yml, detekt and ESLint.

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
  (cd "$dir" && GOWORK="$(go_env "$m")" go test -count=1 -coverprofile="$profile" ./...) || die "$m: tests"
  local total
  total="$(cd "$dir" && GOWORK="$(go_env "$m")" go tool cover -func="$profile" | awk '/^total:/ {sub("%","",$3); print $3}')"
  echo "coverage: ${total}%"
  awk -v t="$total" -v min="$COVERAGE_MIN" 'BEGIN { exit !(t >= min) }' || die "$m: coverage ${total}% < ${COVERAGE_MIN}%"
  say "$m" "CRAP <= $CRAP_MAX"
  "$BIN/crap" -go-profile "$profile" -go-src "$dir" -threshold "$CRAP_MAX" || die "$m: CRAP"
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

gate_gen() {
  local dir="$ROOT/proto/gen/go"
  say gen "generated code builds and vets"
  (cd "$dir" && go build ./... && go vet ./...) || die "gen: build"
}

gate_proto() {
  need_tools
  say proto "buf lint + generated code is up to date"
  make -C "$ROOT" proto
  git -C "$ROOT" diff --exit-code -- proto/gen || die "proto: generated code differs from committed code; run make proto and commit"
  make -C "$ROOT" lint-proto || die "proto: buf lint"
}

gate_server() {
  local mode="$1"
  need_tools
  say server "spotless, detekt, tests, coverage >= ${COVERAGE_MIN}%"
  (cd "$ROOT" && ./gradlew --no-daemon -q :server:check) || die "server: check"
  say server "CRAP <= $CRAP_MAX"
  "$BIN/crap" -jacoco "$ROOT/server/build/reports/jacoco/test/jacocoTestReport.xml" -threshold "$CRAP_MAX" || die "server: CRAP"
  [ "$mode" = fast ] && return 0
  say server "mutation testing"
  (cd "$ROOT" && ./gradlew --no-daemon -q :server:mutationTest) || die "server: mutation testing"
}

gate_web() {
  say web "eslint, typecheck, unit tests, build"
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
