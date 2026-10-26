# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Sourced by the test scripts. test_image <name:tag> prints the reference
# test/images.env pins for that tag ("debian:12" → "debian:12@sha256:…") and
# fails when the tag is not pinned there, so a test never pulls by tag (SC1).
# TEST_IMAGES_FILE overrides the file (the test of this function).

test_image() {
  local file="${TEST_IMAGES_FILE:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/test/images.env}" ref
  ref="$(awk -F= -v want="$1@" '$0 !~ /^[ \t]*#/ && index($2, want) == 1 { print $2; exit }' "$file")"
  if [ -z "$ref" ]; then
    echo "test_image: $1 is not pinned in $file" >&2
    return 1
  fi
  echo "$ref"
}
