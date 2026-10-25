#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fetches the restic release pinned in agent/internal/restic/restic-version
# into .bin/restic/<version>/linux_<arch>/ (ignored by git), next to restic's
# BSD-2 license text (docs/adr/0017-restic-shipped-with-agent.md).
#
#   scripts/fetch-restic.sh              host architecture
#   scripts/fetch-restic.sh amd64 arm64  both release architectures
#   scripts/fetch-restic.sh --update-pins   after version= changed (Renovate):
#                                          the SHA-256 lines from the signed SHA256SUMS
#
# The archive must match the SHA-256 pinned in the version file AND the line
# in the release's SHA256SUMS, and SHA256SUMS must carry a GPG signature by
# the key with the pinned fingerprint (SC1: every build, not only a release).
# The key is agent/internal/restic/restic-signing-key.asc, or the file
# RESTIC_SIGNING_KEY names; without gpg or the key nothing is installed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_FILE="$ROOT/agent/internal/restic/restic-version"

pinned() {
  local value
  value="$(sed -n "s/^$1=//p" "$VERSION_FILE")"
  [ -n "$value" ] || { echo "fetch-restic: $1 missing in $VERSION_FILE" >&2; exit 1; }
  echo "$value"
}

VERSION="$(pinned version)"
BASE="https://github.com/restic/restic/releases/download/v$VERSION"
DEST="$ROOT/.bin/restic/$VERSION"
DL="$DEST/download"

fetch() { curl --proto '=https' --tlsv1.2 -sSfL --retry 3 -o "$2" "$1"; }

host_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) echo "fetch-restic: unsupported host architecture $(uname -m)" >&2; exit 1 ;;
  esac
}

check_signature() {
  local fpr key="${RESTIC_SIGNING_KEY:-$ROOT/agent/internal/restic/restic-signing-key.asc}"
  command -v gpg >/dev/null || { echo "fetch-restic: gpg is needed to check the signature of SHA256SUMS" >&2; exit 1; }
  [ -f "$key" ] || { echo "fetch-restic: no restic signing key at $key" >&2; exit 1; }
  fpr="$(pinned signing_key_fingerprint)"
  fetch "$BASE/SHA256SUMS.asc" "$DL/SHA256SUMS.asc"
  verify_signature "$key" "$DL/SHA256SUMS.asc" "$DL/SHA256SUMS" "$fpr" ||
    { echo "fetch-restic: SHA256SUMS is not signed by the pinned key $fpr" >&2; exit 1; }
  echo "fetch-restic: SHA256SUMS signature OK ($fpr)"
}

# verify_signature succeeds only when sig is a good signature over data made
# by the key with fingerprint fpr (or a subkey of it). The signer comes from
# gpg's VALIDSIG status, not from the keys in the file: a key file holding
# the pinned key and another one must not let the other one sign.
verify_signature() {
  local key="$1" sig="$2" data="$3" fpr="$4" home status ok=1
  home="$(mktemp -d)"
  if GNUPGHOME="$home" gpg --batch --quiet --import "$key" 2>/dev/null &&
    status="$(GNUPGHOME="$home" gpg --batch --status-fd 1 --verify "$sig" "$data" 2>/dev/null)"; then
    # [GNUPG:] VALIDSIG <signing key fpr> ... <primary key fpr>
    awk -v fpr="$fpr" '$2 == "VALIDSIG" && ($3 == fpr || $NF == fpr) { found = 1 } END { exit !found }' <<<"$status" && ok=0
  fi
  rm -rf "$home"
  return "$ok"
}

install_arch() {
  local arch="$1" archive want got
  archive="restic_${VERSION}_linux_${arch}.bz2"
  want="$(pinned "sha256_linux_$arch")"
  fetch "$BASE/$archive" "$DL/$archive"
  got="$(sha256sum "$DL/$archive" | cut -d' ' -f1)"
  [ "$got" = "$want" ] || { echo "fetch-restic: $archive: sha256 $got, pinned $want" >&2; exit 1; }
  grep -qx "$want  $archive" "$DL/SHA256SUMS" || { echo "fetch-restic: $archive: pinned sha256 not in release SHA256SUMS" >&2; exit 1; }
  mkdir -p "$DEST/linux_$arch"
  bzip2 -dc "$DL/$archive" >"$DEST/linux_$arch/restic.tmp"
  chmod 0755 "$DEST/linux_$arch/restic.tmp"
  mv "$DEST/linux_$arch/restic.tmp" "$DEST/linux_$arch/restic"
  cp "$ROOT/third_party/restic/LICENSE" "$DEST/linux_$arch/LICENSE.restic"
  echo "fetch-restic: $DEST/linux_$arch/restic ($archive sha256 OK)"
}

# update_pins rewrites the sha256_linux_* lines of the version file from the
# release's SHA256SUMS, once its signature by the pinned key checks out; the
# version, the fingerprint and everything else stay as they are.
update_pins() {
  local arch sha
  mkdir -p "$DL"
  fetch "$BASE/SHA256SUMS" "$DL/SHA256SUMS"
  check_signature
  for arch in amd64 arm64; do
    sha="$(awk -v f="restic_${VERSION}_linux_$arch.bz2" '$2 == f { print $1 }' "$DL/SHA256SUMS")"
    [[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "fetch-restic: no linux_$arch archive in SHA256SUMS of $VERSION" >&2; exit 1; }
    sed -i "s/^sha256_linux_$arch=.*/sha256_linux_$arch=$sha/" "$VERSION_FILE"
  done
  echo "fetch-restic: $VERSION_FILE pinned to the signed SHA256SUMS of restic $VERSION"
}

main() {
  [ "${1:-}" = --update-pins ] && { update_pins; return; }
  mkdir -p "$DL"
  fetch "$BASE/SHA256SUMS" "$DL/SHA256SUMS"
  check_signature
  [ "$#" -gt 0 ] || set -- "$(host_arch)"
  for arch in "$@"; do
    case "$arch" in
      amd64 | arm64) install_arch "$arch" ;;
      *) echo "fetch-restic: unsupported architecture $arch (amd64, arm64)" >&2; exit 1 ;;
    esac
  done
}

# Sourced (to check verify_signature), the script only defines functions.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
