#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# cosign over the images of a release (SC1, ADR 00XX-draft-supply-chain), by
# the digests in images.json (written by release.yml, job image):
#
#   release-cosign.sh sign <images.json>
#       signs every image and each of its platforms (--recursive) with the
#       key in COSIGN_PRIVATE_KEY (password COSIGN_PASSWORD); the signature
#       is also logged in Rekor, the public transparency log
#   release-cosign.sh attest <images.json> <sbom dir>
#       attaches <name>_linux_amd64.cdx.json and _arm64 (release-sbom.sh) to
#       the image as signed CycloneDX attestations, on the digest of its
#       index: the one a tag resolves to, so an operator checks them by tag
#   release-cosign.sh verify <images.json> [<public key>]
#       both checks an operator runs (docs/operator/10-security.md), with
#       deploy/release/cosign.pub by default
#
# images.json: {"version": "vX.Y.Z", "images": [{"name": "ghcr.io/o/sard-server",
# "sbom": "sard-server", "digest": "sha256:…"}, …]}. cosign runs from the image
# pinned in tools/images.env; the key reaches it as an environment variable
# only, never as an argument, so it is in no command line and no log.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../tools/images.env
. "$ROOT/tools/images.env"

die() { echo "release-cosign: $*" >&2; exit 1; }

cosign() { # extra docker run flags... -- cosign arguments...
  local flags=()
  while [ "$1" != -- ]; do flags+=("$1"); shift; done
  shift
  docker run --rm -e COSIGN_PRIVATE_KEY -e COSIGN_PASSWORD -e COSIGN_YES=true \
    -e DOCKER_CONFIG=/docker -v "${DOCKER_CONFIG:-$HOME/.docker}:/docker:ro" \
    "${flags[@]}" "$TOOL_IMAGE_COSIGN" "$@"
}

# images <images.json>: "<name>@<digest> <sbom name>" per image; a file that
# names none is an error, so no mode can pass by doing nothing. Called as
# list="$(images …)" in the main shell, where its failure stops the script.
images() {
  jq -er '.images | select(length > 0) | .[] | "\(.name)@\(.digest) \(.sbom)"' "$1" 2>/dev/null || die "$1: no images"
}

sign() {
  local ref _sbom list
  [ -n "${COSIGN_PRIVATE_KEY:-}" ] || die "COSIGN_PRIVATE_KEY is empty"
  [ -n "${COSIGN_PASSWORD:-}" ] || die "COSIGN_PASSWORD is empty"
  list="$(images "$1")"
  version="$(jq -er .version "$1")"
  while read -r ref _sbom; do
    cosign -- sign --recursive --key env://COSIGN_PRIVATE_KEY -a "sard.version=$version" "$ref"
  done <<<"$list"
}

attest() {
  local ref sbom arch sboms list
  [ -n "${COSIGN_PRIVATE_KEY:-}" ] || die "COSIGN_PRIVATE_KEY is empty"
  list="$(images "$1")"
  sboms="$(cd "$2" && pwd)"
  while read -r ref sbom; do
    for arch in amd64 arm64; do
      [ -f "$sboms/${sbom}_linux_$arch.cdx.json" ] || die "no SBOM ${sbom}_linux_$arch.cdx.json in $2"
      cosign -v "$sboms:/sbom:ro" -- attest --key env://COSIGN_PRIVATE_KEY --type cyclonedx \
        --predicate "/sbom/${sbom}_linux_$arch.cdx.json" "$ref"
    done
  done <<<"$list"
}

verify() {
  local pub="${2:-$ROOT/deploy/release/cosign.pub}" ref _sbom list
  [ -f "$pub" ] || die "public key $pub missing"
  list="$(images "$1")"
  pub="$(cd "$(dirname "$pub")" && pwd)/$(basename "$pub")"
  while read -r ref _sbom; do
    cosign -v "$pub:/cosign.pub:ro" -- verify --key /cosign.pub "$ref" >/dev/null
    cosign -v "$pub:/cosign.pub:ro" -- verify-attestation --key /cosign.pub --type cyclonedx "$ref" >/dev/null
    echo "release-cosign: $ref signed, its SBOMs attested"
  done <<<"$list"
}

case "${1:-}" in
  sign) [ "$#" -eq 2 ] || die "usage: release-cosign.sh sign <images.json>"; sign "$2" ;;
  attest) [ "$#" -eq 3 ] || die "usage: release-cosign.sh attest <images.json> <sbom dir>"; attest "$2" "$3" ;;
  verify) [ "$#" -ge 2 ] || die "usage: release-cosign.sh verify <images.json> [<public key>]"; verify "$2" "${3:-}" ;;
  *) die "usage: release-cosign.sh sign|attest|verify <images.json> ..." ;;
esac
