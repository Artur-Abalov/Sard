#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# SubagentStop hook: the fast gate (make gate-fast M=<module>) for the modules
# this branch changes, against origin/main, committed or not, and the modules
# that depend on them. CI still runs every gate.
#
#   scripts/claude/gate-changed.sh             run the gates
#   scripts/claude/gate-changed.sh --dry-run   print the modules only
#   scripts/claude/gate-changed.sh --dry-run <path>...   modules for these paths
set -uo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT" || exit 1
ORDER=(proto gen sdk tools agent cli server web)
GO=(gen sdk tools agent cli)

# modules_for <path>: the modules whose gate a change of this path can break.
modules_for() {
  case "$1" in
    docs/* | *.md) ;;
    proto/gen/go/*) echo gen agent cli ;;
    proto/jvm/*) echo server ;;
    proto/*) echo proto gen agent cli server ;;
    agent/plugins/sdk/*) echo sdk agent ;;
    agent/*) echo agent server ;; # the server's seam tests run the real agent
    cli/*) echo cli ;;
    tools/*) echo tools ;;
    server/* | build.gradle.kts | settings.gradle.kts | gradle.properties | gradle/* | gradlew) echo server ;;
    web/*) echo web ;;
    scripts/gate.sh | Makefile) echo "${ORDER[@]}" ;;
    scripts/mutate-exec.sh | scripts/mutation-weights/* | scripts/lib/lpt.awk | .golangci.yml | go.work | go.work.sum) echo "${GO[@]}" ;;
  esac
}

changed_paths() {
  local base
  base="$(git merge-base HEAD origin/main 2>/dev/null || git rev-parse HEAD)"
  { git diff --name-only "$base"; git ls-files --others --exclude-standard; } | sort -u
}

dry=false
if [ "${1:-}" = --dry-run ]; then
  dry=true
  shift
fi
if [ "$#" -gt 0 ]; then paths="$(printf '%s\n' "$@")"; else paths="$(changed_paths)"; fi

declare -A wanted=()
while read -r p; do
  [ -n "$p" ] || continue
  for m in $(modules_for "$p"); do wanted[$m]=1; done
done <<<"$paths"

modules=()
for m in "${ORDER[@]}"; do [ -n "${wanted[$m]:-}" ] && modules+=("$m"); done

if [ "${#modules[@]}" -eq 0 ]; then
  echo "gate-changed: no module changed against origin/main"
  exit 0
fi
echo "gate-changed: ${modules[*]}"
$dry && exit 0

status=0
for m in "${modules[@]}"; do
  make -C "$ROOT" gate-fast M="$m" || status=1
done
exit "$status"
