#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
#   scripts/claude/test-quiet.sh <module> [filter]
#
# The fast test loop for a module: prints only failures and the final summary
# line. Exit code is the test command's. The full gate is scripts/gate.sh.
#   server  gradle test, filter = --tests pattern (a class name or glob)
#   Go      go test ./... , filter = -run regexp
#   web     npm test
set -euo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT"
module="${1:?usage: test-quiet.sh <module> [filter]}"
filter="${2:-}"

# Cyrillic test class names are real here: keep a UTF-8 locale.
export LC_ALL=C.UTF-8

run_server() {
  local args=(-q --console=plain :server:test)
  [ -z "$filter" ] || args+=(--tests "$filter")
  ./gradlew "${args[@]}" 2>&1 | { grep -v '^Picked up JAVA_TOOL_OPTIONS' || true; }
}

run_go() {
  local dir="$1" args=(./...)
  [ -z "$filter" ] || args+=(-run "$filter")
  (cd "$dir" && GOWORK="$([ "$dir" = tools ] && echo off || echo "${GOWORK:-}")" go test "${args[@]}") 2>&1 | { grep -v '^ok' || true; }
}

run_web() {
  npm --prefix web test --silent 2>&1 | tail -n 15
}

case "$module" in
  server) run_server ;;
  agent | cli | tools | agent/plugins/sdk | sdk) run_go "$([ "$module" = sdk ] && echo agent/plugins/sdk || echo "$module")" ;;
  web) run_web ;;
  *)
    echo "test-quiet: unknown module '$module' (server agent cli tools sdk web)" >&2
    exit 2
    ;;
esac
