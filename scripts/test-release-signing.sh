#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Shows that the release signature guarantees something
# (docs/adr/0041-agent-release.md): signs a copy of a built
# release with a throwaway test key, made here and never stored, then checks
# that scripts/release-verify.sh accepts it and refuses every forgery.
#
#   scripts/test-release-signing.sh <dist> <version>     e.g. dist/ after make package
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
die() { echo "test-release-signing: FAIL: $*" >&2; exit 1; }

[ "$#" -eq 2 ] || die "usage: test-release-signing.sh <dist> <version>"
SRC="$1"
VERSION="$2"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# keygen <name>: an encrypted minisign key pair <name>.key / <name>.pub in $WORK.
keygen() {
  printf 'test-%s\ntest-%s\n' "$1" "$1" | minisign -G -f -p "$WORK/$1.pub" -s "$WORK/$1.key" >/dev/null 2>&1
}

# sign <dir> <key name> <version>: release-sign.sh with that key.
sign() {
  MINISIGN_SECRET_KEY="$(cat "$WORK/$2.key")" MINISIGN_PASSWORD="test-$2" \
    "$ROOT/scripts/release-sign.sh" "$1" "$3" >/dev/null 2>&1
}

# fresh <name>: a copy of the release under $WORK/<name>, signed by the test key.
fresh() {
  mkdir -p "$WORK/$1"
  find "$SRC" -maxdepth 1 -type f ! -name SHA256SUMS.minisig -exec cp {} "$WORK/$1/" \;
  sign "$WORK/$1" release "$VERSION"
  echo "$WORK/$1"
}

accepts() {
  "$ROOT/scripts/release-verify.sh" "$1" "$VERSION" "$WORK/release.pub" >/dev/null 2>&1 || die "$2: refused"
  echo "ok: $2 — accepted"
}

# refuses <dir> <what> <reason>: verification fails, and for that reason.
refuses() {
  local out
  ! out="$("$ROOT/scripts/release-verify.sh" "$1" "$VERSION" "$WORK/release.pub" 2>&1)" || die "$2: accepted"
  grep -qF -- "$3" <<<"$out" || die "$2: refused for another reason: $out"
  echo "ok: $2 — refused ($3)"
}

# flip_byte <file> <offset>: changes exactly one byte.
flip_byte() {
  local old new
  old="$(od -An -tu1 -j "$2" -N1 "$1" | tr -d ' ')"
  new=$(((old + 1) % 256))
  printf "$(printf '\\%03o' "$new")" | dd of="$1" bs=1 seek="$2" conv=notrunc status=none
}

command -v minisign >/dev/null || die "minisign not installed"
keygen release
keygen foreign
deb="$(cd "$SRC" && ls -1 *.deb | head -1)"

accepts "$(fresh good)" "the release as built"

d="$(fresh byte)"
flip_byte "$d/$deb" 4096
refuses "$d" "$deb changed by one byte" "a file does not match"

d="$(fresh sums)"
flip_byte "$d/$deb" 4096
(cd "$d" && awk '{print $2}' SHA256SUMS | xargs sha256sum -- >"$WORK/recomputed-sums" && mv "$WORK/recomputed-sums" SHA256SUMS)
refuses "$d" "$deb changed and SHA256SUMS recomputed without the key" "signature does not verify"

d="$(fresh foreign)"
sign "$d" foreign "$VERSION"
refuses "$d" "SHA256SUMS signed by a foreign key" "signature does not verify"

d="$(fresh version)"
sign "$d" release "v0.0.0-other"
refuses "$d" "signature made for another version" "expected \"sard-agent $VERSION\""

d="$(fresh missing)"
rm "$d/$deb"
refuses "$d" "$deb missing" "a file does not match"

d="$(fresh extra)"
echo extra >"$d/sard-agent_extra.deb"
refuses "$d" "a file not covered by SHA256SUMS" "does not list exactly"

d="$(fresh nosig)"
rm "$d/SHA256SUMS.minisig"
refuses "$d" "no signature" "SHA256SUMS.minisig missing"
echo "test-release-signing: all checks passed"
