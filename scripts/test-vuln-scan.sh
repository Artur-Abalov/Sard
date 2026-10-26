#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The scans block what they must (SC1): on the fixtures of test/vuln, which
# hold known vulnerable versions on purpose (a Go module calling
# golang.org/x/text v0.3.7, an npm lock file with lodash 4.17.15),
#   - with no exceptions, scripts/vuln-scan.sh fails (exit 2) and names them;
#   - with an exception per blocking ID, valid for 30 days, it passes;
#   - with the same exceptions expired, it fails (exit 1).
# Needs the network (vulnerability databases) and Docker (grype): CI.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-vuln-scan: FAIL: $*" >&2; exit 1; }
say() { echo "test-vuln-scan: $*"; }

scan() { # want exit, mode, dir
  local rc=0
  VULN_OUT="$WORK/out" VULN_EXCEPTIONS="$WORK/exceptions" GOWORK=off \
    "$ROOT/scripts/vuln-scan.sh" "$2" "$3" >"$WORK/verdict" 2>&1 || rc=$?
  [ "$rc" -eq "$1" ] || { cat "$WORK/verdict" >&2; die "$2 $3: exit $rc, want $1"; }
}

case_of() { # mode, dir
  : >"$WORK/exceptions"
  scan 2 "$1" "$2"
  local ids
  ids="$(awk '$1 == "BLOCKING" { print $2 }' "$WORK/verdict" | sort -u)"
  [ -n "$ids" ] || die "$1 $2: no blocking ID in: $(cat "$WORK/verdict")"
  say "ok: $1 $2 blocks $(tr '\n' ' ' <<<"$ids")"

  local until
  until="$(date -u -d '+30 days' +%F)"
  while read -r id; do
    echo "$id $until fixture of scripts/test-vuln-scan.sh: vulnerable on purpose"
  done <<<"$ids" >"$WORK/exceptions"
  scan 0 "$1" "$2"
  grep -q '^EXCEPTED ' "$WORK/verdict" || die "$1 $2: excepted findings not shown"
  say "ok: $1 $2 passes with an exception per ID"

  sed -i "s/ $until / $(date -u -d '-1 day' +%F) /" "$WORK/exceptions"
  scan 1 "$1" "$2"
  grep -q 'expired on' "$WORK/verdict" || die "$1 $2: expired exception not named"
  say "ok: $1 $2 fails on an expired exception"
}

case_of go test/vuln/go
case_of deps test/vuln/npm

echo "test-vuln-scan: OK"
