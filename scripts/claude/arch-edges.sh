#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Structure facts for the architect agent, in a few dozen lines instead of the
# server's Maven dependency tree.
#
#   Go (agent, agent/plugins/sdk, cli): one "pkg -> pkg" line per package-level
#   edge between Sard packages.
#   Server: forbidden imports found in domain code, or "domain: clean".
#   Then PASS/FAIL for: the SDK and proto import no AGPL package; the agent
#   opens no listener. Exit 1 if any rule fails.
#
# The repository root comes from CLAUDE_PROJECT_DIR or the script location.
set -euo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT"

PREFIX="github.com/Artur-Abalov/sard/"
# Server packages that must stay free of framework, HTTP, gRPC and JPA imports.
# Packages not listed mix Spring wiring with logic and are not checked.
SERVER_DOMAIN=(workflow role metrics)
FORBIDDEN='^import (org\.springframework|jakarta\.|javax\.|org\.hibernate|io\.grpc|dev\.sard\.proto)'

edges() {
  (cd "$1" && go list -f '{{range .Imports}}{{$.ImportPath}} -> {{.}}{{"\n"}}{{end}}' ./...) |
    { grep -F -- "-> $PREFIX" || true; } | sed "s#$PREFIX##g" | sort -u
}

# deps_outside <dir> <allowed prefix>...: Sard packages in the transitive
# imports of <dir> that are outside every allowed prefix.
deps_outside() {
  local dir="$1" pkg ok p
  shift
  (cd "$dir" && go list -deps ./...) | { grep -F -- "$PREFIX" || true; } | while read -r pkg; do
    ok=false
    for p in "$@"; do
      case "$pkg" in "$PREFIX$p"*) ok=true ;; esac
    done
    $ok || echo "$pkg"
  done
}

status=0
rule() { # rule <name> <offenders>
  if [ -z "$2" ]; then
    echo "PASS $1"
  else
    echo "FAIL $1"
    sed 's/^/  /' <<<"$2"
    status=1
  fi
}

for m in agent agent/plugins/sdk cli; do edges "$m"; done

found=""
hits=""
for d in "${SERVER_DOMAIN[@]}"; do
  dir="server/src/main/kotlin/dev/sard/server/$d"
  [ -d "$dir" ] || continue
  hits="$(grep -rnE "$FORBIDDEN" "$dir" || true)"
  [ -z "$hits" ] || found+="${found:+$'\n'}$hits"
done
if [ -z "$found" ]; then echo "domain: clean"; else
  echo "$found"
  status=1
fi

rule "sdk imports no AGPL package" \
  "$(deps_outside agent/plugins/sdk agent/plugins/sdk proto/gen/go)"
rule "proto imports no AGPL package" \
  "$(deps_outside proto/gen/go proto/gen/go)"
listeners="$(grep -rnE --include='*.go' --exclude='*_test.go' \
  'net\.Listen|ListenAndServe|ListenTCP|ListenUnix|\.Serve\(' agent || true)"
rule "agent opens no listener" "$listeners"
exit "$status"
