#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Verifies a signed agent release the way a person (or CI) would:
#
#   scripts/release-verify.sh <dist> <version> [<public key file>]
#
# 1. SHA256SUMS.minisig is a valid signature of SHA256SUMS by the release key
#    (default deploy/release/sard-release.pub) with the trusted comment
#    "sard-agent <version>";
# 2. every file listed in SHA256SUMS is present and matches its sum, and no
#    file in <dist> is left out of SHA256SUMS;
# 3. manifest.json names <version>.
#
# The same check by hand, with only minisign and coreutils:
#   minisign -Vm SHA256SUMS -p sard-release.pub && sha256sum -c SHA256SUMS
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
die() { echo "release-verify: $*" >&2; exit 1; }

[ "$#" -ge 2 ] || die "usage: release-verify.sh <dist> <version> [<public key file>]"
DIST="$1"
VERSION="$2"
PUBKEY="${3:-$ROOT/deploy/release/sard-release.pub}"
[ -f "$PUBKEY" ] || die "public key $PUBKEY missing"
command -v minisign >/dev/null || die "minisign not installed"
for f in SHA256SUMS SHA256SUMS.minisig manifest.json; do
  [ -f "$DIST/$f" ] || die "$DIST/$f missing"
done

comment="$(minisign -V -q -Q -p "$PUBKEY" -m "$DIST/SHA256SUMS" -x "$DIST/SHA256SUMS.minisig")" ||
  die "SHA256SUMS: signature does not verify with $PUBKEY"
[ "$comment" = "sard-agent $VERSION" ] ||
  die "SHA256SUMS: signed for \"$comment\", expected \"sard-agent $VERSION\""

(cd "$DIST" && sha256sum --check --strict --quiet SHA256SUMS) || die "SHA256SUMS: a file does not match"
listed="$(awk '{print $2}' "$DIST/SHA256SUMS" | sort)"
present="$(cd "$DIST" && find . -maxdepth 1 -type f ! -name SHA256SUMS ! -name SHA256SUMS.minisig -printf '%f\n' | sort)"
[ "$listed" = "$present" ] || die "SHA256SUMS does not list exactly the files in $DIST"

grep -q "^  \"version\": \"$VERSION\",\$" "$DIST/manifest.json" || die "manifest.json: version is not $VERSION"
echo "release-verify: $DIST is sard-agent $VERSION, signed by $(sed -n 's/^untrusted comment: //p' "$PUBKEY")"
