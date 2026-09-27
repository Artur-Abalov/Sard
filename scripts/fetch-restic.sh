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
#
# The archive must match the SHA-256 pinned in the version file AND the line
# in the release's SHA256SUMS. The signature on SHA256SUMS is checked when gpg
# is installed and RESTIC_SIGNING_KEY names a file holding the release key;
# its fingerprint must equal the pinned one. REQUIRE_SIGNATURE=1 makes a
# missing check fatal.
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
  if [ -z "${RESTIC_SIGNING_KEY:-}" ] || ! command -v gpg >/dev/null; then
    [ "${REQUIRE_SIGNATURE:-0}" = 1 ] && { echo "fetch-restic: signature required but gpg or RESTIC_SIGNING_KEY missing" >&2; exit 1; }
    echo "fetch-restic: signature not checked (needs gpg and RESTIC_SIGNING_KEY); SHA-256 pinned in the repo"
    return
  fi
  local fpr
  fpr="$(pinned signing_key_fingerprint)"
  fetch "$BASE/SHA256SUMS.asc" "$DL/SHA256SUMS.asc"
  verify_signature "$RESTIC_SIGNING_KEY" "$DL/SHA256SUMS.asc" "$DL/SHA256SUMS" "$fpr" ||
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

main() {
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
