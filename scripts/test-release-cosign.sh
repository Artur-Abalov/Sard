#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/release-cosign.sh without Docker or a registry: an
# images.json that names no image fails every mode before cosign runs, so a
# release cannot pass its image check vacuously. docker on PATH is a stub
# that fails the test if it is ever called.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-release-cosign: FAIL: $*" >&2; exit 1; }

mkdir "$WORK/bin"
printf '#!/bin/sh\necho "docker called: $*" >&2\nexit 99\n' >"$WORK/bin/docker"
chmod +x "$WORK/bin/docker"
touch "$WORK/cosign.pub"

expect_refused() { # description, images.json content, mode arguments...
  local what="$1" rc=0 out
  printf '%s' "$2" >"$WORK/images.json"
  shift 2
  out="$(PATH="$WORK/bin:$PATH" COSIGN_PRIVATE_KEY=k COSIGN_PASSWORD=p "$HERE/release-cosign.sh" "$@" 2>&1)" || rc=$?
  [ "$rc" -eq 1 ] || die "$what: exit $rc, want 1: $out"
  grep -q "docker called" <<<"$out" && die "$what: cosign ran: $out"
  grep -q "no images" <<<"$out" || die "$what: reason not given: $out"
  echo "ok: $what"
}

for mode in sign attest verify; do
  args=("$mode" "$WORK/images.json")
  [ "$mode" = attest ] && args+=("$WORK")
  [ "$mode" = verify ] && args+=("$WORK/cosign.pub")
  expect_refused "$mode: an empty list" '{"version":"v1.0.0","images":[]}' "${args[@]}"
  expect_refused "$mode: no images field" '{"version":"v1.0.0"}' "${args[@]}"
  expect_refused "$mode: not JSON" 'oops' "${args[@]}"
done

echo "test-release-cosign: OK"
