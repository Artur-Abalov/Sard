#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Prints the CRAP table for one module (worst first) without failing the
# gate. Used by the cleaner agent.
#
#   ./scripts/crap.sh <sdk|agent|cli|tools|server> [top]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/.bin"
m="${1:?usage: crap.sh <module> [top]}"
top="${2:-30}"

[ -x "$BIN/crap" ] || make -C "$ROOT" tools

case "$m" in
  server)
    (cd "$ROOT" && ./gradlew --no-daemon -q :server:test :server:jacocoTestReport)
    "$BIN/crap" -jacoco "$ROOT/server/build/reports/jacoco/test/jacocoTestReport.xml" -top "$top" || true
    ;;
  sdk | agent | cli | tools)
    dir="$ROOT/$m"
    [ "$m" = sdk ] && dir="$ROOT/agent/plugins/sdk"
    gowork="${GOWORK:-}"
    [ "$m" = tools ] && gowork=off
    pkgs="$(cd "$dir" && GOWORK="$gowork" go list ./... | paste -sd, -)"
    (cd "$dir" && GOWORK="$gowork" go test -count=1 -coverpkg="$pkgs" -coverprofile=.cover.out ./... >/dev/null)
    "$BIN/crap" -go-profile "$dir/.cover.out" -go-src "$dir" -top "$top" || true
    ;;
  *)
    echo "crap.sh: no CRAP measurement for '$m'" >&2
    exit 1
    ;;
esac
