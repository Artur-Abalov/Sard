#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# PostToolUse hook for Edit|Write: runs the fastest meaningful check for
# the edited file and reports failures back to the agent (exit 2).
set -uo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
path="$(jq -r '.tool_input.file_path // empty')"
[ -z "$path" ] && exit 0
rel="${path#"$ROOT"/}"

report() {
  local out
  if ! out="$("$@" 2>&1)"; then
    echo "on-edit: '$*' failed after editing $rel:" >&2
    tail -40 <<<"$out" >&2
    exit 2
  fi
}

# Nearest directory containing go.mod, walking up from the edited file.
go_module_dir() {
  local d
  d="$(dirname "$path")"
  while [ "$d" != "$ROOT" ] && [ "$d" != / ]; do
    [ -f "$d/go.mod" ] && { echo "$d"; return; }
    d="$(dirname "$d")"
  done
}

case "$rel" in
  proto/gen/*) exit 0 ;;
  *.go)
    mod="$(go_module_dir)"
    [ -n "$mod" ] || exit 0
    pkg="./$(realpath --relative-to="$mod" "$(dirname "$path")")"
    gowork="${GOWORK:-}"
    [ "$mod" = "$ROOT/tools" ] && gowork=off
    report bash -c "cd '$mod' && GOWORK='$gowork' go vet '$pkg' && GOWORK='$gowork' go test -count=1 '$pkg'"
    ;;
  server/*.kt | server/*.kts)
    report bash -c "cd '$ROOT' && LC_ALL=C.UTF-8 ./gradlew --no-daemon -q :server:compileTestKotlin"
    ;;
  proto/*.proto)
    report make -C "$ROOT" lint-proto
    ;;
  web/src/*.ts | web/src/*.tsx)
    [ -d "$ROOT/web/node_modules" ] || exit 0
    report npm --prefix "$ROOT/web" run --silent typecheck
    ;;
esac
exit 0
