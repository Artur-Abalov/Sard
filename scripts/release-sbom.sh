#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# SBOMs of a release, CycloneDX JSON by syft (SC1, ADR 00XX-draft-supply-chain):
#
#   release-sbom.sh packages <dist>
#       one per agent tarball, sard-agent_<version>_linux_<arch>.cdx.json: what
#       the tarball, the deb and the rpm of that architecture ship (the same
#       sard-agent and restic binaries); SHA256SUMS is written anew over the
#       packages, the SBOMs and manifest.json, so the minisign signature of
#       SHA256SUMS covers the SBOMs too
#   release-sbom.sh image <outdir> <name> <ref@digest>
#       one per platform of the image, <name>_linux_<arch>.cdx.json
#
# syft runs from the image pinned in tools/images.env.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../tools/images.env
. "$ROOT/tools/images.env"

die() { echo "release-sbom: $*" >&2; exit 1; }

# syft <output directory> <output file> <mount or ""> <source> [syft flags...]:
# the SBOM of the source; registry credentials of the runner's docker login.
syft() {
  local out="$1" file="$2" mount=()
  [ -z "$3" ] || mount=(-v "$3")
  docker run --rm -v "$out:/out" -e DOCKER_CONFIG=/docker -v "${DOCKER_CONFIG:-$HOME/.docker}:/docker:ro" \
    "${mount[@]}" "$TOOL_IMAGE_SYFT" "$4" "${@:5}" --quiet -o "cyclonedx-json=/out/$file"
}

packages() {
  local dist="$1" tarball arch unpacked n=0
  dist="$(cd "$dist" && pwd)"
  [ -f "$dist/manifest.json" ] || die "$dist: no manifest.json"
  for tarball in "$dist"/sard-agent_*_linux_*.tar.gz; do
    [ -f "$tarball" ] || continue
    arch="$(basename "$tarball" .tar.gz)"
    unpacked="$(mktemp -d)"
    tar -xzf "$tarball" -C "$unpacked"
    syft "$dist" "$arch.cdx.json" "$unpacked:/src:ro" dir:/src
    rm -r "$unpacked"
    n=$((n + 1))
  done
  [ "$n" -gt 0 ] || die "$dist: no agent tarball"
  (cd "$dist" && sha256sum -- *.tar.gz *.deb *.rpm *.cdx.json manifest.json >SHA256SUMS)
  echo "release-sbom: $n SBOM(s) in $dist, SHA256SUMS written anew"
}

image() {
  local outdir="$1" name="$2" ref="$3" arch
  [[ "$ref" == *@sha256:* ]] || die "$ref: name the image by digest"
  mkdir -p "$outdir"
  outdir="$(cd "$outdir" && pwd)"
  for arch in amd64 arm64; do
    syft "$outdir" "${name}_linux_$arch.cdx.json" "" "registry:$ref" --platform "linux/$arch"
  done
  echo "release-sbom: $outdir/${name}_linux_{amd64,arm64}.cdx.json"
}

case "${1:-}" in
  packages) [ "$#" -eq 2 ] || die "usage: release-sbom.sh packages <dist>"; packages "$2" ;;
  image) [ "$#" -eq 4 ] || die "usage: release-sbom.sh image <outdir> <name> <ref@digest>"; image "$2" "$3" "$4" ;;
  *) die "usage: release-sbom.sh packages <dist> | image <outdir> <name> <ref@digest>" ;;
esac
