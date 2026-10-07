#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The one rule from a git tag to the versions of a release
# (docs/adr/0047-release-versions.md). Every other script and workflow asks
# this one instead of parsing a version itself.
#
#   release-version.sh check TAG          exit 0 for a release tag, else name the form and exit 1
#   release-version.sh image TAG          the image tag: v0.1.0-beta.1 → 0.1.0-beta.1
#   release-version.sh package VERSION    deb/rpm version: v0.1.0-beta.1 → 0.1.0~beta.1,
#                                         v0.1.0 → 0.1.0, anything else → 0.0.0~dev.<…>
#   release-version.sh deb-file VERSION ARCH   sard-agent_<VERSION>_<arch>.deb
#   release-version.sh rpm-file VERSION ARCH   sard-agent-<VERSION>.<x86_64|aarch64>.rpm
#
# A release tag is vX.Y.Z, vX.Y.Z-beta.N or vX.Y.Z-rc.N: numbers without
# leading zeros, N from 1. The pre-release counter follows a dot so that
# SemVer compares it as a number (rc.2 < rc.10; rc2 > rc10).
# "~" sorts a deb/rpm pre-release below its release; it stays inside the
# package, never in a file name: GitHub renames "~" in release assets to ".",
# and the signed SHA256SUMS would no longer name the files.
set -euo pipefail

NUM='(0|[1-9][0-9]*)'
RELEASE_TAG="^v$NUM\.$NUM\.$NUM(-(beta|rc)\.[1-9][0-9]*)?$"
# Characters of a version that may appear in a file name.
FILE_SAFE='^[A-Za-z0-9][A-Za-z0-9._+-]*$'

die() {
  echo "release-version: $*" >&2
  exit 1
}

is_release() { [[ "$1" =~ $RELEASE_TAG ]]; }

check() {
  is_release "$1" || die "'$1' is not a release tag: want vX.Y.Z, vX.Y.Z-beta.N or vX.Y.Z-rc.N (N from 1, no leading zeros)"
}

image() {
  check "$1"
  echo "${1#v}"
}

package() {
  local plain
  if is_release "$1"; then
    plain="${1#v}"
    echo "${plain/-/\~}"
  else
    echo "0.0.0~dev.${1//[^A-Za-z0-9.]/.}"
  fi
}

file_version() {
  [[ "$1" =~ $FILE_SAFE ]] || die "'$1' cannot be part of a file name (letters, digits, . _ + - only)"
}

rpm_arch() {
  case "$1" in
    amd64) echo x86_64 ;;
    arm64) echo aarch64 ;;
    *) die "unsupported architecture '$1' (amd64, arm64)" ;;
  esac
}

case "${1:-}" in
  check) [ "$#" -eq 2 ] || die "usage: check TAG"; check "$2" ;;
  image) [ "$#" -eq 2 ] || die "usage: image TAG"; image "$2" ;;
  package) [ "$#" -eq 2 ] || die "usage: package VERSION"; package "$2" ;;
  deb-file)
    [ "$#" -eq 3 ] || die "usage: deb-file VERSION ARCH"
    file_version "$2"
    rpm_arch "$3" >/dev/null
    echo "sard-agent_$2_$3.deb"
    ;;
  rpm-file)
    [ "$#" -eq 3 ] || die "usage: rpm-file VERSION ARCH"
    file_version "$2"
    echo "sard-agent-$2.$(rpm_arch "$3").rpm"
    ;;
  *) die "usage: release-version.sh check|image|package|deb-file|rpm-file …" ;;
esac
