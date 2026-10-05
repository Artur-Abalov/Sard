#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The server image carries only agent packages of its own version, intact
# (deploy/server/Dockerfile, stage agent-packages;
# docs/adr/00XX-draft-agent-release.md). Builds that stage alone, no Gradle:
#
#   scripts/test-image-packages.sh <version>      after make package VERSION=<version>
#
# - packages of <version> → the stage builds;
# - another server version → the build fails, naming both versions;
# - a file that does not match SHA256SUMS → the build fails;
# - no packages → the build fails, pointing to make package.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
die() { echo "test-image-packages: FAIL: $*" >&2; exit 1; }

[ "$#" -eq 1 ] || die "usage: test-image-packages.sh <version>"
VERSION="$1"
[ -f "$ROOT/dist/manifest.json" ] || die "dist/ is empty: run make package VERSION=$VERSION"
# A tampered or empty copy must sit inside the build context.
mkdir -p "$ROOT/test/packages/build"
WORK="$(mktemp -d "$ROOT/test/packages/build/image.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# stage <packages dir relative to the repo> <server version>: build output; status as docker's.
stage() {
  docker build --progress=plain --target agent-packages --build-arg AGENT_PACKAGES="$1" \
    --build-arg SARD_VERSION="$2" -f "$ROOT/deploy/server/Dockerfile" "$ROOT" 2>&1
}

builds() {
  stage "$1" "$2" >/dev/null || die "$3: refused"
  echo "ok: $3 — builds"
}

fails() {
  local out
  ! out="$(stage "$1" "$2")" || die "$3: built"
  grep -qF -- "$4" <<<"$out" || die "$3: failed for another reason: $(tail -20 <<<"$out")"
  echo "ok: $3 — fails ($4)"
}

rel="${WORK#"$ROOT"/}"
builds dist "$VERSION" "packages of the server's version"
fails dist "$VERSION-other" "a server of another version" \
  "agent packages are version '$VERSION', the server is '$VERSION-other'"

mkdir "$WORK/tampered"
cp "$ROOT"/dist/*.tar.gz "$ROOT"/dist/*.deb "$ROOT"/dist/*.rpm "$ROOT"/dist/manifest.json "$ROOT"/dist/SHA256SUMS "$WORK/tampered/"
deb="$(cd "$WORK/tampered" && ls -1 ./*.deb | head -1)"
printf 'x' >>"$WORK/tampered/$deb"
fails "$rel/tampered" "$VERSION" "a package that does not match SHA256SUMS" "FAILED"

mkdir "$WORK/empty"
fails "$rel/empty" "$VERSION" "no packages" "run make package"
echo "test-image-packages: all checks passed"
