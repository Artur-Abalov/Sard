#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/fetch-restic.sh (SC1) against a fake restic release in a
# temporary directory, signed by throwaway GPG keys: nothing is downloaded.
# The packaged restic is installed only when the archive matches the pinned
# SHA-256 and the release's SHA256SUMS, and SHA256SUMS is signed by the key
# with the pinned fingerprint; anything else fails the build.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-fetch-restic: FAIL: $*" >&2; exit 1; }
command -v gpg >/dev/null || die "gpg is needed"

# A key in its own keyring; prints its fingerprint, exports it to $WORK/<name>.asc.
new_key() {
  local home="$WORK/gnupg-$1"
  mkdir -m 700 "$home"
  GNUPGHOME="$home" gpg --batch --quiet --passphrase '' --quick-gen-key "$1 <$1@test.invalid>" ed25519 sign never 2>/dev/null
  GNUPGHOME="$home" gpg --batch --armor --export >"$WORK/$1.asc" 2>/dev/null
  GNUPGHOME="$home" gpg --batch --with-colons --list-keys 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }'
}
sign() { # key name, file
  GNUPGHOME="$WORK/gnupg-$1" gpg --batch --yes --quiet --armor --detach-sign -o "$2.asc" "$2"
}

PINNED_FPR="$(new_key restic)"
new_key mallory >/dev/null

# The fake release v9.9.9: one amd64 archive, SHA256SUMS and its signature.
REL="$WORK/release"
mkdir "$REL"
printf '#!/bin/sh\necho restic 9.9.9\n' | bzip2 >"$REL/restic_9.9.9_linux_amd64.bz2"
GOOD_SHA="$(sha256sum "$REL/restic_9.9.9_linux_amd64.bz2" | cut -d' ' -f1)"
(cd "$REL" && sha256sum restic_9.9.9_linux_amd64.bz2 >SHA256SUMS)
sign restic "$REL/SHA256SUMS"

# run <description> <want exit> [VAR=value...]: fetch-restic in a subshell,
# with the fake release, a pin file and a fresh destination.
run() {
  local what="$1" want="$2" rc=0
  shift 2
  rm -rf "$WORK/root"
  mkdir -p "$WORK/root/agent/internal/restic" "$WORK/root/third_party/restic"
  echo license >"$WORK/root/third_party/restic/LICENSE"
  [ -z "${KEY_IN_ROOT:-}" ] || cp "$WORK/restic.asc" "$WORK/root/agent/internal/restic/restic-signing-key.asc"
  cat >"$WORK/root/agent/internal/restic/restic-version" <<PIN
version=9.9.9
sha256_linux_amd64=${PIN_SHA:-$GOOD_SHA}
signing_key_fingerprint=$PINNED_FPR
PIN
  (
    for assignment in "$@"; do export "${assignment?}"; done
    # shellcheck source=fetch-restic.sh
    . "$HERE/fetch-restic.sh"
    ROOT="$WORK/root"
    VERSION_FILE="$ROOT/agent/internal/restic/restic-version"
    VERSION=9.9.9
    RESTIC_SIGNING_KEY="${RESTIC_SIGNING_KEY-$WORK/restic.asc}"
    DEST="$ROOT/.bin/restic/9.9.9"
    DL="$DEST/download"
    BASE="$REL"
    fetch() { cp "$1" "$2"; }
    main amd64
  ) >"$WORK/out" 2>&1 || rc=$?
  [ "$rc" -eq 0 ] && rc=0 || rc=1
  [ "$rc" -eq "$want" ] || { cat "$WORK/out" >&2; die "$what: exit $rc, want $want"; }
}

run "the pinned release" 0
[ -x "$WORK/root/.bin/restic/9.9.9/linux_amd64/restic" ] || die "the pinned release: no restic installed"
grep -q "signature OK ($PINNED_FPR)" "$WORK/out" || die "the pinned release: signature not reported checked"

KEY_IN_ROOT=1 run "no RESTIC_SIGNING_KEY: the key of the repository" 0 RESTIC_SIGNING_KEY=
grep -q "signature OK ($PINNED_FPR)" "$WORK/out" || die "the key of the repository: signature not reported checked"
run "no RESTIC_SIGNING_KEY and no key in the repository" 1 RESTIC_SIGNING_KEY=
grep -q "no restic signing key" "$WORK/out" || die "no key: reason not given"
# A PATH with every tool fetch-restic.sh needs but gpg (a hashed command
# would otherwise be found anyway).
mkdir "$WORK/no-gpg"
for tool in bash dirname sed awk curl sha256sum grep bzip2 mkdir chmod mv cp cat rm mktemp uname cut; do
  ln -s "$(command -v "$tool")" "$WORK/no-gpg/$tool"
done
run "no gpg" 1 PATH="$WORK/no-gpg"
grep -q "gpg is needed" "$WORK/out" || die "no gpg: reason not given"

PIN_SHA="$(printf '%064d' 0)" run "another SHA-256 pinned" 1
grep -q "pinned" "$WORK/out" || die "another SHA-256 pinned: reason not given"

cp "$REL/restic_9.9.9_linux_amd64.bz2" "$WORK/good.bz2"
printf 'evil' | bzip2 >"$REL/restic_9.9.9_linux_amd64.bz2"
run "a substituted archive" 1
cp "$WORK/good.bz2" "$REL/restic_9.9.9_linux_amd64.bz2"

cp "$REL/SHA256SUMS" "$WORK/sums" && cp "$REL/SHA256SUMS.asc" "$WORK/sums.asc"
sign mallory "$REL/SHA256SUMS"
run "SHA256SUMS signed by another key" 1
grep -q "not signed by the pinned key" "$WORK/out" || die "another signer: reason not given"
run "another key offered as the release key" 1 RESTIC_SIGNING_KEY="$WORK/mallory.asc"

printf 'other\n' >>"$REL/SHA256SUMS"
sign restic "$REL/SHA256SUMS"
run "another line in a signed SHA256SUMS" 0
: >"$REL/SHA256SUMS"
sign restic "$REL/SHA256SUMS"
run "the archive missing from a signed SHA256SUMS" 1
cp "$WORK/sums" "$REL/SHA256SUMS" && cp "$WORK/sums.asc" "$REL/SHA256SUMS.asc"
printf 'tampered\n' >>"$REL/SHA256SUMS"
run "SHA256SUMS tampered with after signing" 1

# update-pins: after Renovate moves version=, the SHA-256 lines are taken from
# the release's SHA256SUMS, and only once its signature checks out.
cp "$WORK/sums" "$REL/SHA256SUMS" && cp "$WORK/sums.asc" "$REL/SHA256SUMS.asc"
(cd "$REL" && cp restic_9.9.9_linux_amd64.bz2 restic_9.9.9_linux_arm64.bz2 &&
  sha256sum restic_9.9.9_linux_amd64.bz2 restic_9.9.9_linux_arm64.bz2 >SHA256SUMS)
sign restic "$REL/SHA256SUMS"
pin_file="$WORK/root/agent/internal/restic/restic-version"
update() { # description, want exit
  local rc=0 stale
  stale="$(printf '%064d' 1)"
  printf 'version=9.9.9\nsha256_linux_amd64=%s\nsha256_linux_arm64=%s\nsigning_key_fingerprint=%s\n' \
    "$stale" "$stale" "$PINNED_FPR" >"$pin_file"
  (
    # shellcheck source=fetch-restic.sh
    . "$HERE/fetch-restic.sh"
    ROOT="$WORK/root"
    VERSION_FILE="$pin_file"
    VERSION=9.9.9
    RESTIC_SIGNING_KEY="$WORK/restic.asc"
    DEST="$ROOT/.bin/restic/9.9.9"
    DL="$DEST/download"
    BASE="$REL"
    fetch() { cp "$1" "$2"; }
    update_pins
  ) >"$WORK/out" 2>&1 || rc=1
  [ "$rc" -eq "$2" ] || { cat "$WORK/out" >&2; die "$1: exit $rc, want $2"; }
}
update "update-pins" 0
grep -qx "sha256_linux_amd64=$GOOD_SHA" "$pin_file" || die "update-pins: amd64 not pinned: $(cat "$pin_file")"
grep -qx "sha256_linux_arm64=$GOOD_SHA" "$pin_file" || die "update-pins: arm64 not pinned: $(cat "$pin_file")"
grep -qx "signing_key_fingerprint=$PINNED_FPR" "$pin_file" || die "update-pins: the fingerprint changed"
grep -qx "version=9.9.9" "$pin_file" || die "update-pins: the version changed"
(cd "$REL" && sha256sum restic_9.9.9_linux_amd64.bz2 >SHA256SUMS)
sign restic "$REL/SHA256SUMS"
update "update-pins with no arm64 archive in SHA256SUMS" 1
grep -qx "sha256_linux_amd64=$(printf '%064d' 1)" "$pin_file" || die "a failed update-pins changed the amd64 pin"
sign mallory "$REL/SHA256SUMS"
update "update-pins with SHA256SUMS signed by another key" 1
grep -qx "sha256_linux_amd64=$(printf '%064d' 1)" "$pin_file" || die "a wrongly signed SHA256SUMS changed the pins"

echo "test-fetch-restic: OK"
