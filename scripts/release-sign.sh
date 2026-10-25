#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Signs a release's SHA256SUMS with the release key (minisign, Ed25519):
#
#   MINISIGN_SECRET_KEY=<contents of the encrypted .key file> \
#   MINISIGN_PASSWORD=<its password> scripts/release-sign.sh <dist> <version>
#
# Writes <dist>/SHA256SUMS.minisig with the trusted comment
# "sard-agent <version>", so a signature cannot be passed off for another
# version. With a third argument SHA256SUMS.server it signs the server assets
# (offline archives, compose file, images.json, image SBOMs; SC1) instead:
# <dist>/SHA256SUMS.server.minisig, "sard-server <version>". Run by the release workflow only (.github/workflows/release.yml,
# docs/adr/0043-agent-release.md): the key lives in the
# "release" environment's secrets, is written to a private temporary file for
# the one minisign call and removed on exit. It never reaches the logs, the
# repository or an artifact.
set -euo pipefail

die() { echo "release-sign: $*" >&2; exit 1; }

[ "$#" -eq 2 ] || [ "$#" -eq 3 ] || die "usage: release-sign.sh <dist> <version> [SHA256SUMS | SHA256SUMS.server]"
DIST="$1"
VERSION="$2"
SUMS="${3:-SHA256SUMS}"
case "$SUMS" in
  SHA256SUMS) COMMENT="sard-agent $VERSION" ;;
  SHA256SUMS.server) COMMENT="sard-server $VERSION" ;;
  *) die "$SUMS: sign SHA256SUMS or SHA256SUMS.server" ;;
esac
[ -n "${MINISIGN_SECRET_KEY:-}" ] || die "MINISIGN_SECRET_KEY is empty"
[ -n "${MINISIGN_PASSWORD:-}" ] || die "MINISIGN_PASSWORD is empty"
[ -f "$DIST/$SUMS" ] || die "$DIST/$SUMS missing"
command -v minisign >/dev/null || die "minisign not installed"

umask 077
key="$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/sard-release.XXXXXX")"
trap 'rm -f "$key"' EXIT
printf '%s\n' "$MINISIGN_SECRET_KEY" >"$key"
printf '%s\n' "$MINISIGN_PASSWORD" |
  minisign -S -s "$key" -m "$DIST/$SUMS" -x "$DIST/$SUMS.minisig" -t "$COMMENT" >/dev/null
echo "release-sign: $DIST/$SUMS.minisig ($COMMENT)"
