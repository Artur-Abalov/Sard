#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Verifies the server assets of a release (SC1, ADR 00XX-draft-supply-chain):
# SHA256SUMS.server is signed by the release key with "sard-server <version>"
# and lists exactly the files of the directory, each with its sum. The release
# workflow runs it before it uploads them; an operator checks the files they
# downloaded with minisign and sha256sum (docs/operator/11-offline.md).
#
#   scripts/release-verify-server.sh <dir> <version> [<public key file>]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
die() { echo "release-verify-server: $*" >&2; exit 1; }

[ "$#" -ge 2 ] || die "usage: release-verify-server.sh <dir> <version> [<public key file>]"
DIR="$1"
VERSION="$2"
PUBKEY="${3:-$ROOT/deploy/release/sard-release.pub}"
[ -f "$PUBKEY" ] || die "public key $PUBKEY missing"
command -v minisign >/dev/null || die "minisign not installed"
for f in SHA256SUMS.server SHA256SUMS.server.minisig; do
  [ -f "$DIR/$f" ] || die "$DIR/$f missing"
done

comment="$(minisign -V -q -Q -p "$PUBKEY" -m "$DIR/SHA256SUMS.server" -x "$DIR/SHA256SUMS.server.minisig")" ||
  die "SHA256SUMS.server: signature does not verify with $PUBKEY"
[ "$comment" = "sard-server $VERSION" ] ||
  die "SHA256SUMS.server: signed for \"$comment\", expected \"sard-server $VERSION\""

(cd "$DIR" && sha256sum --check --strict --quiet SHA256SUMS.server) || die "SHA256SUMS.server: a file does not match"
listed="$(awk '{print $2}' "$DIR/SHA256SUMS.server" | sort)"
present="$(cd "$DIR" && find . -maxdepth 1 -type f ! -name SHA256SUMS.server ! -name SHA256SUMS.server.minisig -printf '%f\n' | sort)"
[ "$listed" = "$present" ] || die "SHA256SUMS.server does not list exactly the files in $DIR"
echo "release-verify-server: $DIR is sard-server $VERSION, signed by $(sed -n 's/^untrusted comment: //p' "$PUBKEY")"
