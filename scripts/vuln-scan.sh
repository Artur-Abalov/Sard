#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Vulnerability scans of SC1 (ADR 00XX-draft-supply-chain); the threshold and
# the exceptions are applied by vulngate (tools/cmd/vulngate):
#
#   scripts/vuln-scan.sh go [<module dir>...]   govulncheck: what the Go code calls
#                                               (default: the modules of go.work)
#   scripts/vuln-scan.sh deps [<dir>]           grype: the lock files of the server
#                                               and web (Gradle, npm; Go is govulncheck's)
#   scripts/vuln-scan.sh image <ref>...         grype: images from their registry
#
# Exit codes are vulngate's: 0 passes, 1 error, 2 blocking findings. The
# reports and vulngate's verdict are kept in VULN_OUT (default build/vuln).
# VULN_EXCEPTIONS overrides security/vuln-exceptions (the test of this script).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/.bin"
OUT="${VULN_OUT:-$ROOT/build/vuln}"
EXCEPTIONS="${VULN_EXCEPTIONS:-$ROOT/security/vuln-exceptions}"
# shellcheck source=../tools/images.env
. "$ROOT/tools/images.env"

die() { echo "vuln-scan: $*" >&2; exit 1; }

gate() { # format, reports...
  local format="$1" rc=0
  shift
  "$BIN/vulngate" -format "$format" -exceptions "$EXCEPTIONS" "$@" >"$OUT/verdict.txt" || rc=$?
  cat "$OUT/verdict.txt"
  return "$rc"
}

grype() { # target, report
  docker run --rm -v "$ROOT:/src:ro" -v "$OUT:/out" -e GRYPE_DB_AUTO_UPDATE=true \
    "$TOOL_IMAGE_GRYPE" "$1" --quiet -o json --file "/out/$2" \
    --exclude './**/go.mod' --exclude './**/go.sum' --exclude './**/node_modules/**' \
    --exclude './**/build/**' --exclude './.bin/**' --exclude './test/vuln/**'
}

scan_go() {
  local reports=() dir name
  [ "$#" -gt 0 ] || set -- agent agent/plugins/sdk cli proto/gen/go
  for dir in "$@"; do
    name="govulncheck-${dir//\//-}.json"
    (cd "$ROOT/$dir" && "$BIN/govulncheck" -format json ./...) >"$OUT/$name"
    reports+=("$OUT/$name")
  done
  gate govulncheck "${reports[@]}"
}

scan_deps() {
  local dir="${1:-.}"
  grype "dir:/src/$dir" grype-deps.json
  gate grype "$OUT/grype-deps.json"
}

scan_images() {
  local reports=() ref name
  [ "$#" -gt 0 ] || die "image: no image given"
  for ref in "$@"; do
    name="grype-$(tr -c 'A-Za-z0-9._-' '-' <<<"$ref").json"
    grype "registry:$ref" "$name"
    reports+=("$OUT/$name")
  done
  gate grype "${reports[@]}"
}

mkdir -p "$OUT"
[ -x "$BIN/vulngate" ] || make -C "$ROOT" tools >/dev/null
case "${1:-}" in
  go)
    [ -x "$BIN/govulncheck" ] || make -C "$ROOT" ci-tools >/dev/null
    shift
    scan_go "$@"
    ;;
  deps) shift; scan_deps "$@" ;;
  image) shift; scan_images "$@" ;;
  *) die "usage: vuln-scan.sh go [<module dir>...] | deps [<dir>] | image <ref>..." ;;
esac
