#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/check-pins.sh on a throwaway git repository.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/check-pins.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-check-pins: FAIL: $*" >&2; exit 1; }

readonly SHA=3d3c42e5aac5ba805825da76410c181273ba90b1
readonly DIGEST=sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5

git -C "$WORK" init -q
mkdir -p "$WORK/.github/workflows"
printf 'jobs:\n  a:\n    steps:\n      - uses: actions/checkout@%s # v7.0.1\n' "$SHA" >"$WORK/.github/workflows/ci.yml"

out="$("$CHECK" "$WORK")" || die "clean tree failed: $out"
grep -q 'OK' <<<"$out" || die "clean tree: no OK in: $out"

expect_rc() { # want, description
  local want="$1" rc=0 out
  out="$("$CHECK" "$WORK" 2>&1)" || rc=$?
  [ "$rc" -eq "$want" ] || die "$2: exit $rc, want $want: $out"
  [ "$want" -eq 0 ] || grep -q 'FAILED' <<<"$out" || die "$2: no FAILED in: $out"
}

# expect_file_rc <want> <description> <path relative to WORK> <content>
# Writes the content, runs the check, removes the file again.
expect_file_rc() {
  mkdir -p "$(dirname "$WORK/$3")"
  printf '%s\n' "$4" >"$WORK/$3"
  expect_rc "$1" "$2"
  rm "$WORK/$3"
}

rc=0
printf '      - uses: actions/checkout@v7\n' >"$WORK/.github/workflows/tag.yml"
out="$("$CHECK" "$WORK")" || rc=$?
[ "$rc" -eq 1 ] || die "action by tag: exit $rc, want 1"
grep -q 'check-pins: .github/workflows/tag.yml:1: ' <<<"$out" || die "offender not named: $out"
rm "$WORK/.github/workflows/tag.yml"

W=.github/workflows/x.yml
expect_file_rc 0 "action by SHA with version" "$W" "      - uses: actions/checkout@$SHA # v7.0.1"
expect_file_rc 0 "action without the dash" "$W" "        uses: gradle/actions/setup-gradle@$SHA # v6.4.0"
expect_file_rc 0 "quoted action" "$W" "      - uses: \"actions/checkout@$SHA\" # v7.0.1"
expect_file_rc 1 "action by SHA, no version comment" "$W" "      - uses: actions/checkout@$SHA"
expect_file_rc 1 "comment that is no version" "$W" "      - uses: actions/checkout@$SHA # latest"
expect_file_rc 1 "short SHA" "$W" "      - uses: actions/checkout@3d3c42e5 # v7.0.1"
expect_file_rc 1 "action by branch" "$W" "      - uses: actions/checkout@main # v7"
expect_file_rc 1 "action without a ref" "$W" "      - uses: actions/checkout"
expect_file_rc 0 "local action" "$W" "      - uses: ./.github/actions/setup"
expect_file_rc 0 "docker action by digest" "$W" "      - uses: docker://alpine:3.22@$DIGEST"
expect_file_rc 1 "docker action by tag" "$W" "      - uses: docker://alpine:3.22"
expect_file_rc 1 "reusable workflow by tag" "$W" "    uses: org/repo/.github/workflows/w.yml@v1"
expect_file_rc 1 "container by tag" "$W" "    container: node:24"
expect_file_rc 1 "service image by tag" "$W" "        image: postgres:18"
expect_file_rc 0 "service image by digest" "$W" "        image: postgres:18@$DIGEST"
expect_file_rc 1 "uses in .github/actions" .github/actions/a/action.yml "    - uses: actions/cache@v4"
expect_file_rc 1 ".yaml workflow" .github/workflows/y.yaml "      - uses: actions/cache@v4"

D=deploy/Dockerfile
expect_file_rc 1 "FROM by tag" "$D" "FROM ubuntu:24.04"
expect_file_rc 1 "FROM without a tag" "$D" "FROM ubuntu"
expect_file_rc 0 "FROM by digest" "$D" "FROM ubuntu:24.04@$DIGEST"
expect_file_rc 0 "FROM by digest, lower case and stage" "$D" "from --platform=\$BUILDPLATFORM ubuntu:24.04@$DIGEST AS unpack"
expect_file_rc 1 "FROM by tag with platform" "$D" "FROM --platform=\$BUILDPLATFORM ubuntu:24.04 AS unpack"
expect_file_rc 0 "FROM scratch" "$D" "FROM scratch AS out"
expect_file_rc 0 "FROM an earlier stage" "$D" $'FROM ubuntu:24.04@'"$DIGEST"$' AS build\nFROM build'
expect_file_rc 0 "FROM an ARG pinned by digest" "$D" $'ARG BASE_IMAGE=ubuntu:24.04@'"$DIGEST"$'\nFROM ${BASE_IMAGE}'
expect_file_rc 1 "FROM an ARG by tag" "$D" $'ARG BASE_IMAGE=ubuntu:24.04\nFROM ${BASE_IMAGE}'
expect_file_rc 1 "FROM an ARG without a default" "$D" $'ARG BASE_IMAGE\nFROM $BASE_IMAGE'
expect_file_rc 1 "Dockerfile.suffix" test/Dockerfile.postgres "FROM postgres:18"
expect_file_rc 1 "name.Dockerfile" test/host.Dockerfile "FROM debian:12"
expect_file_rc 0 "an ARG FROM never uses" "$D" $'ARG VERSION=1.2\nFROM scratch'

C=deploy/docker-compose.yml
expect_file_rc 1 "compose image by tag" "$C" "    image: postgres:18-alpine"
expect_file_rc 0 "compose image by digest" "$C" "    image: postgres:18-alpine@$DIGEST"
expect_file_rc 0 "compose image, default by digest" "$C" "    image: \${SARD_POSTGRES_IMAGE:-postgres:18.6-alpine@$DIGEST}"
expect_file_rc 1 "compose image, default by tag" "$C" "    image: \${SARD_POSTGRES_IMAGE:-postgres:18.6-alpine}"
expect_file_rc 0 "compose image, reason on the line above" "$C" \
  $'    # allow-unpinned: our release image, signed with cosign\n    image: ghcr.io/o/sard-server:${SARD_VERSION}'
expect_file_rc 0 "compose image, reason on the same line" "$C" \
  '    image: sard-server:dev # allow-unpinned: built locally by this file'
expect_file_rc 1 "compose image, empty reason" "$C" '    image: sard-server:dev # allow-unpinned:'
expect_file_rc 1 "compose image, reason two lines above" "$C" \
  $'    # allow-unpinned: reason\n    build: .\n    image: sard-server:dev'
expect_file_rc 1 "compose.yaml" compose.yaml "    image: redis:8"

E=deploy/postgres.env
expect_file_rc 1 "env image by tag" "$E" "SARD_POSTGRES_IMAGE=postgres:18.6-alpine"
expect_file_rc 0 "env image by digest" "$E" "SARD_POSTGRES_IMAGE=postgres:18.6-alpine@$DIGEST"
expect_file_rc 0 "env, not an image" "$E" 'SARD_POSTGRES_INITDB_ARGS="--data-checksums"'
expect_file_rc 1 "env.example image by tag" deploy/.env.example "SARD_IMAGE=ghcr.io/o/sard-server"
expect_file_rc 1 "test images by tag" test/images.env "TEST_IMAGE_DEBIAN_12=debian:12"
expect_file_rc 0 "test images by digest" test/images.env "TEST_IMAGE_DEBIAN_12=debian:12@$DIGEST"

expect_file_rc 0 "the check script itself" scripts/check-pins.sh "FROM ubuntu:24.04"
expect_file_rc 0 "the test script itself" scripts/test-check-pins.sh "      - uses: actions/checkout@v7"
expect_file_rc 0 "docs are history and prose" docs/x.md "      - uses: actions/checkout@v7"

echo "test-check-pins: OK"
