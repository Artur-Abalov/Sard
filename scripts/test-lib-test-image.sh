#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of test_image (scripts/lib/test-image.sh) on a throwaway pin file.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/test-image.sh
. "$HERE/lib/test-image.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-lib-test-image: FAIL: $*" >&2; exit 1; }

readonly D12=sha256:854dd8ab51d62866dbd8f5431d15ce77b12983a2a4a1e1fe43a2395dc7ef9f61
readonly D13=sha256:4c25275446ca371766b8b093c696782eabce06263fccacb8d5d813b3dc44b308
export TEST_IMAGES_FILE="$WORK/images.env"
cat >"$TEST_IMAGES_FILE" <<PINS
# TEST_IMAGE_OLD=debian:11@$D12
TEST_IMAGE_DEBIAN_12=debian:12@$D12
TEST_IMAGE_DEBIAN_13=debian:13@$D13
TEST_IMAGE_ROCKY_9=rockylinux/rockylinux:9@$D13
PINS

[ "$(test_image debian:12)" = "debian:12@$D12" ] || die "debian:12"
[ "$(test_image debian:13)" = "debian:13@$D13" ] || die "debian:13"
[ "$(test_image rockylinux/rockylinux:9)" = "rockylinux/rockylinux:9@$D13" ] || die "a name with a slash"
for missing in debian:1 debian debian:11 rockylinux:9 ubuntu:24.04; do
  if out="$(test_image "$missing" 2>&1)"; then die "$missing: pinned as $out"; fi
  grep -q "$missing is not pinned" <<<"$out" || die "$missing: no reason in: $out"
done
unset TEST_IMAGES_FILE
[ "$(test_image debian:12)" = "$(grep '^TEST_IMAGE_DEBIAN_12=' "$HERE/../test/images.env" | cut -d= -f2)" ] ||
  die "the repository's test/images.env is not the default"

echo "test-lib-test-image: OK"
