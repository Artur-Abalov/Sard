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
#
# As a hook it reads the SubagentStop input on stdin and does nothing for the
# agents that change no files (specifier, architect); an empty or unknown
# agent_type still runs the gates. A tree that already passed the same modules
# is not gated again (cache in build/.gate-ok/); GATE_NO_CACHE=1 disables that.
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
    scripts/mutate-exec.sh | scripts/mutation-weights/* | scripts/lib/lpt.awk | scripts/lib/covering-tests.awk | .golangci.yml | go.work | go.work.sum) echo "${GO[@]}" ;;
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
# Hook input (SubagentStop): skip agents that change nothing.
if ! $dry && [ ! -t 0 ]; then
  agent_type="$(timeout 2 cat 2>/dev/null | jq -r '.agent_type // empty' 2>/dev/null || true)"
  case "$agent_type" in
    specifier | architect)
      echo "gate-changed: skipped, $agent_type changes no files"
      exit 0
      ;;
  esac
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

# A pass is cached by the module list plus a hash of the working tree against
# the base (tracked diff and untracked files), so an unchanged tree is not gated twice.
base="$(git merge-base HEAD origin/main 2>/dev/null || git rev-parse HEAD)"
key="$({
  echo "${modules[*]}"
  git diff "$base"
  git ls-files --others --exclude-standard -z | xargs -0 -r sha256sum
} | sha256sum | cut -d' ' -f1)"
cache="$ROOT/build/.gate-ok/$key"
if [ -z "${GATE_NO_CACHE:-}" ] && [ -e "$cache" ]; then
  echo "gate-changed: same tree already passed, skipped"
  exit 0
fi

status=0
for m in "${modules[@]}"; do
  make -C "$ROOT" gate-fast M="$m" || status=1
done
if [ "$status" -eq 0 ]; then
  mkdir -p "$ROOT/build/.gate-ok"
  : >"$cache"
fi
exit "$status"
